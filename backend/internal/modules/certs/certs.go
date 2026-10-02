// Package certs 证书自动续期与发放（P5 M5）。
// lego（ACME）DNS challenge 申请/续期；到期前 30 天自动续（扫描型调度，
// 对齐 backup 模块模式）；发放复用 SSH：上传证书 → nginx -t 通过才 reload
// （失败告警走统一通知路由 cert_expiring 源）。消化遗留 #16（生产 HTTPS 未配）。
package certs

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"
	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/modules/notify"
	cryptopkg "github.com/custos-machina/backend/internal/pkg/crypto"
	"github.com/custos-machina/backend/internal/pkg/jobs"
	"github.com/custos-machina/backend/internal/pkg/logger"
)

var ErrNotFound = errors.New("证书不存在")

// CA 目录。
const (
	CALetsEncrypt        = "https://acme-v02.api.letsencrypt.org/directory"
	CALetsEncryptStaging = "https://acme-staging-v02.api.letsencrypt.org/directory"
	renewBefore          = 30 * 24 * time.Hour // 到期前 30 天触发续期
	retryBackoff         = 24 * time.Hour      // 失败退避
	warnBefore           = 14 * 24 * time.Hour // 到期告警（走统一通知路由）
)

// 状态。
const (
	StatusPending = "pending"
	StatusIssued  = "issued"
	StatusFailed  = "failed"
)

// Cert 证书定义与状态。
type Cert struct {
	ID uint `gorm:"primarykey" json:"id"`
	// 定义
	Name          string `gorm:"size:64;not null" json:"name"`
	Domains       string `gorm:"size:512;not null" json:"domains"` // 逗号分隔（SAN）
	Email         string `gorm:"size:128;not null" json:"email"`   // ACME 账号
	CADirURL      string `gorm:"size:255;not null;default:https://acme-v02.api.letsencrypt.org/directory" json:"caDirUrl"`
	DNSProvider   string `gorm:"size:32;not null" json:"dnsProvider"` // alidns|cloudflare|dnspod|huaweicloud|gandi|godaddy
	CredsEnc      string `gorm:"type:text" json:"-"`                  // DNS 凭证 JSON（AES）
	AccountKeyEnc string `gorm:"type:text" json:"-"`                  // ACME 账号私钥（AES，复用免重复注册）
	// 部署目标
	ServerID  uint   `gorm:"not null" json:"serverId"`
	CertPath  string `gorm:"size:512;not null" json:"certPath"` // 含 fullchain
	KeyPath   string `gorm:"size:512;not null" json:"keyPath"`
	NginxTest string `gorm:"size:512" json:"nginxTest"` // 可选：nginx -t 工作目录/命令上下文（空 = 直接 nginx -t）
	// 状态
	Enabled   bool       `gorm:"not null;default:true" json:"enabled"`
	Status    string     `gorm:"size:16;not null;default:pending" json:"status"`
	ExpiresAt *time.Time `json:"expiresAt"`
	LastError string     `gorm:"size:512" json:"lastError"`
	NextTryAt *time.Time `gorm:"index" json:"nextTryAt"` // 续期/重试时间（含退避）
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

func (Cert) TableName() string { return "certs" }

func Models() []any { return []any{&Cert{}} }

// EventNotifier 统一通知路由出口。
type EventNotifier interface {
	NotifyEvent(ctx context.Context, source, level, dedupKey, title, detail string)
}

// SSHExecutor resources.Service 投影。
type SSHExecutor interface {
	SftpWrite(serverID uint, name string, content []byte) error
	RunCommandOn(ctx context.Context, serverID uint, cmd, stdin string, timeout time.Duration) (string, error)
	RecordEvent(ctx context.Context, serverID uint, typ, msg string)
}

type Service struct {
	db       *gorm.DB
	cipher   *cryptopkg.Cipher
	ssh      SSHExecutor
	notifier EventNotifier
}

func NewService(db *gorm.DB, cipher *cryptopkg.Cipher, ssh SSHExecutor) *Service {
	return &Service{db: db, cipher: cipher, ssh: ssh}
}

func (s *Service) SetNotifier(n EventNotifier) { s.notifier = n }

// ---- CRUD ----

var validProviders = map[string]bool{
	"alidns": true, "cloudflare": true, "dnspod": true,
	"huaweicloud": true, "gandi": true, "godaddy": true, "tencentcloud": true,
}

type SaveInput struct {
	Name        string `json:"name" binding:"required,max=64"`
	Domains     string `json:"domains" binding:"required,max=512"`
	Email       string `json:"email" binding:"required,email,max=128"`
	CADirURL    string `json:"caDirUrl" binding:"omitempty,max=255"`
	DNSProvider string `json:"dnsProvider" binding:"required"`
	// JSON 对象：各 provider 的环境变量（如 {"ALICLOUD_ACCESS_KEY":"...","ALICLOUD_SECRET_KEY":"..."}）
	Credentials map[string]string `json:"credentials"`
	ServerID    uint              `json:"serverId" binding:"required"`
	CertPath    string            `json:"certPath" binding:"required,max=512"`
	KeyPath     string            `json:"keyPath" binding:"required,max=512"`
	Enabled     bool              `json:"enabled"`
}

func (s *Service) validate(in SaveInput) error {
	if !strings.Contains(in.Email, "@") || len(in.Email) < 5 {
		return fmt.Errorf("ACME 账号邮箱不合法")
	}
	if !validProviders[in.DNSProvider] {
		return fmt.Errorf("暂不支持的 DNS provider %q（支持 alidns/cloudflare/dnspod/tencentcloud/huaweicloud/gandi/godaddy）", in.DNSProvider)
	}
	if in.CADirURL != "" && !strings.HasPrefix(in.CADirURL, "https://") {
		return fmt.Errorf("CA 目录 URL 须为 https://")
	}
	for d := range strings.SplitSeq(in.Domains, ",") {
		d = strings.TrimSpace(d)
		if d == "" || len(d) > 253 {
			return fmt.Errorf("域名不合法: %q", d)
		}
	}
	return nil
}

func (s *Service) encryptJSON(m map[string]string) (string, error) {
	if len(m) == 0 {
		return "", nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return s.cipher.Encrypt(string(b))
}

// Create 建立定义（立即排一次签发）。
func (s *Service) Create(ctx context.Context, in SaveInput) (*Cert, error) {
	if err := s.validate(in); err != nil {
		return nil, err
	}
	if s.cipher == nil {
		return nil, cryptopkg.ErrNoMasterKey
	}
	credsEnc, err := s.encryptJSON(in.Credentials)
	if err != nil {
		return nil, err
	}
	ca := in.CADirURL
	if ca == "" {
		ca = CALetsEncrypt
	}
	now := time.Now()
	c := Cert{Name: in.Name, Domains: strings.TrimSpace(in.Domains), Email: in.Email,
		CADirURL: ca, DNSProvider: in.DNSProvider, CredsEnc: credsEnc,
		ServerID: in.ServerID, CertPath: in.CertPath, KeyPath: in.KeyPath,
		Enabled: in.Enabled, Status: StatusPending, NextTryAt: &now}
	if err := s.db.WithContext(ctx).Create(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Service) Update(ctx context.Context, id uint, in SaveInput) (*Cert, error) {
	var c Cert
	if err := s.db.WithContext(ctx).First(&c, id).Error; err != nil {
		return nil, ErrNotFound
	}
	if err := s.validate(in); err != nil {
		return nil, err
	}
	c.Name, c.Domains, c.Email = in.Name, strings.TrimSpace(in.Domains), in.Email
	if in.CADirURL != "" {
		c.CADirURL = in.CADirURL
	}
	c.DNSProvider, c.ServerID = in.DNSProvider, in.ServerID
	c.CertPath, c.KeyPath, c.Enabled = in.CertPath, in.KeyPath, in.Enabled
	if len(in.Credentials) > 0 { // 留空保留
		enc, err := s.encryptJSON(in.Credentials)
		if err != nil {
			return nil, err
		}
		c.CredsEnc = enc
	}
	// 域名/CA 变了需要重签
	if err := s.db.WithContext(ctx).Save(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Service) Delete(ctx context.Context, id uint) error {
	res := s.db.WithContext(ctx).Delete(&Cert{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

type CertOut struct {
	Cert
	HasCreds bool `json:"hasCreds"`
}

func toOut(c Cert) CertOut { return CertOut{Cert: c, HasCreds: c.CredsEnc != ""} }

func (s *Service) List(ctx context.Context) ([]CertOut, error) {
	var cs []Cert
	if err := s.db.WithContext(ctx).Order("id").Find(&cs).Error; err != nil {
		return nil, err
	}
	out := make([]CertOut, len(cs))
	for i, c := range cs {
		out[i] = toOut(c)
	}
	return out, nil
}

// RenewNow 手动触发签发/续期（立即，忽略 NextTryAt）。
func (s *Service) RenewNow(ctx context.Context, id uint) error {
	var c Cert
	if err := s.db.WithContext(ctx).First(&c, id).Error; err != nil {
		return ErrNotFound
	}
	return s.issue(ctx, &c)
}

// ---- 签发与部署 ----

// markResult 落结果到 DB（不回写传入的 Cert——issue 由 scanDue 的 goroutine
// 持有副本，写共享 struct 会与 warnExpiring 的读构成数据竞争）。
func (s *Service) markResult(c *Cert, status string, expires *time.Time, err error) {
	lastErr := ""
	if err != nil {
		lastErr = truncStr(err.Error(), 500)
	}
	next := time.Now().Add(retryBackoff)
	if err == nil && expires != nil {
		next = expires.Add(-renewBefore)
	}
	s.db.Model(&Cert{}).Where("id = ?", c.ID).Updates(map[string]any{
		"status": status, "expires_at": expires, "last_error": lastErr, "next_try_at": next,
	})
}

func (s *Service) issue(ctx context.Context, c *Cert) error {
	key, err := s.accountKey(c)
	if err != nil {
		s.markResult(c, StatusFailed, c.ExpiresAt, err)
		return err
	}
	user := &acmeUser{email: c.Email, key: key}
	cfg := lego.NewConfig(user)
	cfg.CADirURL = c.CADirURL
	cfg.Certificate.KeyType = certcrypto.EC256

	client, err := lego.NewClient(cfg)
	if err != nil {
		s.markResult(c, StatusFailed, c.ExpiresAt, err)
		return err
	}
	creds, err := s.dnsCredentials(c)
	if err != nil {
		s.markResult(c, StatusFailed, c.ExpiresAt, err)
		return err
	}
	provider, err := buildDNSProvider(c.DNSProvider, creds)
	if err != nil {
		s.markResult(c, StatusFailed, c.ExpiresAt, err)
		return err
	}
	if err := client.Challenge.SetDNS01Provider(provider); err != nil {
		s.markResult(c, StatusFailed, c.ExpiresAt, err)
		return err
	}

	// ACME 账号注册/复用
	if user.registration == nil {
		reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			s.markResult(c, StatusFailed, c.ExpiresAt, fmt.Errorf("ACME 注册失败: %w", err))
			return err
		}
		user.registration = reg
		if err := s.saveAccountKey(c, key); err != nil {
			// 非致命：下次重新注册
			_ = err
		}
	}

	domains := splitDomains(c.Domains)
	res, err := client.Certificate.Obtain(certificate.ObtainRequest{Domains: domains, Bundle: true})
	if err != nil {
		s.markResult(c, StatusFailed, c.ExpiresAt, fmt.Errorf("签发失败: %w", err))
		s.notifyFailure(c, err)
		return err
	}

	// 部署：上传 → nginx -t → reload（脱离请求 ctx：签发后部署被客户端
	// 断开拦腰截断是最坏情形——证书已耗限额却没落盘）
	if err := s.deploy(context.WithoutCancel(ctx), c, res); err != nil {
		s.markResult(c, StatusFailed, c.ExpiresAt, fmt.Errorf("已签发但部署失败: %w", err))
		s.notifyFailure(c, err)
		return err
	}
	exp := time.Now().Add(90 * 24 * time.Hour)
	if res.Certificate != nil {
		if leaf, parseErr := certcrypto.ParsePEMCertificate(res.Certificate); parseErr == nil && leaf.NotAfter.After(time.Now()) {
			exp = leaf.NotAfter
		}
	}
	s.markResult(c, StatusIssued, &exp, nil)
	s.audit(ctx, c, fmt.Sprintf("证书 %s（%s）签发部署成功，到期 %s", c.Name, c.Domains, exp.Format(time.DateOnly)))
	return nil
}

func (s *Service) deploy(ctx context.Context, c *Cert, res *certificate.Resource) error {
	if s.ssh == nil {
		return fmt.Errorf("SSH 执行器未注入")
	}
	if err := s.ssh.SftpWrite(c.ServerID, c.CertPath, res.Certificate); err != nil {
		return fmt.Errorf("上传证书失败: %w", err)
	}
	if err := s.ssh.SftpWrite(c.ServerID, c.KeyPath, res.PrivateKey); err != nil {
		return fmt.Errorf("上传私钥失败: %w", err)
	}
	out, err := s.ssh.RunCommandOn(ctx, c.ServerID, "nginx -t", "", 30*timeoutSecond)
	if err != nil {
		return fmt.Errorf("nginx -t 未通过（不 reload）: %v（%s）", err, truncStr(out, 200))
	}
	if _, err := s.ssh.RunCommandOn(ctx, c.ServerID, "nginx -s reload", "", 30*timeoutSecond); err != nil {
		return fmt.Errorf("reload 失败: %v", err)
	}
	return nil
}

const timeoutSecond = time.Second

// ---- 调度 ----

// NewScheduler 到期扫描（对齐 backup 模式）：到期前 30 天续期、失败退避 24h、
// 临近到期告警（14 天 warn / 7 天 critical，每天最多一条）。
func NewScheduler(svc *Service) (*Scheduler, func(), error) {
	g := jobs.NewGroup(
		jobs.Job{Name: "certs:sched", Interval: time.Hour, Fn: svc.scanDue},
	)
	g.Start()
	return &Scheduler{}, g.Stop, nil
}

type Scheduler struct{}

func (s *Service) scanDue(ctx context.Context) error {
	now := time.Now()
	var due []Cert
	if err := s.db.WithContext(ctx).
		Where("enabled = ? AND next_try_at IS NOT NULL AND next_try_at <= ?", true, now).
		Limit(10).Find(&due).Error; err != nil {
		return err
	}
	for i := range due {
		go func(c Cert) {
			if err := s.issue(context.WithoutCancel(ctx), &c); err != nil {
				logger.Warnf("[certs] 续期失败 %q: %v", c.Name, err)
			}
		}(due[i])
	}
	s.warnExpiring(ctx, now)
	return nil
}

// warnExpiring 临近到期告警（每天一次：按 ExpiresAt 小时数聚合去重窗口足够）。
func (s *Service) warnExpiring(ctx context.Context, now time.Time) {
	if s.notifier == nil {
		return
	}
	var expiring []Cert
	s.db.WithContext(ctx).Where("status = ? AND expires_at IS NOT NULL AND expires_at < ?",
		StatusIssued, now.Add(warnBefore)).Find(&expiring)
	for _, c := range expiring {
		level := notify.LevelWarn
		days := int(c.ExpiresAt.Sub(now).Hours() / 24)
		if days <= 7 {
			level = notify.LevelCritical
		}
		detail := fmt.Sprintf("证书：%s（%s）\n剩余 %d 天到期（%s）\n自动续期%s",
			c.Name, c.Domains, days, c.ExpiresAt.Format(time.DateOnly),
			map[bool]string{true: "已启用，若持续失败请检查 DNS 凭证", false: "未启用"}[c.Enabled])
		go func(c Cert, level, detail string) {
			s.notifier.NotifyEvent(context.WithoutCancel(ctx),
				notify.SourceCertExpiring, level,
				fmt.Sprintf("cert-%d", c.ID), "证书即将到期："+c.Name, detail)
		}(c, level, detail)
	}
}

// ---- 辅助 ----

type acmeUser struct {
	email        string
	key          crypto.PrivateKey
	registration *registration.Resource
}

func (u *acmeUser) GetEmail() string                        { return u.email }
func (u *acmeUser) GetRegistration() *registration.Resource { return u.registration }
func (u *acmeUser) GetPrivateKey() crypto.PrivateKey        { return u.key }

// accountKey ACME 账号私钥（首签生成并 AES 存行，复用避免重复注册）。
func (s *Service) accountKey(c *Cert) (crypto.PrivateKey, error) {
	if c.AccountKeyEnc != "" && s.cipher != nil {
		if pemStr, err := s.cipher.Decrypt(c.AccountKeyEnc); err == nil {
			if key, err := certcrypto.ParsePEMPrivateKey([]byte(pemStr)); err == nil {
				return key, nil
			}
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	return key, nil
}

func (s *Service) saveAccountKey(c *Cert, key crypto.PrivateKey) error {
	if s.cipher == nil {
		return nil
	}
	pemBytes := certcrypto.PEMEncode(key)
	enc, err := s.cipher.Encrypt(string(pemBytes))
	if err != nil {
		return err
	}
	return s.db.Model(&Cert{}).Where("id = ?", c.ID).Update("account_key_enc", enc).Error
}

func (s *Service) dnsCredentials(c *Cert) (map[string]string, error) {
	if c.CredsEnc == "" {
		return nil, fmt.Errorf("DNS 凭证未配置")
	}
	plain, err := s.cipher.Decrypt(c.CredsEnc)
	if err != nil {
		return nil, fmt.Errorf("DNS 凭证解密失败: %w", err)
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(plain), &m); err != nil {
		return nil, fmt.Errorf("DNS 凭证格式错误: %w", err)
	}
	return m, nil
}

func (s *Service) notifyFailure(c *Cert, err error) {
	if s.notifier == nil {
		return
	}
	ctx := context.Background()
	go func() {
		s.notifier.NotifyEvent(ctx, notify.SourceCertExpiring, notify.LevelWarn,
			fmt.Sprintf("cert-%d", c.ID), "证书签发/续期失败："+c.Name,
			fmt.Sprintf("证书：%s（%s）\n错误：%v", c.Name, c.Domains, err))
	}()
}

func (s *Service) audit(ctx context.Context, c *Cert, msg string) {
	if s.ssh != nil {
		s.ssh.RecordEvent(ctx, c.ServerID, "cert_deploy", msg)
	}
}

func splitDomains(s string) []string {
	var out []string
	for d := range strings.SplitSeq(s, ",") {
		if d = strings.TrimSpace(d); d != "" {
			out = append(out, d)
		}
	}
	return out
}

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
