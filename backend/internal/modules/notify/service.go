package notify

import (
	"context"
	"errors"
	"fmt"
	"strconv"

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
	sender *sender
}

func NewService(db *gorm.DB, cipher *crypto.Cipher) *Service {
	return &Service{db: db, cipher: cipher, sender: newSender()}
}

// ---- 群管理 ----

type SaveGroupInput struct {
	Name    string `json:"name" binding:"required,max=64"`
	Scope   string `json:"scope" binding:"required,oneof=prod dev"`
	Webhook string `json:"webhook" binding:"omitempty,max=1024"`
	Remark  string `json:"remark" binding:"max=255"`
}

// validScope 用途显式选择（2026-09-24 审核：取消名称前缀约束，登记时直接选用途）。
func validScope(scope string) bool {
	return scope == ScopeProd || scope == ScopeDev
}

func (s *Service) Create(ctx context.Context, in SaveGroupInput) (*GroupOut, error) {
	if !validScope(in.Scope) {
		return nil, fmt.Errorf("用途须为 prod 或 dev")
	}
	g := Group{Name: in.Name, Scope: in.Scope, Remark: in.Remark}
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
	g.Name, g.Scope, g.Remark = in.Name, in.Scope, in.Remark
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
	// 硬删：软删行占住 name 唯一索引
	res := s.db.WithContext(ctx).Unscoped().Delete(&Group{}, id)
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
	err := s.sender.Send(ctx, group.ID, webhook, title, content)
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
	switch detectProvider(webhook) {
	case provWecom, provDingtalk, provFeishu:
	default:
		return "", errors.New("webhook 须为企微 / 钉钉 / 飞书群机器人地址")
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
