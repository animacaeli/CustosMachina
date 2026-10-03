package observ

// 告警模板（R1 告警模板化）：管理员预设带占位符的查询体，项目侧填参实例化
// 后同步 O2。渲染在服务端完成——dev 角色只能经模板建告警（不能手写 SQL，
// 占位符值做字面量转义，杜绝经参数注入任意查询）。

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/pkg/httpx"
)

// Placeholder 占位符元数据（Query 中以 {{key}} 引用）。
type Placeholder struct {
	Key      string `json:"key"`      // {{key}} 中的 key
	Label    string `json:"label"`    // 表单显示名
	Type     string `json:"type"`     // string | number
	Required bool   `json:"required"` // 必填
	Default  string `json:"default"`  // 缺省值（非必填且留空时使用）
	Hint     string `json:"hint"`     // 输入提示
}

// AlertTemplate 管理员维护的告警查询模板。
type AlertTemplate struct {
	ID               uint   `gorm:"primarykey" json:"id"`
	Name             string `gorm:"size:128;uniqueIndex;not null" json:"name"`
	Description      string `gorm:"size:255" json:"description"`
	Category         string `gorm:"size:64" json:"category"`                       // 分类（日志/数据库/主机…）
	QueryType        string `gorm:"size:16;not null;default:sql" json:"queryType"` // sql（promql R3 指标接入后开放）
	Query            string `gorm:"type:text;not null" json:"query"`               // 带 {{占位符}} 的查询体
	PlaceholdersJSON string `gorm:"type:text" json:"placeholdersJson"`             // []Placeholder 序列化
	// 默认触发参数（实例化时可覆盖）
	Period    int       `gorm:"not null;default:10" json:"period"`
	Operator  string    `gorm:"size:8;not null;default=>=" json:"operator"`
	Threshold int       `gorm:"not null;default:1" json:"threshold"`
	Frequency int       `gorm:"not null;default:1" json:"frequency"`
	Silence   int       `gorm:"not null;default:10" json:"silence"`
	Level     string    `gorm:"size:8;not null;default:warn" json:"level"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (AlertTemplate) TableName() string { return "observ_alert_templates" }

// Placeholders 反序列化占位符元数据（坏数据容错为空表）。
func (t *AlertTemplate) Placeholders() []Placeholder {
	var out []Placeholder
	if t.PlaceholdersJSON == "" {
		return out
	}
	_ = json.Unmarshal([]byte(t.PlaceholdersJSON), &out)
	return out
}

type SaveTemplateInput struct {
	Name         string        `json:"name" binding:"required,max=128"`
	Description  string        `json:"description"`
	Category     string        `json:"category"`
	QueryType    string        `json:"queryType" binding:"omitempty,oneof=sql promql"`
	Query        string        `json:"query" binding:"required,max=8192"`
	Placeholders []Placeholder `json:"placeholders"`
	Period       int           `json:"period" binding:"omitempty,min=1,max=1440"`
	Operator     string        `json:"operator" binding:"omitempty,oneof=>= <= > < = !="`
	Threshold    int           `json:"threshold" binding:"omitempty,min=0"`
	Frequency    int           `json:"frequency" binding:"omitempty,min=1,max=1440"`
	Silence      int           `json:"silence" binding:"omitempty,min=0,max=14400"`
	Level        string        `json:"level" binding:"omitempty,oneof=info warn critical"`
}

var placeholderRe = regexp.MustCompile(`\{\{\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\}\}`)

func validateTemplate(in SaveTemplateInput) error {
	if in.QueryType == "" {
		in.QueryType = "sql"
	}
	// 占位符与元数据双向一致：查询里每个 {{k}} 有元数据，元数据不悬空
	used := map[string]bool{}
	for _, m := range placeholderRe.FindAllStringSubmatch(in.Query, -1) {
		used[m[1]] = true
	}
	seen := map[string]bool{}
	for _, p := range in.Placeholders {
		if p.Key == "" {
			return fmt.Errorf("占位符 key 不能为空")
		}
		if seen[p.Key] {
			return fmt.Errorf("占位符 %q 重复", p.Key)
		}
		seen[p.Key] = true
		if p.Type != "" && p.Type != "string" && p.Type != "number" {
			return fmt.Errorf("占位符 %q 类型只支持 string/number", p.Key)
		}
		if !used[p.Key] {
			return fmt.Errorf("占位符 %q 在查询体中未被 {{%s}} 引用", p.Key, p.Key)
		}
	}
	for k := range used {
		if !seen[k] {
			return fmt.Errorf("查询体中的 {{%s}} 缺少占位符定义", k)
		}
	}
	return nil
}

func templateFromInput(in SaveTemplateInput) (*AlertTemplate, error) {
	t := &AlertTemplate{
		Name: in.Name, Description: in.Description, Category: in.Category,
		Query: in.Query, Period: in.Period, Operator: in.Operator,
		Threshold: in.Threshold, Frequency: in.Frequency, Silence: in.Silence,
		Level: in.Level,
	}
	if t.QueryType = in.QueryType; t.QueryType == "" {
		t.QueryType = "sql"
	}
	if t.Period == 0 {
		t.Period = 10
	}
	if t.Operator == "" {
		t.Operator = ">="
	}
	if t.Frequency == 0 {
		t.Frequency = 1
	}
	if t.Level == "" {
		t.Level = "warn"
	}
	b, err := json.Marshal(in.Placeholders)
	if err != nil {
		return nil, err
	}
	t.PlaceholdersJSON = string(b)
	return t, nil
}

// ListTemplates 模板列表（含引用计数——删除时前端提示用）。
func (s *Service) ListTemplates(ctx context.Context) ([]map[string]any, error) {
	var ts []AlertTemplate
	if err := s.db.WithContext(ctx).Order("id").Find(&ts).Error; err != nil {
		return nil, err
	}
	type cnt struct {
		TemplateID uint
		N          uint
	}
	var counts []cnt
	if err := s.db.WithContext(ctx).Model(&Alert{}).
		Select("template_id, count(*) as n").Where("template_id > 0").
		Group("template_id").Scan(&counts).Error; err != nil {
		return nil, err
	}
	cm := map[uint]uint{}
	for _, c := range counts {
		cm[c.TemplateID] = c.N
	}
	out := make([]map[string]any, 0, len(ts))
	for i := range ts {
		out = append(out, map[string]any{
			"id": ts[i].ID, "name": ts[i].Name, "description": ts[i].Description,
			"category": ts[i].Category, "queryType": ts[i].QueryType, "query": ts[i].Query,
			"placeholders": ts[i].Placeholders(), "period": ts[i].Period,
			"operator": ts[i].Operator, "threshold": ts[i].Threshold,
			"frequency": ts[i].Frequency, "silence": ts[i].Silence, "level": ts[i].Level,
			"boundCount": cm[ts[i].ID], "createdAt": ts[i].CreatedAt,
		})
	}
	return out, nil
}

func (s *Service) CreateTemplate(ctx context.Context, in SaveTemplateInput) (*AlertTemplate, error) {
	if err := validateTemplate(in); err != nil {
		return nil, err
	}
	t, err := templateFromInput(in)
	if err != nil {
		return nil, err
	}
	if err := s.db.WithContext(ctx).Create(t).Error; err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Service) UpdateTemplate(ctx context.Context, id uint, in SaveTemplateInput) (*AlertTemplate, error) {
	var t AlertTemplate
	if err := s.db.WithContext(ctx).First(&t, id).Error; err != nil {
		return nil, gorm.ErrRecordNotFound
	}
	if err := validateTemplate(in); err != nil {
		return nil, err
	}
	nt, err := templateFromInput(in)
	if err != nil {
		return nil, err
	}
	t = *nt
	t.ID = id
	if err := s.db.WithContext(ctx).Save(&t).Error; err != nil {
		return nil, err
	}
	// 模板修改不影响已实例化的告警（快照语义）——仅元数据提示
	return &t, nil
}

func (s *Service) DeleteTemplate(ctx context.Context, id uint) error {
	var n int64
	if err := s.db.WithContext(ctx).Model(&Alert{}).
		Where("template_id = ?", id).Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("仍有 %d 条告警策略引用该模板（已建策略不受影响，可先迁移再删除）", n)
	}
	return s.db.WithContext(ctx).Delete(&AlertTemplate{}, id).Error
}

// RenderTemplate 服务端渲染：占位符替换 + 值转义。
// string → 'v'（单引号翻倍转义）；number → 纯数字校验；必填校验；默认值兜底。
func (s *Service) RenderTemplate(ctx context.Context, templateID uint, params map[string]string) (string, error) {
	var t AlertTemplate
	if err := s.db.WithContext(ctx).First(&t, templateID).Error; err != nil {
		return "", gorm.ErrRecordNotFound
	}
	ph := map[string]Placeholder{}
	for _, p := range t.Placeholders() {
		ph[p.Key] = p
	}
	// PromQL 模板的字符串占位符直接替换原文（不加 SQL 引号——
	// PromQL 里的引号/区间如 "[2m]" 由模板作者掌控）
	rawStrings := t.QueryType == "promql"
	var missing []string
	rendered := placeholderRe.ReplaceAllStringFunc(t.Query, func(m string) string {
		key := placeholderRe.FindStringSubmatch(m)[1]
		p, ok := ph[key]
		if !ok {
			return m
		}
		v := strings.TrimSpace(params[key])
		if v == "" {
			if p.Default != "" {
				v = p.Default
			} else if p.Required {
				missing = append(missing, p.labelOrKey())
				return m
			} else {
				return "''"
			}
		}
		if rawStrings {
			return v
		}
		if p.Type == "number" {
			if _, err := strconv.ParseFloat(v, 64); err != nil {
				missing = append(missing, p.labelOrKey()+"（须为数字）")
				return m
			}
			return v
		}
		return "'" + strings.ReplaceAll(v, "'", "''") + "'"
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("参数无效：%s", strings.Join(missing, "、"))
	}
	return rendered, nil
}

func (p Placeholder) labelOrKey() string {
	if p.Label != "" {
		return p.Label
	}
	return p.Key
}

// InstantiateFromTemplate dev 角色建告警的强制路径：SQL 由服务端按模板渲染，
// 触发参数取模板默认（显式覆盖仅允许 period/threshold/level/silence）。
func (s *Service) InstantiateFromTemplate(ctx context.Context, in SaveAlertInput) (*SaveAlertInput, error) {
	if in.TemplateID == 0 {
		return nil, fmt.Errorf("dev 角色只能基于模板创建告警策略")
	}
	if in.ProjectID == 0 {
		return nil, fmt.Errorf("项目侧告警策略必须归属项目")
	}
	var t AlertTemplate
	if err := s.db.WithContext(ctx).First(&t, in.TemplateID).Error; err != nil {
		return nil, fmt.Errorf("模板不存在")
	}
	sql, err := s.RenderTemplate(ctx, in.TemplateID, in.Params)
	if err != nil {
		return nil, err
	}
	// 服务端强制覆盖：忽略客户端传入的 SQL 与流名（模板语义），触发参数缺省取模板
	out := in
	out.SQL = sql
	// 查询类型随模板（promql 型同步 O2 时走 metrics + promql_condition；
	// O2 校验 stream 存在——取查询里的主指标名作为流名）
	if t.QueryType == "promql" {
		out.QueryType = "promql"
		if strings.TrimSpace(out.StreamName) == "" {
			out.StreamName = firstMetricName(sql)
		}
	} else if strings.TrimSpace(in.StreamName) == "" {
		out.StreamName = "default"
	}
	if out.Period == 0 {
		out.Period = t.Period
	}
	if out.Operator == "" {
		out.Operator = t.Operator
	}
	if out.Threshold == 0 {
		out.Threshold = t.Threshold
	}
	if out.Frequency == 0 {
		out.Frequency = t.Frequency
	}
	if out.Silence == 0 {
		out.Silence = t.Silence
	}
	if out.Level == "" {
		out.Level = t.Level
	}
	return &out, nil
}

// ---- handler ----

func (h *Handler) listTemplates(c *gin.Context) {
	out, err := h.svc.ListTemplates(c.Request.Context())
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *Handler) createTemplate(c *gin.Context) {
	var in SaveTemplateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	out, err := h.svc.CreateTemplate(c.Request.Context(), in)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, out)
}

func (h *Handler) updateTemplate(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	var in SaveTemplateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	out, err := h.svc.UpdateTemplate(c.Request.Context(), id, in)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, out)
}

func (h *Handler) deleteTemplate(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteTemplate(c.Request.Context(), id); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{"ok": true})
}

// renderTemplate 渲染预览（项目侧配置时实时预览 SQL，登录即可用——只读无副作用）。
func (h *Handler) renderTemplate(c *gin.Context) {
	var in struct {
		TemplateID uint              `json:"templateId" binding:"required"`
		Params     map[string]string `json:"params"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	sql, err := h.svc.RenderTemplate(c.Request.Context(), in.TemplateID, in.Params)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{"sql": sql})
}

// FromTemplateInput 项目侧实例化入参（弱校验：触发参数留空取模板默认，
// 不复用直建路径的 required 强校验——SQL/流名/操作符均不接受客户端值）。
type FromTemplateInput struct {
	ID          uint              `json:"id"` // >0 = 更新
	Name        string            `json:"name" binding:"required,max=128"`
	ProjectID   uint              `json:"projectId" binding:"required"`
	TemplateID  uint              `json:"templateId" binding:"required"`
	Params      map[string]string `json:"params"`
	StreamName  string            `json:"streamName" binding:"omitempty,max=128"`
	Description string            `json:"description" binding:"omitempty,max=255"`
	Enabled     *bool             `json:"enabled"`
	Period      int               `json:"period" binding:"omitempty,min=1,max=1440"`
	Threshold   int               `json:"threshold" binding:"omitempty,min=0"`
	Silence     int               `json:"silence" binding:"omitempty,min=0,max=1440"`
	Frequency   int               `json:"frequency" binding:"omitempty,min=1,max=1440"`
	Level       string            `json:"level" binding:"omitempty,oneof=info warn critical"`
}

// upsertAlertFromTemplate 项目侧实例化（创建/更新二合一：id=0 创建）。
// 对所有调用者统一模板语义——SQL 服务端渲染、触发参数缺省取模板默认，
// 项目归属必须显式携带；更新时还要求目标本就是项目侧策略。
func (h *Handler) upsertAlertFromTemplate(c *gin.Context) {
	var raw FromTemplateInput
	if err := c.ShouldBindJSON(&raw); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	enabled := raw.Enabled == nil || *raw.Enabled
	in := SaveAlertInput{
		ID: raw.ID, Name: raw.Name, StreamName: raw.StreamName, StreamType: "logs",
		Period: raw.Period, Operator: "", Threshold: raw.Threshold,
		Frequency: raw.Frequency, Silence: raw.Silence, Enabled: enabled,
		Description: raw.Description, Level: raw.Level,
		ProjectID: raw.ProjectID, TemplateID: raw.TemplateID, Params: raw.Params,
	}
	// 服务端渲染 SQL + 模板默认参数兜底（忽略客户端 SQL）
	rendered, err := h.svc.InstantiateFromTemplate(c.Request.Context(), in)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if rendered.Name == "" {
		httpx.FailBadRequest(c, "策略名不能为空")
		return
	}
	if in.ID > 0 { // 更新：仅项目侧策略可经此路径
		a, err := h.svc.getAlert(c.Request.Context(), in.ID)
		if err != nil {
			httpx.FailBadRequest(c, "策略不存在")
			return
		}
		if a.ProjectID == 0 {
			httpx.Fail(c, 403, 403, "平台级告警不能经模板路径修改")
			return
		}
		if a.ProjectID != rendered.ProjectID {
			httpx.FailBadRequest(c, "不能跨项目修改策略")
			return
		}
		rendered.Name = mergeAlertName(a.Name, rendered.Name)
		if _, err := h.svc.UpdateAlert(c.Request.Context(), in.ID, *rendered); err != nil {
			httpx.FailBadRequest(c, err.Error())
			return
		}
		httpx.OK(c, gin.H{"ok": true, "id": in.ID})
		return
	}
	out, err := h.svc.CreateAlert(c.Request.Context(), *rendered)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, out)
}

// mergeAlertName 更新时名字冲突回退（唯一索引；未改名则沿用旧名避免
// "同名再建"歧义——改名走名称比对删除旧 O2 告警的既有逻辑）。
func mergeAlertName(oldName, newName string) string {
	if newName == "" {
		return oldName
	}
	return newName
}

// firstMetricName 从 PromQL 提取第一个指标名（跳过函数/关键字）——
// O2 的 promql 告警要求 stream_name 指向真实存在的 metrics 流。
var promqlFuncs = map[string]bool{
	"rate": true, "irate": true, "increase": true, "delta": true, "idelta": true,
	"avg": true, "sum": true, "min": true, "max": true, "count": true,
	"stddev": true, "stdvar": true, "quantile": true, "topk": true, "bottomk": true,
	"by": true, "without": true, "on": true, "ignoring": true, "group_left": true,
	"group_right": true, "offset": true, "bool": true, "and": true, "or": true,
	"unless": true, "vector": true, "scalar": true, "absent": true, "clamp": true,
}

var (
	// 指标选择器形态：指标名后紧跟 {（最可靠——label selector）
	metricSelectorRe = regexp.MustCompile(`([a-zA-Z_:][a-zA-Z0-9_:]*)\s*\{`)
	metricNameRe     = regexp.MustCompile(`[a-zA-Z_:][a-zA-Z0-9_:]*`)
)

func firstMetricName(promql string) string {
	// 1) 带 label selector 的指标名（avg by(instance) 的 label 不会带 {）
	for _, m := range metricSelectorRe.FindAllStringSubmatch(promql, -1) {
		if name := m[1]; !promqlFuncs[name] {
			return name
		}
	}
	// 2) 退化：第一个非函数 token
	for _, tok := range metricNameRe.FindAllString(promql, -1) {
		if !promqlFuncs[tok] {
			return tok
		}
	}
	return "node_cpu_seconds_total" // 兜底：主机 CPU 指标恒有（observ 部署约定）
}
