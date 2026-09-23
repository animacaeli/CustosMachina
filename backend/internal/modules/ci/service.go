package ci

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/modules/notify"
	"github.com/custos-machina/backend/internal/pkg/crypto"
	"github.com/custos-machina/backend/internal/pkg/logger"
)

var ErrNotFound = errors.New("CI 配置不存在")

// RegistryOut / GlobalConfigOut 对外视图。
type RegistryOut struct {
	Registry
	HasCredential bool `json:"hasCredential"`
}

func toOut(r Registry) RegistryOut {
	out := RegistryOut{Registry: r, HasCredential: r.Credential != ""}
	out.Credential = ""
	return out
}

type GlobalConfigOut struct {
	GiteaBaseURL  string `json:"giteaBaseUrl"`
	HasGiteaToken bool   `json:"hasGiteaToken"`
	WebhookSet    bool   `json:"webhookSet"`
	WebhookHint   string `json:"webhookHint"` // 给前端展示的回调地址模板
}

type Service struct {
	db     *gorm.DB
	cipher *crypto.Cipher
	notify *notify.Service
}

func NewService(db *gorm.DB, cipher *crypto.Cipher, ntfy *notify.Service) *Service {
	return &Service{db: db, cipher: cipher, notify: ntfy}
}

// ---- 全局配置 ----

type SaveGlobalInput struct {
	GiteaBaseURL  string `json:"giteaBaseUrl" binding:"omitempty,url,max=255"`
	GiteaToken    string `json:"giteaToken" binding:"omitempty,max=512"`    // 留空保留
	WebhookSecret string `json:"webhookSecret" binding:"omitempty,max=128"` // 留空保留
}

func (s *Service) GetGlobal(ctx context.Context) (*GlobalConfigOut, error) {
	var g GlobalConfig
	if err := s.db.WithContext(ctx).First(&g, 1).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &GlobalConfigOut{WebhookHint: "配置 base url 后生成"}, nil
		}
		return nil, err
	}
	return &GlobalConfigOut{
		GiteaBaseURL:  g.GiteaBaseURL,
		HasGiteaToken: g.GiteaToken != "",
		WebhookSet:    g.WebhookSecret != "",
		WebhookHint:   "在 gitea 仓库 Settings → Webhooks 添加：POST <平台地址>/api/ci/webhook/gitea（X-Gitea-Signature）",
	}, nil
}

func (s *Service) SaveGlobal(ctx context.Context, in SaveGlobalInput) (*GlobalConfigOut, error) {
	var g GlobalConfig
	s.db.WithContext(ctx).First(&g, 1) // 无则新建
	g.ID = 1
	if in.GiteaBaseURL != "" {
		g.GiteaBaseURL = strings.TrimRight(in.GiteaBaseURL, "/")
	}
	if in.GiteaToken != "" {
		enc, err := s.encrypt(in.GiteaToken)
		if err != nil {
			return nil, err
		}
		g.GiteaToken = enc
	}
	if in.WebhookSecret != "" {
		g.WebhookSecret = in.WebhookSecret
	}
	if err := s.db.WithContext(ctx).Save(&g).Error; err != nil {
		return nil, err
	}
	return s.GetGlobal(ctx)
}

func (s *Service) loadGlobal(ctx context.Context) (*GlobalConfig, error) {
	var g GlobalConfig
	if err := s.db.WithContext(ctx).First(&g, 1).Error; err != nil {
		return nil, errors.New("CI 全局配置未初始化")
	}
	return &g, nil
}

// projectToken 项目级 token（空 = 全局）。projects 表直读（同库，避免模块循环依赖）。
func (s *Service) projectToken(repoPath string) (string, error) {
	var row struct{ CIToken string }
	if err := s.db.Table("projects").Select("ci_token").Where("repo_path = ?", repoPath).First(&row).Error; err != nil {
		return "", nil // 项目可能未登记，回落全局
	}
	if row.CIToken == "" || s.cipher == nil {
		return "", nil
	}
	if dec, err := s.cipher.Decrypt(row.CIToken); err == nil {
		return dec, nil
	}
	return "", nil
}

func (s *Service) clientFor(ctx context.Context, repoPath string) (*giteaClient, error) {
	g, err := s.loadGlobal(ctx)
	if err != nil {
		return nil, err
	}
	token := ""
	if g.GiteaToken != "" && s.cipher != nil {
		if dec, err := s.cipher.Decrypt(g.GiteaToken); err == nil {
			token = dec
		}
	}
	if pt, _ := s.projectToken(repoPath); pt != "" {
		token = pt
	}
	return newGiteaClient(g.GiteaBaseURL, token), nil
}

// ---- Registry CRUD ----

type SaveRegistryInput struct {
	Name       string `json:"name" binding:"required,max=64"`
	Type       string `json:"type" binding:"required,oneof=aliyun tencent gitea"`
	Address    string `json:"address" binding:"required,max=255"`
	Credential string `json:"credential" binding:"omitempty,max=512"` // "username:password"；留空保留
	Remark     string `json:"remark" binding:"max=255"`
}

func (s *Service) ListRegistries(ctx context.Context) ([]RegistryOut, error) {
	var rs []Registry
	if err := s.db.WithContext(ctx).Order("id").Find(&rs).Error; err != nil {
		return nil, err
	}
	out := make([]RegistryOut, len(rs))
	for i, r := range rs {
		out[i] = toOut(r)
	}
	return out, nil
}

func (s *Service) SaveRegistry(ctx context.Context, id uint, in SaveRegistryInput) (*RegistryOut, error) {
	var r Registry
	if id > 0 {
		if err := s.db.WithContext(ctx).First(&r, id).Error; err != nil {
			return nil, ErrNotFound
		}
	}
	r.Name, r.Type, r.Address, r.Remark = in.Name, in.Type, in.Address, in.Remark
	if in.Credential != "" {
		enc, err := s.encrypt(in.Credential)
		if err != nil {
			return nil, err
		}
		r.Credential = enc
	}
	if err := s.db.WithContext(ctx).Save(&r).Error; err != nil {
		return nil, err
	}
	out := toOut(r)
	return &out, nil
}

func (s *Service) DeleteRegistry(ctx context.Context, id uint) error {
	res := s.db.WithContext(ctx).Delete(&Registry{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- webhook ----

// giteaTagPayload gitea tag push webhook（POST body）的感兴趣字段。
type giteaTagPayload struct {
	Ref   string `json:"ref"` // refs/tags/v1.0.0
	After string `json:"after"`
	Repo  struct {
		FullName string `json:"full_name"`
		HTMLURL  string `json:"html_url"`
	} `json:"repo"`
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
}

// VerifySignature X-Gitea-Signature = HMAC-SHA256(body, secret)。
func (s *Service) VerifySignature(ctx context.Context, body []byte, sigHex string) error {
	g, err := s.loadGlobal(ctx)
	if err != nil {
		return errors.New("CI 全局配置未初始化，拒绝 webhook")
	}
	if g.WebhookSecret == "" {
		return errors.New("webhook 密钥未配置，拒绝回调")
	}
	mac := hmac.New(sha256.New, []byte(g.WebhookSecret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(sigHex)) {
		return errors.New("webhook 签名校验失败")
	}
	return nil
}

// HandleTagPush 处理标签推送：匹配已登记项目则落构建记录。
func (s *Service) HandleTagPush(ctx context.Context, body []byte) (*Build, error) {
	var p giteaTagPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("webhook payload 解析失败: %w", err)
	}
	if !strings.HasPrefix(p.Ref, "refs/tags/") {
		return nil, nil // 非标签推送（push 分支事件 M5 槽位链路处理）
	}
	tag := strings.TrimPrefix(p.Ref, "refs/tags/")

	// 项目匹配：repo full_name（大小写不敏感）
	var proj struct {
		ID                  uint
		NotifyOnSuccess     bool
		NotifyProdGroupID   *uint
		NotifyCanaryGroupID *uint
		NotifyTestGroupID   *uint
	}
	if err := s.db.Table("projects").
		Where("lower(repo_path) = ?", strings.ToLower(p.Repo.FullName)).
		First(&proj).Error; err != nil {
		return nil, nil // 非平台登记的项目，忽略
	}

	// 标签规则 → 环境：v* = 正式；canary-* = 灰度（格式校验 canary-yyyymmdd-缩写）
	env := ""
	switch {
	case strings.HasPrefix(tag, "v"):
		env = "prod"
	case strings.HasPrefix(tag, "canary-"):
		if !validCanaryTag(tag) {
			return nil, fmt.Errorf("灰度标签 %q 不符合 canary-yyyymmdd-姓名缩写 规则", tag)
		}
		env = "canary"
	default:
		return nil, nil // 不识别的标签不落记录
	}

	b := Build{
		ProjectID: proj.ID, EnvType: env, Tag: tag, SHA: p.After,
		Builder: p.Sender.Login, Source: SourceTag, Status: BuildPending,
	}
	if err := s.db.WithContext(ctx).Create(&b).Error; err != nil {
		return nil, err
	}
	return &b, nil
}

// validCanaryTag canary-yyyymmdd-[姓名首字母小写]。
func validCanaryTag(tag string) bool {
	parts := strings.SplitN(tag, "-", 3)
	if len(parts) != 3 || parts[0] != "canary" {
		return false
	}
	if len(parts[1]) != 8 {
		return false
	}
	for _, ch := range parts[1] {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return parts[2] != ""
}

// ---- 构建 ----

type BuildQuery struct {
	ProjectID  uint
	EnvType    string
	Page, Size int
}

func (s *Service) ListBuilds(ctx context.Context, q BuildQuery) ([]Build, int64, error) {
	tx := s.db.WithContext(ctx).Model(&Build{})
	if q.ProjectID > 0 {
		tx = tx.Where("project_id = ?", q.ProjectID)
	}
	if q.EnvType != "" {
		tx = tx.Where("env_type = ?", q.EnvType)
	}
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if q.Page <= 0 {
		q.Page = 1
	}
	if q.Size <= 0 || q.Size > 100 {
		q.Size = 20
	}
	var list []Build
	if err := tx.Order("id DESC").Offset((q.Page - 1) * q.Size).Limit(q.Size).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ---- 状态轮询（jobs 常驻任务） ----

// PollPending 更新所有未到终态的构建状态；终态时按项目配置推通知。
func (s *Service) PollPending(ctx context.Context) error {
	var pendings []Build
	if err := s.db.WithContext(ctx).
		Where("status IN ?", []string{BuildPending, BuildRunning}).
		Find(&pendings).Error; err != nil {
		return err
	}
	for _, b := range pendings {
		if b.SHA == "" {
			continue
		}
		var proj struct {
			RepoPath            string
			NotifyOnSuccess     bool
			NotifyProdGroupID   *uint
			NotifyCanaryGroupID *uint
			NotifyTestGroupID   *uint
		}
		if err := s.db.Table("projects").Select("repo_path, notify_on_success, notify_prod_group_id, notify_canary_group_id, notify_test_group_id").
			Where("id = ?", b.ProjectID).First(&proj).Error; err != nil {
			continue
		}
		client, err := s.clientFor(ctx, proj.RepoPath)
		if err != nil {
			logger.Warnf("[ci] 构建 %d 无法建 gitea 客户端: %v", b.ID, err)
			continue
		}
		st, err := client.commitStatus(ctx, proj.RepoPath, b.SHA)
		if err != nil {
			logger.Warnf("[ci] 构建 %d 状态轮询失败: %v", b.ID, err)
			continue
		}
		newStatus := mapStatus(st.State)
		if newStatus == BuildRunning && b.Status == BuildPending {
			newStatus = BuildRunning
		}
		if newStatus == b.Status {
			continue
		}
		updates := map[string]any{"status": newStatus}
		if b.LogURL == "" {
			updates["log_url"] = client.actionsURL(proj.RepoPath)
		}
		if err := s.db.WithContext(ctx).Model(&Build{}).Where("id = ?", b.ID).Updates(updates).Error; err != nil {
			continue
		}
		if newStatus == BuildSuccess || newStatus == BuildFailed {
			s.notifyBuild(ctx, b, proj, newStatus)
		}
	}
	return nil
}

func (s *Service) notifyBuild(ctx context.Context, b Build, proj struct {
	RepoPath            string
	NotifyOnSuccess     bool
	NotifyProdGroupID   *uint
	NotifyCanaryGroupID *uint
	NotifyTestGroupID   *uint
}, status string) {
	if status == BuildSuccess && !proj.NotifyOnSuccess {
		return
	}
	var groupID *uint
	switch b.EnvType {
	case "prod":
		groupID = proj.NotifyProdGroupID
	case "canary":
		groupID = proj.NotifyCanaryGroupID
	case "test":
		groupID = proj.NotifyTestGroupID
	}
	if groupID == nil {
		return
	}
	g, err := s.notify.Get(ctx, *groupID)
	if err != nil {
		return
	}
	verb := "成功"
	if status == BuildFailed {
		verb = "失败"
	}
	title := fmt.Sprintf("构建%s：%s %s", verb, proj.RepoPath, b.Tag)
	content := fmt.Sprintf("环境：%s\n构建人：%s\n[查看日志](%s)", envLabel(b.EnvType), b.Builder, b.LogURL)
	go func() {
		if err := s.notify.Send(context.WithoutCancel(ctx), g, title, content); err != nil {
			logger.Warnf("[ci] 构建通知发送失败: %v", err)
		}
	}()
}

func envLabel(env string) string {
	switch env {
	case "prod":
		return "正式"
	case "canary":
		return "灰度"
	case "test":
		return "测试"
	}
	return env
}

// Branches 供前端表单（M5 槽位占用选分支）。
func (s *Service) Branches(ctx context.Context, projectID uint) ([]string, error) {
	var repoPath string
	if err := s.db.Table("projects").Select("repo_path").Where("id = ?", projectID).First(&repoPath).Error; err != nil {
		return nil, ErrNotFound
	}
	client, err := s.clientFor(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	return client.branches(ctx, repoPath)
}

func (s *Service) encrypt(v string) (string, error) {
	if s.cipher == nil {
		return "", errors.New("平台主密钥未配置，无法加密")
	}
	return s.cipher.Encrypt(v)
}
