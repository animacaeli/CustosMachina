package projects

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm/clause"

	"github.com/custos-machina/backend/internal/modules/auth"
	"github.com/custos-machina/backend/internal/modules/rbac"
	"github.com/custos-machina/backend/internal/pkg/httpx"
)

type Follow struct {
	UserID    uint `gorm:"primaryKey;autoIncrement:false"`
	ProjectID uint `gorm:"primaryKey;autoIncrement:false;index"`
}

func (Follow) TableName() string { return "project_follows" }

func (s *Service) followedIDs(ctx context.Context, userID uint) ([]uint, error) {
	var ids []uint
	err := s.db.WithContext(ctx).Model(&Follow{}).Where("user_id = ?", userID).Pluck("project_id", &ids).Error
	return ids, err
}

func (h *Handler) follow(c *gin.Context) {
	var in struct {
		ProjectID uint `json:"projectId" binding:"required"`
		Following bool `json:"following"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if !rbac.CanViewProject(c, in.ProjectID) {
		httpx.Fail(c, http.StatusForbidden, 403, "无该项目的查看权限")
		return
	}
	db := h.svc.db.WithContext(c.Request.Context())
	var project Project
	if err := db.Select("id").First(&project, in.ProjectID).Error; err != nil {
		httpx.FailNotFound(c, ErrNotFound.Error())
		return
	}
	follow := Follow{UserID: auth.ClaimsFromContext(c).UserID, ProjectID: in.ProjectID}
	var err error
	if in.Following {
		err = db.Clauses(clause.OnConflict{DoNothing: true}).Create(&follow).Error
	} else {
		err = db.Delete(&follow).Error
	}
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, gin.H{"following": in.Following})
}
