// events.go 告警事件留痕（v0.12.0 审计产品项：告警→诊断→通知主线曾在
// 「事件不落库」处断开——用户无法回溯发生过什么、无法确认/关闭，只能翻 IM）。
// 最小可用版：webhook 收到即落一条 alert_events；历史页列表 + 手动标记已处理。
package observ

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/pkg/logger"
)

// AlertEvent 告警事件（不可变留痕；处理状态可流转）。
type AlertEvent struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	Source    string    `gorm:"size:32;index;not null" json:"source"` // o2_alert | business | cert_expiring | platform_ops
	Level     string    `gorm:"size:16;not null" json:"level"`        // info | warn | critical
	Title     string    `gorm:"size:255;not null" json:"title"`
	Detail    string    `gorm:"type:text" json:"detail"`
	DedupKey  string    `gorm:"size:128;index" json:"dedupKey"`
	Status    string    `gorm:"size:16;index;not null" json:"status"` // open | handled
	HandledBy string    `gorm:"size:64" json:"handledBy"`
	CreatedAt time.Time `gorm:"index" json:"createdAt"`
}

func (AlertEvent) TableName() string { return "alert_events" }

const (
	EventStatusOpen    = "open"
	EventStatusHandled = "handled"
)

// recordAlertEvent 落一条事件（失败只打日志——留痕不阻塞通知主链路）。
func (s *Service) recordAlertEvent(ctx context.Context, source, level, title, detail, dedupKey string) {
	ev := AlertEvent{
		Source: source, Level: level, Title: title, Detail: detail,
		DedupKey: dedupKey, Status: EventStatusOpen,
	}
	if err := s.db.WithContext(ctx).Create(&ev).Error; err != nil {
		logger.Errorf("[observ] 告警事件落库失败 source=%s title=%s: %v", source, title, err)
	}
}

// AlertEventPage 事件分页。
type AlertEventPage struct {
	Items []AlertEvent `json:"items"`
	Total int64        `json:"total"`
}

// ListAlertEvents 告警历史（时间倒序；level/status 过滤；分页钳制 100）。
func (s *Service) ListAlertEvents(ctx context.Context, level, status string, page, size int) (*AlertEventPage, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	q := s.db.WithContext(ctx).Model(&AlertEvent{})
	if level != "" {
		q = q.Where("level = ?", level)
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}
	var items []AlertEvent
	if err := q.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&items).Error; err != nil {
		return nil, err
	}
	return &AlertEventPage{Items: items, Total: total}, nil
}

// HandleAlertEvent 标记已处理（幂等；记录处理人）。
func (s *Service) HandleAlertEvent(ctx context.Context, id uint, by string) error {
	res := s.db.WithContext(ctx).Model(&AlertEvent{}).
		Where("id = ? AND status = ?", id, EventStatusOpen).
		Updates(map[string]any{"status": EventStatusHandled, "handled_by": by})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
