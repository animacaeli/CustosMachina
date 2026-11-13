package notify

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/custos-machina/backend/internal/modules/auth"
	"github.com/custos-machina/backend/internal/modules/identity"
	"github.com/custos-machina/backend/internal/modules/projects"
	"github.com/custos-machina/backend/internal/modules/rbac"
	"github.com/custos-machina/backend/internal/pkg/httpx"
	"github.com/custos-machina/backend/internal/pkg/logger"
)

type InboxItem struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	UserID    uint      `gorm:"not null;uniqueIndex:idx_inbox_delivery,priority:1;index:idx_inbox_user_read" json:"-"`
	ProjectID uint      `gorm:"not null;uniqueIndex:idx_inbox_delivery,priority:2" json:"projectId"`
	EventKey  string    `gorm:"size:128;not null;uniqueIndex:idx_inbox_delivery,priority:3" json:"-"`
	Title     string    `gorm:"size:255;not null" json:"title"`
	Message   string    `gorm:"size:512" json:"message"`
	IsRead    bool      `gorm:"not null;default:false;index:idx_inbox_user_read" json:"isRead"`
	Hidden    bool      `gorm:"not null;default:false" json:"-"`
	CreatedAt time.Time `json:"createdAt"`
}

func (InboxItem) TableName() string { return "notify_inbox" }

type ProjectEvent struct {
	PlatformOnMissingProject bool
	ProjectID                uint
	Key                      string
	Title                    string
	Message                  string
}

type ProjectEventRecorder interface {
	RecordProjectEvent(context.Context, ProjectEvent)
}

func (s *Service) RecordProjectEvent(ctx context.Context, event ProjectEvent) {
	if event.Key == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.recordInbox(ctx, event); err != nil {
		logger.Warnf("[notify] 站内通知写入失败 project=%d event=%s: %v", event.ProjectID, event.Key, err)
	}
}

func (s *Service) recordInbox(ctx context.Context, event ProjectEvent) error {
	db := s.db.WithContext(ctx)
	q := db.Where("status = ?", identity.StatusActive)
	admins := "is_local_admin = ? OR roles LIKE ?"
	var project projects.Project
	if event.ProjectID != 0 {
		err := db.Select("id", "creator_id").First(&project, event.ProjectID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) && event.PlatformOnMissingProject {
			event.ProjectID = 0
		} else if err != nil {
			return err
		}
	}
	creatorID := project.CreatorID
	followers := db.Model(&projects.Follow{}).Select("user_id").Where("project_id = ?", event.ProjectID)
	if event.ProjectID == 0 {
		q = q.Where(admins, true, "%admin%")
	} else {
		q = q.Where(admins+" OR id = ? OR id IN (?)", true, "%admin%", creatorID, followers)
	}
	var users []identity.User
	if err := q.Find(&users).Error; err != nil {
		return err
	}
	followed := map[uint]bool{}
	if event.ProjectID != 0 {
		var ids []uint
		if err := followers.Pluck("user_id", &ids).Error; err != nil {
			return err
		}
		for _, id := range ids {
			followed[id] = true
		}
	}
	items := make([]InboxItem, 0, len(users))
	for _, u := range users {
		roles := identity.ParseRoleList(u.Roles)
		admin := u.IsLocalAdmin || slices.Contains(roles, "admin") || slices.Contains(roles, "superadmin")
		if !admin && (event.ProjectID == 0 || (creatorID != u.ID && !followed[u.ID])) {
			continue
		}
		if event.ProjectID != 0 {
			if all, ids := rbac.ProjectVisibility(u); !all && !slices.Contains(ids, event.ProjectID) {
				continue
			}
		}
		items = append(items, InboxItem{UserID: u.ID, ProjectID: event.ProjectID, EventKey: event.Key,
			Title: limitText(event.Title, 255), Message: limitText(event.Message, 512)})
	}
	if len(items) == 0 {
		return nil
	}
	return db.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(items, 100).Error
}

func limitText(text string, limit int) string {
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit-1]) + "…"
	}
	return text
}

func (h *Handler) inbox(c *gin.Context) {
	claims := auth.ClaimsFromContext(c)
	db := h.svc.db.WithContext(c.Request.Context())
	var user identity.User
	if err := db.First(&user, claims.UserID).Error; err != nil {
		httpx.FailServer(c, err)
		return
	}
	base := db.Model(&InboxItem{}).Where("user_id = ? AND hidden = ?", user.ID, false)
	all, ids := rbac.ProjectVisibility(user)
	visible := db.Model(&projects.Project{}).Select("id")
	if !all {
		visible = visible.Where("id IN ?", ids)
	}
	roles := identity.ParseRoleList(user.Roles)
	if user.IsLocalAdmin || slices.Contains(roles, "admin") || slices.Contains(roles, "superadmin") {
		base = base.Where("project_id = 0 OR project_id IN (?)", visible)
	} else {
		base = base.Where("project_id IN (?)", visible)
	}
	if c.Request.Method == http.MethodGet {
		before, _ := strconv.ParseUint(c.Query("before"), 10, 64)
		page := base.Session(&gorm.Session{})
		if before != 0 {
			page = page.Where("id < ?", before)
		}
		items := make([]InboxItem, 0, 51)
		if err := page.Order("id DESC").Limit(51).Find(&items).Error; err != nil {
			httpx.FailServer(c, err)
			return
		}
		var unread int64
		if err := base.Where("is_read = ?", false).Count(&unread).Error; err != nil {
			httpx.FailServer(c, err)
			return
		}
		var next uint
		if len(items) > 50 {
			items = items[:50]
			next = items[49].ID
		}
		httpx.OK(c, gin.H{"items": items, "unread": unread, "next": next})
		return
	}
	var in struct {
		ID     uint   `json:"id"`
		Action string `json:"action" binding:"required,oneof=read remove read-all clear"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	target := base.Session(&gorm.Session{})
	if in.Action == "read" || in.Action == "remove" {
		if in.ID == 0 {
			httpx.FailBadRequest(c, "请选择通知")
			return
		}
		target = target.Where("id = ?", in.ID)
	}
	field := "is_read"
	if in.Action == "remove" || in.Action == "clear" {
		field = "hidden"
	}
	if err := target.UpdateColumn(field, true).Error; err != nil {
		httpx.FailServer(c, err)
		return
	}
	var unread int64
	if err := base.Where("is_read = ?", false).Count(&unread).Error; err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, gin.H{"ok": true, "unread": unread})
}
