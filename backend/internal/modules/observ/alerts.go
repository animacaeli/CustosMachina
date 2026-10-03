package observ

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/modules/notify"
	"github.com/custos-machina/backend/internal/pkg/crypto"
	"github.com/custos-machina/backend/internal/pkg/logger"
	"github.com/custos-machina/backend/internal/pkg/strx"
)

// O2 观测告警闭环（P5 M3，docs/research-o2-alerts.md）：
// 平台模型自持（observ_alerts 表），O2 是投影——CRUD 后同步 O2 稳定版 v1 API；
// destination/template 由平台自动维护，告警触发经 webhook 回流 NotifyEvent。

const (
	settingO2Email    = "observ.o2_email"    // 认证用户（root）
	settingO2Password = "observ.o2_password" // AES 加密
	settingO2Org      = "observ.o2_org"      // 组织名，默认 "default"
	settingO2Token    = "observ.o2_token"    // webhook 共享密钥（AES 加密）

	platformDestination = "custos-platform" // 平台专用 O2 destination 名
	platformTemplate    = "custos-platform-tpl"
)

// platformTemplateBody 平台模板体：全部值加引号——变量未被 O2 渲染、
// 留下 "{rows_count}" 占位符时整体仍是合法 JSON（真机 v0.91 教训），
// webhook 侧 flexInt 与原文兜底负责消化其余形态。
const platformTemplateBody = `{"alert_name":"{alert_name}","stream_name":"{stream_name}","org_name":"{org_name}","alert_type":"{alert_type}","trigger_time":"{trigger_time}","rows_count":"{rows_count}"}`

const (
	// 同步状态
	SyncPending = "pending"
	SyncOK      = "synced"
	SyncFailed  = "failed"
)

// Alert 平台侧告警定义（O2 投影的真相源）。
type Alert struct {
	ID uint `gorm:"primarykey" json:"id"`
	// 定义（与 O2 稳定版字段对齐）
	Name        string `gorm:"size:128;uniqueIndex;not null" json:"name"`
	StreamName  string `gorm:"size:128;not null" json:"streamName"`
	StreamType  string `gorm:"size:16;not null;default:logs" json:"streamType"` // logs | metrics | traces
	SQL         string `gorm:"type:text" json:"sql"`                            // 查询（sql 型告警）
	Period      int    `gorm:"not null;default:10" json:"period"`               // 窗口分钟
	Operator    string `gorm:"size:8;not null;default=>=" json:"operator"`
	Threshold   int    `gorm:"not null;default=1" json:"threshold"`
	Frequency   int    `gorm:"not null;default=1" json:"frequency"` // 分钟
	Silence     int    `gorm:"not null;default=10" json:"silence"`  // 触发后静默分钟
	Enabled     bool   `gorm:"not null;default:true" json:"enabled"`
	Description string `gorm:"size:255" json:"description"`
	// 平台侧扩展
	Level string `gorm:"size:8;not null;default:warn" json:"level"` // info|warn|critical（路由用）
	// R1 模板化：项目侧策略归属与来源模板（paramsSnapshot 记录实例化参数——
	// 模板后续修改不漂移，重新填充才更新）
	ProjectID      uint   `gorm:"index;not null;default:0" json:"projectId"` // 0 = 平台级（管理后台直建）
	TemplateID     uint   `gorm:"index;not null;default:0" json:"templateId"`
	ParamsSnapshot string `gorm:"type:text" json:"paramsSnapshot"`
	// 同步状态
	SyncStatus string    `gorm:"size:16;not null;default:pending" json:"syncStatus"`
	SyncError  string    `gorm:"size:512" json:"syncError"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

func (Alert) TableName() string { return "observ_alerts" }

// ---- O2 连接 ----

type O2Config struct {
	BaseURL  string
	Org      string
	Email    string
	Password string
	Token    string // webhook 共享密钥
}

// o2Config 读配置；BaseURL/Email/Password 齐备才可用。
func (s *Service) o2Config(ctx context.Context) (*O2Config, bool) {
	base := s.O2URL(ctx)
	email, _ := s.setting(ctx, settingO2Email)
	encPass, _ := s.setting(ctx, settingO2Password)
	org, _ := s.setting(ctx, settingO2Org)
	if org == "" {
		org = "default"
	}
	// o2_url 存的是完整采集地址（vector 渲染用，可含 userinfo/路径/?ndjson）；
	// API 调用只取 origin——否则路径拼进 API URL 会把告警创建变成日志写入
	if u, perr := url.Parse(base); perr == nil && u.Host != "" {
		base = u.Scheme + "://" + u.Host
	}
	cfg := &O2Config{BaseURL: base, Org: org, Email: email}
	if cfg.BaseURL == "" || email == "" || encPass == "" {
		return cfg, false
	}
	if s.cipher != nil {
		if p, err := s.cipher.Decrypt(encPass); err == nil {
			cfg.Password = p
		}
	}
	if t, _ := s.setting(ctx, settingO2Token); t != "" && s.cipher != nil {
		if p, err := s.cipher.Decrypt(t); err == nil {
			cfg.Token = p
		}
	}
	return cfg, cfg.Password != ""
}

type O2SettingsInput struct {
	Email    string `json:"email" binding:"omitempty,max=128"`
	Password string `json:"password" binding:"omitempty,max=128"` // 留空保留
	Org      string `json:"org" binding:"omitempty,max=64"`
}

// SaveO2Settings 保存 O2 连接配置（凭证 AES 落库；webhook token 自动生成）。
func (s *Service) SaveO2Settings(ctx context.Context, in O2SettingsInput) error {
	if s.cipher == nil {
		return crypto.ErrNoMasterKey
	}
	if in.Email != "" {
		if err := s.setSetting(ctx, settingO2Email, in.Email); err != nil {
			return err
		}
	}
	if in.Org != "" {
		if err := s.setSetting(ctx, settingO2Org, in.Org); err != nil {
			return err
		}
	}
	if in.Password != "" {
		enc, err := s.cipher.Encrypt(in.Password)
		if err != nil {
			return err
		}
		if err := s.setSetting(ctx, settingO2Password, enc); err != nil {
			return err
		}
	}
	// webhook 共享密钥：首次自动生成
	if t, _ := s.setting(ctx, settingO2Token); t == "" {
		tok, err := randomToken()
		if err != nil {
			return err
		}
		enc, err := s.cipher.Encrypt(tok)
		if err != nil {
			return err
		}
		return s.setSetting(ctx, settingO2Token, enc)
	}
	return nil
}

// O2SettingsOut 对外视图（不含明文密钥）。
type O2SettingsOut struct {
	Configured bool   `json:"configured"`
	Email      string `json:"email"`
	Org        string `json:"org"`
}

func (s *Service) O2Settings(ctx context.Context) O2SettingsOut {
	cfg, ok := s.o2Config(ctx)
	return O2SettingsOut{Configured: ok, Email: cfg.Email, Org: cfg.Org}
}

// ---- 告警 CRUD + 同步 ----

type SaveAlertInput struct {
	ID          uint   `json:"id"` // 模板路径更新时携带（平台直建路径不用，走路径参数）
	Name        string `json:"name" binding:"required,max=128"`
	StreamName  string `json:"streamName" binding:"required,max=128"`
	StreamType  string `json:"streamType" binding:"omitempty,oneof=logs metrics traces"`
	SQL         string `json:"sql" binding:"required,max=8000"`
	Period      int    `json:"period" binding:"min=1,max=1440"`
	Operator    string `json:"operator" binding:"required,oneof=> >= < <= = !="`
	Threshold   int    `json:"threshold" binding:"min=1"`
	Frequency   int    `json:"frequency" binding:"min=1,max=1440"`
	Silence     int    `json:"silence" binding:"min=0,max=1440"`
	Enabled     bool   `json:"enabled"`
	Description string `json:"description" binding:"max=255"`
	Level       string `json:"level" binding:"required,oneof=info warn critical"`
	// R1 模板化：项目侧经模板实例化时携带（dev 强制走该路径）
	ProjectID  uint              `json:"projectId"`
	TemplateID uint              `json:"templateId"`
	Params     map[string]string `json:"params"`
}

func (s *Service) ListAlerts(ctx context.Context) ([]Alert, error) {
	var as []Alert
	if err := s.db.WithContext(ctx).Order("id").Find(&as).Error; err != nil {
		return nil, err
	}
	return as, nil
}

// listProjectAlerts dev 视角：仅项目侧策略（平台级手写 SQL 告警不可见）。
func (s *Service) listProjectAlerts(ctx context.Context) ([]Alert, error) {
	var as []Alert
	if err := s.db.WithContext(ctx).Where("project_id > 0").Order("id").Find(&as).Error; err != nil {
		return nil, err
	}
	return as, nil
}

func (s *Service) getAlert(ctx context.Context, id uint) (*Alert, error) {
	var a Alert
	if err := s.db.WithContext(ctx).First(&a, id).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

// CreateAlert 平台落库 + 立即同步 O2。
func (s *Service) CreateAlert(ctx context.Context, in SaveAlertInput) (*Alert, error) {
	a := alertFromInput(in)
	a.SyncStatus = SyncPending
	if err := s.db.WithContext(ctx).Create(a).Error; err != nil {
		return nil, err
	}
	go s.SyncAlert(context.WithoutCancel(ctx), a.ID)
	return a, nil
}

func (s *Service) UpdateAlert(ctx context.Context, id uint, in SaveAlertInput) (*Alert, error) {
	var a Alert
	if err := s.db.WithContext(ctx).First(&a, id).Error; err != nil {
		return nil, gorm.ErrRecordNotFound
	}
	na := alertFromInput(in)
	// name 允许改（O2 侧删旧建新）；项目归属也可能变——删旧名按旧归属
	oldName, oldProject := a.Name, a.ProjectID
	a = *na
	a.ID = id
	a.SyncStatus = SyncPending
	a.SyncError = ""
	if err := s.db.WithContext(ctx).Model(&Alert{}).Where("id = ?", id).Updates(map[string]any{
		"name": a.Name, "stream_name": a.StreamName, "stream_type": a.StreamType,
		"sql": a.SQL, "period": a.Period, "operator": a.Operator, "threshold": a.Threshold,
		"frequency": a.Frequency, "silence": a.Silence, "enabled": a.Enabled,
		"description": a.Description, "level": a.Level,
		"project_id": a.ProjectID, "template_id": a.TemplateID, "params_snapshot": a.ParamsSnapshot,
		"sync_status": SyncPending, "sync_error": "",
	}).Error; err != nil {
		return nil, err
	}
	go func() {
		c := context.WithoutCancel(ctx)
		if oldName != a.Name { // O2 侧删旧（按旧记录算前缀名）
			old := *na
			old.Name = oldName
			old.ProjectID = oldProject
			s.deleteO2AlertQuiet(c, &old)
		}
		s.SyncAlert(c, id)
	}()
	return &a, nil
}

func (s *Service) DeleteAlert(ctx context.Context, id uint) error {
	var a Alert
	if err := s.db.WithContext(ctx).First(&a, id).Error; err != nil {
		return gorm.ErrRecordNotFound
	}
	if err := s.db.WithContext(ctx).Delete(&Alert{}, id).Error; err != nil {
		return err
	}
	go s.deleteO2AlertQuiet(context.WithoutCancel(ctx), &a)
	return nil
}

func alertFromInput(in SaveAlertInput) *Alert {
	st := in.StreamType
	if st == "" {
		st = "logs"
	}
	paramsJSON := ""
	if len(in.Params) > 0 {
		if b, err := json.Marshal(in.Params); err == nil {
			paramsJSON = string(b)
		}
	}
	return &Alert{
		Name: in.Name, StreamName: in.StreamName, StreamType: st,
		SQL: in.SQL, Period: in.Period, Operator: in.Operator, Threshold: in.Threshold,
		Frequency: in.Frequency, Silence: in.Silence, Enabled: in.Enabled,
		ProjectID: in.ProjectID, TemplateID: in.TemplateID, ParamsSnapshot: paramsJSON,
		Description: in.Description, Level: in.Level,
	}
}

// SyncAlert 同步单条告警到 O2（确保 destination/template 存在后 PUT alert）。
// 失败更新 sync_status=failed + 原文错误。
func (s *Service) SyncAlert(ctx context.Context, id uint) {
	var a Alert
	if err := s.db.WithContext(ctx).First(&a, id).Error; err != nil {
		return
	}
	if err := s.syncAlertToO2(ctx, &a); err != nil {
		logger.Warnf("[observ] 告警同步 O2 失败 %q: %v", a.Name, err)
		s.db.Model(&Alert{}).Where("id = ?", id).Updates(map[string]any{
			"sync_status": SyncFailed, "sync_error": truncateStr(err.Error(), 500),
		})
		return
	}
	s.db.Model(&Alert{}).Where("id = ?", id).Updates(map[string]any{
		"sync_status": SyncOK, "sync_error": "",
	})
}

// SyncAllAlerts 手动全量同步（重试入口）。
func (s *Service) SyncAllAlerts(ctx context.Context) (int, error) {
	var as []Alert
	if err := s.db.WithContext(ctx).Find(&as).Error; err != nil {
		return 0, err
	}
	for i := range as {
		s.SyncAlert(ctx, as[i].ID)
	}
	return len(as), nil
}

func (s *Service) deleteO2AlertQuiet(ctx context.Context, a *Alert) {
	cfg, ok := s.o2Config(ctx)
	if !ok {
		return
	}
	name := s.o2NameFor(a)
	// DELETE 路径参数同样是 alert_id（真机教训：传名称恒 404，残留孤儿告警）
	id := s.o2AlertIDByName(ctx, cfg, name)
	if id == "" {
		return
	}
	_, err := o2Request(ctx, cfg, http.MethodDelete, fmt.Sprintf("/api/v2/%s/alerts/%s", cfg.Org, id), nil)
	if err != nil && !strings.Contains(err.Error(), "404") {
		logger.Warnf("[observ] O2 删除告警失败 %q: %v", name, err)
	}
}

// syncAlertToO2 单条同步：ensure infra → PUT alert（upsert 语义）。
func (s *Service) syncAlertToO2(ctx context.Context, a *Alert) error {
	cfg, ok := s.o2Config(ctx)
	if !ok {
		return fmt.Errorf("O2 连接未配置（地址/账号/密码）")
	}
	if err := s.ensureO2Infra(ctx, cfg); err != nil {
		return fmt.Errorf("初始化 destination/template 失败: %w", err)
	}
	body := map[string]any{
		"name":        s.o2NameFor(a),
		"org_id":      cfg.Org,
		"stream_type": a.StreamType,
		"stream_name": a.StreamName,
		"query_condition": map[string]any{
			"type":   "sql",
			"sql":    a.SQL,
			"promql": nil,
		},
		"trigger_condition": map[string]any{
			"period":         a.Period,
			"operator":       a.Operator,
			"threshold":      a.Threshold,
			"frequency":      a.Frequency,
			"frequency_type": "minutes",
			"silence":        a.Silence,
			"cron":           "",
		},
		"destinations": []string{platformDestination},
		"enabled":      a.Enabled,
		"description":  a.Description,
		"tz_offset":    480, // Asia/Shanghai
	}
	// v0.91+ 的 alerts 在 /api/v2（destinations/templates 在无版本前缀——
	// 混合形态见 docs/research-o2-alerts.md 真机补充）。
	// 真机语义：POST 重名不报错而是静默创建重复条目，upsert 必须先按名
	// 反查 alert_id（PUT/DELETE 的路径参数均为 id）——命中则 PUT，未命中才 POST
	if id := s.o2AlertIDByName(ctx, cfg, s.o2NameFor(a)); id != "" {
		_, err := o2Request(ctx, cfg, http.MethodPut, fmt.Sprintf("/api/v2/%s/alerts/%s", cfg.Org, id), body)
		return err
	}
	_, err := o2Request(ctx, cfg, http.MethodPost, fmt.Sprintf("/api/v2/%s/alerts", cfg.Org), body)
	return err
}

// o2AlertIDByName 列表反查告警的 alert_id（PUT/DELETE 均需 id）；同名多条
// 返回最新一条（同名校验在平台侧，O2 侧出现同名属异常残留）。
func (s *Service) o2AlertIDByName(ctx context.Context, cfg *O2Config, name string) string {
	b, err := o2Request(ctx, cfg, http.MethodGet, fmt.Sprintf("/api/v2/%s/alerts", cfg.Org), nil)
	if err != nil {
		return ""
	}
	var out struct {
		List []struct {
			AlertID string `json:"alert_id"`
			Name    string `json:"name"`
		} `json:"list"`
	}
	if json.Unmarshal(b, &out) != nil {
		return ""
	}
	id := ""
	for _, a := range out.List {
		if a.Name == name {
			id = a.AlertID
		}
	}
	return id
}

// ensureO2Infra 幂等确保平台专用 destination 与模板存在。
func (s *Service) ensureO2Infra(ctx context.Context, cfg *O2Config) error {
	// 平台对外可达地址：webhook 回流入口（复用 CUSTOS_IM_PUBLIC_URL——
	// 平台对外只有这一个地址语义，不为 O2 单独引入第二个地址配置）
	if s.publicURL == "" {
		return fmt.Errorf("未配置平台对外地址（CUSTOS_IM_PUBLIC_URL）")
	}
	hookURL := strings.TrimRight(s.publicURL, "/") + "/api/observ/alerts/webhook"
	headers := map[string]string{"X-Custos-Token": cfg.Token}
	if cfg.Token == "" {
		return fmt.Errorf("webhook 共享密钥缺失（重新保存 O2 连接配置以生成）")
	}

	// template：单大括号替换（真机实测 v0.91 双大括号只部分渲染）
	tplBody := map[string]any{
		"name":       platformTemplate,
		"body":       platformTemplateBody,
		"is_default": false,
	}
	if _, err := o2Request(ctx, cfg, http.MethodPost, fmt.Sprintf("/api/%s/alerts/templates", cfg.Org), tplBody); err != nil {
		if !strings.Contains(err.Error(), "already exist") {
			return fmt.Errorf("template: %w", err)
		}
		// 已存在也要 PUT 覆盖——模板体演进（如双大括号→单大括号修复）才能下发
		if _, err2 := o2Request(ctx, cfg, http.MethodPut,
			fmt.Sprintf("/api/%s/alerts/templates/%s", cfg.Org, platformTemplate), tplBody); err2 != nil {
			return fmt.Errorf("template: %w", err2)
		}
	}

	// destination（POST 已存在则 PUT 覆盖——URL/token 可能轮换）
	destBody := map[string]any{
		"name":            platformDestination,
		"url":             hookURL,
		"method":          "post",
		"headers":         headers,
		"template":        platformTemplate,
		"skip_tls_verify": false,
	}
	if _, err := o2Request(ctx, cfg, http.MethodPost, fmt.Sprintf("/api/%s/alerts/destinations", cfg.Org), destBody); err != nil {
		if strings.Contains(err.Error(), "already exist") {
			if _, err2 := o2Request(ctx, cfg, http.MethodPut,
				fmt.Sprintf("/api/%s/alerts/destinations/%s", cfg.Org, platformDestination), destBody); err2 != nil {
				return fmt.Errorf("destination: %w", err2)
			}
		} else {
			return fmt.Errorf("destination: %w", err)
		}
	}
	return nil
}

// o2Request O2 API 调用（Basic 认证，JSON body；错误带状态码与响应原文片段）。
func o2Request(ctx context.Context, cfg *O2Config, method, path string, body any) ([]byte, error) {
	url := strings.TrimRight(cfg.BaseURL, "/") + path
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(cfg.Email, cfg.Password)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s: HTTP %d: %.300s", method, path, resp.StatusCode, out)
	}
	return out, nil
}

// setSetting 通用平台设置写入（upsert）。
func (s *Service) setSetting(ctx context.Context, key, value string) error {
	return s.db.WithContext(ctx).Exec(
		`INSERT INTO platform_settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value).Error
}

// randomToken 生成 webhook 共享密钥（16 字节 hex）。
func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// alertByName 按名取告警定义（webhook 定级用）。
// o2NameFor O2 侧告警名：项目侧策略带项目前缀（防跨项目同名互踩；
// webhook 回流时反解平台名）。projectID=0 平台级原名。
func (s *Service) o2NameFor(a *Alert) string {
	if a.ProjectID == 0 {
		return a.Name
	}
	var pname string
	if err := s.db.WithContext(context.Background()).
		Table("projects").Select("name").Where("id = ?", a.ProjectID).Scan(&pname).Error; err != nil || pname == "" {
		return fmt.Sprintf("p%d-%s", a.ProjectID, a.Name)
	}
	return strx.NormalizeName(pname) + "-" + a.Name
}

func (s *Service) alertByName(ctx context.Context, name string) (*Alert, error) {
	// webhook 回流的 alert_name 是 O2 侧名（项目策略带前缀）——先精确匹配
	// 平台名，miss 后按 o2NameFor 全量反查（告警量小，可接受）
	var a Alert
	if err := s.db.WithContext(ctx).Where("name = ?", name).First(&a).Error; err == nil {
		return &a, nil
	}
	var all []Alert
	if err := s.db.WithContext(ctx).Find(&all).Error; err != nil {
		return nil, err
	}
	for i := range all {
		if s.o2NameFor(&all[i]) == name {
			return &all[i], nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

// Digestor AI 诊断摘要出口（ai.DigestService 实现，app 层注入；可空 = 降级纯通知）。
type Digestor interface {
	MaybeDigest(ctx context.Context, alertName, alertBody string)
}

// notifyAlert O2 告警事件投统一通知路由（先发主通知保及时性；AI 摘要异步补发）。
func (s *Service) notifyAlert(ctx context.Context, alertName, level, detail string) {
	if s.digestor != nil {
		s.digestor.MaybeDigest(context.WithoutCancel(ctx), alertName, detail)
	}
	if s.notifier == nil {
		return
	}
	go func() {
		s.notifier.NotifyEvent(context.WithoutCancel(ctx),
			notify.SourceO2Alert, level,
			"o2-"+alertName, "O2 告警："+alertName, detail)
	}()
}

// Models 本模块自动迁移清单。
func Models() []any { return []any{&Alert{}, &AlertTemplate{}} }
