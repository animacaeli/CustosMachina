// Package home 首页运维态势聚合（独立审核第 2 批 P1：登录后第一眼回答
// "现在是否健康、哪里需要处理"）。跨模块只读投影，单条 SQL 聚合，
// 不引模块依赖（与 bluegreen 读 projects 同款约定）。
package home

import (
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/pkg/httpx"
	"github.com/custos-machina/backend/internal/server"
)

type Handler struct{ db *gorm.DB }

func NewHandler(db *gorm.DB) *Handler { return &Handler{db: db} }

func (h *Handler) Name() string { return "home" }

func (h *Handler) RegisterRoutes(r server.Router) {
	r.Authed.GET("/home/summary", h.summary)
}

// Summary 运维态势（计数为全局聚合，不泄露凭据/内容明细）。
type Summary struct {
	Alerts struct {
		Open     int         `json:"open"`
		Critical int         `json:"critical"`
		Items    []AlertItem `json:"items"`
	} `json:"alerts"`
	Servers struct {
		Unreachable1h int `json:"unreachable1h"`
	} `json:"servers"`
	Releases struct {
		Failed7d int           `json:"failed7d"`
		Recent   []ReleaseItem `json:"recent"`
	} `json:"releases"`
	Jobs struct {
		Failed7d int `json:"failed7d"`
	} `json:"jobs"`
	Certs struct {
		Expiring14d int `json:"expiring14d"`
	} `json:"certs"`
}

type AlertItem struct {
	ID        uint      `json:"id"`
	Level     string    `json:"level"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"createdAt"`
}

type ReleaseItem struct {
	ID        uint      `json:"id"`
	Project   string    `json:"project"`
	EnvType   string    `json:"envType"`
	Tag       string    `json:"tag"`
	Status    string    `json:"status"`
	ReleaseBy string    `json:"releaseBy"`
	CreatedAt time.Time `json:"createdAt"`
}

func (h *Handler) summary(c *gin.Context) {
	now := time.Now()
	d7, d14, d1h := now.AddDate(0, 0, -7), now.Add(14*24*time.Hour), now.Add(-time.Hour)
	out := &Summary{}
	out.Alerts.Items = []AlertItem{}
	out.Releases.Recent = []ReleaseItem{}

	db := h.db.WithContext(c.Request.Context())
	db.Table("alert_events").Where("status = ?", "open").Count(new(int64)) // 预热无副作用
	var open, crit int64
	db.Table("alert_events").Where("status = ?", "open").Count(&open)
	db.Table("alert_events").Where("status = ? AND level = ?", "open", "critical").Count(&crit)
	out.Alerts.Open, out.Alerts.Critical = int(open), int(crit)
	db.Table("alert_events").Select("id, level, title, created_at").
		Where("status = ?", "open").
		Order("CASE level WHEN 'critical' THEN 0 WHEN 'warn' THEN 1 ELSE 2 END, id DESC").Limit(5).
		Scan(&out.Alerts.Items)

	var unreach int64
	db.Table("server_events").
		Where("type = ? AND created_at > ?", "unreachable", d1h).
		Distinct("server_id").Count(&unreach)
	out.Servers.Unreachable1h = int(unreach)

	var rf, jf int64
	db.Table("releases").Where("status IN ? AND created_at > ?", []string{"failed", "timeout"}, d7).Count(&rf)
	out.Releases.Failed7d = int(rf)
	db.Table("cron_runs").Where("status IN ? AND started_at > ?", []string{"failed", "timeout"}, d7).Count(&jf)
	out.Jobs.Failed7d = int(jf)

	var certs int64
	db.Table("certs").
		Where("status = ? AND expires_at IS NOT NULL AND expires_at < ?", "issued", d14).Count(&certs)
	out.Certs.Expiring14d = int(certs)

	db.Table("releases").Select("releases.id, projects.name AS project, releases.env_type, releases.tag, releases.status, releases.release_by, releases.created_at").
		Joins("LEFT JOIN projects ON projects.id = releases.project_id").
		Order("releases.id DESC").Limit(5).Scan(&out.Releases.Recent)

	httpx.OK(c, out)
}
