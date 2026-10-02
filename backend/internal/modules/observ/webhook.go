package observ

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/modules/notify"
	"github.com/custos-machina/backend/internal/pkg/httpx"
)

// O2 告警 webhook 接收（P5 M3）：O2 侧告警触发 → 渲染平台模板 JSON
// → POST 到本端点（X-Custos-Token 共享密钥防伪造）→ 统一通知路由。
//
// 模板输出契约（ensureO2Infra 维护，双方约定字段）：
// {"alert_name","stream_name","org_name","alert_type","trigger_time","rows_count"}
// 真机（v0.91 企业版）教训：部分变量（trigger_time/rows_count）不被渲染、
// 保留占位符，因此模板全部值加引号保证 JSON 合法，且本端解析全程容错——
// 解析失败时提取 alert_name、原文进通知，告警不能因模板差异被丢掉。

type o2AlertPayload struct {
	AlertName  string          `json:"alert_name"`
	StreamName string          `json:"stream_name"`
	OrgName    string          `json:"org_name"`
	AlertType  string          `json:"alert_type"`
	TriggerAt  string          `json:"trigger_time"`
	RowsCount  flexInt         `json:"rows_count"`
	Rows       json.RawMessage `json:"rows"`
}

// flexInt 兼容数字与字符串数字（模板把 rows_count 渲染成带引号字符串，
// 未渲染时是 "{rows_count}" 占位符），解析失败一律归零。
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		*f = flexInt(n)
	}
	return nil
}

var alertNameRe = regexp.MustCompile(`"alert_name"\s*:\s*"([^"]+)"`)

func (h *Handler) o2AlertWebhook(c *gin.Context) {
	cfg, _ := h.svc.o2Config(c.Request.Context())
	if cfg.Token == "" {
		httpx.Fail(c, http.StatusServiceUnavailable, 503, "O2 集成未配置")
		return
	}
	if c.GetHeader("X-Custos-Token") != cfg.Token {
		httpx.FailUnauthorized(c, "token 无效")
		return
	}
	raw, _ := c.GetRawData()
	var p o2AlertPayload
	detail := ""
	if err := json.Unmarshal(raw, &p); err == nil && p.AlertName != "" {
		detail = formatO2AlertDetail(p)
	} else if m := alertNameRe.FindSubmatch(raw); m != nil {
		p.AlertName = string(m[1])
		detail = "告警：" + p.AlertName + "\n（模板渲染异常，原始内容）" + truncateStr(string(raw), 400)
	} else {
		httpx.FailBadRequest(c, "payload 无法解析且缺 alert_name")
		return
	}
	// 告警级别取平台侧定义（无定义默认 warn——告警可能在平台删除后仍触发）
	level := notify.LevelWarn
	if a, err := h.svc.alertByName(c.Request.Context(), p.AlertName); err == nil && a.Level != "" {
		level = a.Level
	}
	h.svc.notifyAlert(c.Request.Context(), p.AlertName, level, detail)
	httpx.OK(c, gin.H{"ok": true})
}

// formatO2AlertDetail 组装通知正文（行数 + 前几条日志摘要）。
func formatO2AlertDetail(p o2AlertPayload) string {
	detail := "告警：" + p.AlertName +
		"\n数据流：" + p.StreamName + "（" + p.OrgName + "）" +
		"\n命中：" + strconv.Itoa(int(p.RowsCount)) + " 条 @ " + p.TriggerAt
	// rows 可能为数组；截前 3 条原样摘要
	var rows []map[string]any
	if err := json.Unmarshal(p.Rows, &rows); err == nil {
		for i, r := range rows {
			if i >= 3 {
				detail += "\n…（其余省略）"
				break
			}
			if b, err := json.Marshal(r); err == nil {
				detail += "\n· " + truncateStr(string(b), 200)
			}
		}
	}
	return detail
}
