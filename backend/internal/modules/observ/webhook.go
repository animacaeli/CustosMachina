package observ

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/modules/notify"
	"github.com/custos-machina/backend/internal/pkg/httpx"
)

// O2 告警 webhook 接收（P5 M3）：O2 侧告警触发 → 渲染平台模板 JSON
// → POST 到本端点（X-Custos-Token 共享密钥防伪造）→ 统一通知路由。
//
// 模板输出契约（ensureO2Infra 维护，双方约定字段）：
// {"alert_name","stream_name","org_name","alert_type","trigger_time","rows_count","rows":[...]}

type o2AlertPayload struct {
	AlertName  string          `json:"alert_name"`
	StreamName string          `json:"stream_name"`
	OrgName    string          `json:"org_name"`
	AlertType  string          `json:"alert_type"`
	TriggerAt  string          `json:"trigger_time"`
	RowsCount  int             `json:"rows_count"`
	Rows       json.RawMessage `json:"rows"`
}

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
	var p o2AlertPayload
	if err := c.ShouldBindJSON(&p); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if p.AlertName == "" {
		httpx.FailBadRequest(c, "alert_name 缺失")
		return
	}
	// 告警级别取平台侧定义（无定义默认 warn——告警可能在平台删除后仍触发）
	level := notify.LevelWarn
	if a, err := h.svc.alertByName(c.Request.Context(), p.AlertName); err == nil && a.Level != "" {
		level = a.Level
	}
	detail := formatO2AlertDetail(p)
	h.svc.notifyAlert(c.Request.Context(), p.AlertName, level, detail)
	httpx.OK(c, gin.H{"ok": true})
}

// formatO2AlertDetail 组装通知正文（行数 + 前几条日志摘要）。
func formatO2AlertDetail(p o2AlertPayload) string {
	detail := "告警：" + p.AlertName +
		"\n数据流：" + p.StreamName + "（" + p.OrgName + "）" +
		"\n命中：" + strconv.Itoa(p.RowsCount) + " 条 @ " + p.TriggerAt
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
