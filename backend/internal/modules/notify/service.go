package notify

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/pkg/crypto"
	"github.com/custos-machina/backend/internal/pkg/logger"
)

// ErrNotFound 统一的未找到错误。
var ErrNotFound = errors.New("通知群不存在")

// GroupOut 对外视图：不含 webhook 明文/密文。
type GroupOut struct {
	Group
	HasWebhook bool `json:"hasWebhook"`
}

func toOut(g Group) GroupOut {
	out := GroupOut{Group: g, HasWebhook: g.Webhook != ""}
	out.Webhook = ""
	return out
}

// Service 通知中心：群 CRUD + 消息发送 + 发送留痕。
type Service struct {
	db     *gorm.DB
	cipher *crypto.Cipher
	sender *wecomSender
}

func NewService(db *gorm.DB, cipher *crypto.Cipher) *Service {
	return &Service{db: db, cipher: cipher, sender: newWecomSender()}
}

// ---- 群管理 ----

type SaveGroupInput struct {
	Name    string `json:"name" binding:"required,max=64"`
	Webhook string `json:"webhook" binding:"omitempty,max=1024"`
	Remark  string `json:"remark" binding:"max=255"`
}

// scopeOfName 名称前缀推导用途；不合规直接报错（前缀是群登记的硬约束）。
func scopeOfName(name string) (string, error) {
	switch {
	case strings.HasPrefix(name, "【P】"):
		return ScopeProd, nil
	case strings.HasPrefix(name, "【dev】"):
		return ScopeDev, nil
	default:
		return "", fmt.Errorf("群名称必须以【P】（生产类）或【dev】（测试类）开头")
	}
}

func (s *Service) Create(ctx context.Context, in SaveGroupInput) (*GroupOut, error) {
	scope, err := scopeOfName(in.Name)
	if err != nil {
		return nil, err
	}
	g := Group{Name: in.Name, Scope: scope, Remark: in.Remark}
	if in.Webhook != "" {
		enc, err := s.encryptWebhook(in.Webhook)
		if err != nil {
			return nil, err
		}
		g.Webhook = enc
	}
	if err := s.db.WithContext(ctx).Create(&g).Error; err != nil {
		return nil, err
	}
	out := toOut(g)
	return &out, nil
}

func (s *Service) Update(ctx context.Context, id uint, in SaveGroupInput) (*GroupOut, error) {
	var g Group
	if err := s.db.WithContext(ctx).First(&g, id).Error; err != nil {
		return nil, ErrNotFound
	}
	scope, err := scopeOfName(in.Name)
	if err != nil {
		return nil, err
	}
	g.Name, g.Scope, g.Remark = in.Name, scope, in.Remark
	if in.Webhook != "" { // 留空保留原 webhook（与服务器凭据同语义）
		enc, err := s.encryptWebhook(in.Webhook)
		if err != nil {
			return nil, err
		}
		g.Webhook = enc
	}
	if err := s.db.WithContext(ctx).Save(&g).Error; err != nil {
		return nil, err
	}
	out := toOut(g)
	return &out, nil
}

func (s *Service) Delete(ctx context.Context, id uint) error {
	res := s.db.WithContext(ctx).Delete(&Group{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) List(ctx context.Context) ([]GroupOut, error) {
	var gs []Group
	if err := s.db.WithContext(ctx).Order("id").Find(&gs).Error; err != nil {
		return nil, err
	}
	out := make([]GroupOut, len(gs))
	for i, g := range gs {
		out[i] = toOut(g)
	}
	return out, nil
}

func (s *Service) Get(ctx context.Context, id uint) (*Group, error) {
	var g Group
	if err := s.db.WithContext(ctx).First(&g, id).Error; err != nil {
		return nil, ErrNotFound
	}
	return &g, nil
}

// ---- 发送 ----

// Send 推送 markdown 消息到指定群并留痕。group 为nil 安全（返回 nil 不发送）。
func (s *Service) Send(ctx context.Context, group *Group, title, content string) error {
	if group == nil {
		return nil
	}
	webhook := group.Webhook
	if webhook != "" && s.cipher != nil {
		if dec, err := s.cipher.Decrypt(webhook); err == nil {
			webhook = dec
		} else {
			s.record(ctx, group.ID, title, content, "failed", "webhook 解密失败")
			return err
		}
	}
	if webhook == "" {
		s.record(ctx, group.ID, title, content, "failed", "群未配置 webhook")
		return fmt.Errorf("群 %q 未配置 webhook", group.Name)
	}
	text := "**" + title + "**\n" + content
	if len(text) > 4000 {
		text = text[:4000]
	}
	err := s.sender.sendMarkdown(ctx, group.ID, webhook, text)
	s.record(ctx, group.ID, title, content, okOr(err), errString(err))
	return err
}

// NotifyOps 平台级运维告警（服务器不可达等）：发到平台设置的运维群。
// 实现 resources.OpsNotifier 接口（第二阶段空壳桥接点）。
func (s *Service) NotifyOps(ctx context.Context, title, detail string) {
	idStr, ok, err := s.setting(ctx, SettingOpsGroup)
	if err != nil || !ok {
		logger.Warnf("[notify] 运维群未配置，丢弃告警: %s", title)
		return
	}
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		logger.Warnf("[notify] 运维群配置无效 %q: %v", idStr, err)
		return
	}
	g, err := s.Get(ctx, uint(id))
	if err != nil {
		logger.Warnf("[notify] 运维群不存在 id=%s", idStr)
		return
	}
	if err := s.Send(ctx, g, title, detail); err != nil {
		logger.Warnf("[notify] 运维告警发送失败: %v", err)
	}
}

// OpsGroupID / SetOpsGroup 平台运维群设置（管理后台用）。
func (s *Service) OpsGroupID(ctx context.Context) (uint, bool) {
	v, ok, err := s.setting(ctx, SettingOpsGroup)
	if err != nil || !ok {
		return 0, false
	}
	id, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0, false
	}
	return uint(id), true
}

func (s *Service) SetOpsGroup(ctx context.Context, id uint) error {
	return s.db.WithContext(ctx).Exec(
		`INSERT INTO platform_settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		SettingOpsGroup, strconv.FormatUint(uint64(id), 10)).Error
}

func (s *Service) setting(ctx context.Context, key string) (string, bool, error) {
	var row struct{ Key, Value string }
	err := s.db.WithContext(ctx).Table("platform_settings").
		Select("key", "value").Where("key = ?", key).First(&row).Error
	if err != nil {
		return "", false, nil
	}
	return row.Value, true, nil
}

func (s *Service) encryptWebhook(webhook string) (string, error) {
	if s.cipher == nil {
		return "", errors.New("平台主密钥未配置，无法加密 webhook")
	}
	if !strings.HasPrefix(webhook, "https://qyapi.weixin.qq.com/cgi-bin/webhook/send") {
		return "", errors.New("webhook 必须是企微群机器人地址（qyapi.weixin.qq.com/cgi-bin/webhook/send...）")
	}
	return s.cipher.Encrypt(webhook)
}

func (s *Service) record(ctx context.Context, groupID uint, title, content, status, errMsg string) {
	rec := SendRecord{
		GroupID: groupID, Title: truncate(title, 128), Content: content,
		Status: status, Error: truncate(errMsg, 512),
	}
	if err := s.db.WithContext(ctx).Create(&rec).Error; err != nil {
		logger.Warnf("[notify] 落发送记录失败: %v", err)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func okOr(err error) string {
	if err != nil {
		return "failed"
	}
	return "ok"
}

func errString(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}
