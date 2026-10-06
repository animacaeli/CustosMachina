package notify

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/modules/identity"
	"github.com/custos-machina/backend/internal/pkg/crypto"
	"github.com/custos-machina/backend/internal/pkg/logger"
	"github.com/custos-machina/backend/internal/pkg/strx"
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

// Service 通知中心：群 CRUD + 路由规则 + 事件投递（聚合/静默）+ 发送留痕。
type Service struct {
	db       *gorm.DB
	cipher   *crypto.Cipher
	sender   *sender
	agg      *aggregator
	analyzer AlertAnalyzer // P7-M2 告警 AI 分析（nil = 关闭）

	stopSweeper func()
}

func NewService(db *gorm.DB, cipher *crypto.Cipher) *Service {
	s := &Service{db: db, cipher: cipher, sender: newSender(), agg: newAggregator()}
	s.stopSweeper = s.startAggSweeper()
	return s
}

// NewServiceWithCleanup wire 装配专用：返回清扫停止函数，让 wire_gen
// 完整可再生成（hand-maintained 的手工 cleanup 行是 wire 门禁的障碍）。
func NewServiceWithCleanup(db *gorm.DB, cipher *crypto.Cipher) (*Service, func(), error) {
	s := NewService(db, cipher)
	return s, s.Stop, nil
}

// Stop 停止聚合清扫 goroutine（进程关停时由 cleanup 链调用）。
func (s *Service) Stop() {
	if s.stopSweeper != nil {
		s.stopSweeper()
	}
}

// SendRecordPage 投递记录分页（排障用：为什么企微没收到通知）。
type SendRecordPage struct {
	Items []SendRecord `json:"items"`
	Total int64        `json:"total"`
}

// ListSendRecords 通知投递记录（时间倒序；groupId 过滤；分页钳制 100）。
func (s *Service) ListSendRecords(ctx context.Context, groupID uint, page, size int) (*SendRecordPage, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	q := s.db.WithContext(ctx).Model(&SendRecord{})
	if groupID > 0 {
		q = q.Where("group_id = ?", groupID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}
	var items []SendRecord
	if err := q.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&items).Error; err != nil {
		return nil, err
	}
	return &SendRecordPage{Items: items, Total: total}, nil
}

// ---- 群管理 ----

type SaveGroupInput struct {
	Name    string `json:"name" binding:"required,max=64"`
	Scope   string `json:"scope" binding:"required,oneof=prod dev"`
	Channel string `json:"channel" binding:"omitempty,oneof=webhook smtp"`
	Webhook string `json:"webhook" binding:"omitempty,max=1024"`
	Target  string `json:"target" binding:"omitempty,max=512"`
	Remark  string `json:"remark" binding:"max=255"`
}

// validateGroupTarget 渠道目标校验（webhook 渠道沿用 webhook 格式校验）。
func validateGroupTarget(channel, target string) error {
	switch channel {
	case ChannelSMTP:
		if target == "" || !strings.Contains(target, "@") {
			return fmt.Errorf("SMTP 渠道须填写收件人邮箱（多个逗号分隔）")
		}
	}
	return nil
}

// validScope 用途显式选择（2026-09-24 审核：取消名称前缀约束，登记时直接选用途）。
func validScope(scope string) bool {
	return scope == ScopeProd || scope == ScopeDev
}

func (s *Service) Create(ctx context.Context, in SaveGroupInput) (*GroupOut, error) {
	if !validScope(in.Scope) {
		return nil, fmt.Errorf("用途须为 prod 或 dev")
	}
	if in.Channel == "" {
		in.Channel = ChannelWebhook
	}
	if err := validateGroupTarget(in.Channel, in.Target); err != nil {
		return nil, err
	}
	g := Group{Name: in.Name, Scope: in.Scope, Channel: in.Channel, Target: in.Target, Remark: in.Remark}
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
	if in.Channel == "" {
		in.Channel = ChannelWebhook
	}
	if err := validateGroupTarget(in.Channel, in.Target); err != nil {
		return nil, err
	}
	g.Name, g.Scope, g.Remark = in.Name, in.Scope, in.Remark
	g.Channel, g.Target = in.Channel, in.Target
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

// Send 推送消息到指定群（按渠道分派：webhook/telegram/smtp）并留痕。
// group 为 nil 安全（返回 nil 不发送）。
func (s *Service) Send(ctx context.Context, group *Group, title, content string) error {
	if group == nil {
		return nil
	}
	if group.Channel == ChannelSMTP {
		err := s.sendByChannel(ctx, group, title, content)
		s.record(ctx, group.ID, title, content, okOr(err), errString(err))
		return err
	}
	webhook := group.Webhook
	if webhook != "" && s.cipher != nil {
		if dec, err := s.cipher.Decrypt(webhook, aadGroupWebhook); err == nil {
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
	return identity.UpsertSetting(s.db, ctx, SettingOpsGroup, strconv.FormatUint(uint64(id), 10))
}

func (s *Service) setting(ctx context.Context, key string) (string, bool, error) {
	var row struct {
		Key   string `gorm:"column:skey"`
		Value string
	}
	err := s.db.WithContext(ctx).Table("platform_settings").
		Select("skey", "value").Where("skey = ?", key).Order("skey").First(&row).Error
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
	return s.cipher.Encrypt(webhook, aadGroupWebhook)
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
	return strx.Truncate(s, n) // rune 安全（防切碎中文，v0.12.0 审计）
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
