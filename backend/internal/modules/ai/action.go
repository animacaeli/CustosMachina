// action.go P6-M4 NL→操作（建议卡形态，用户 2026-10-05 定调）：
// AI 绝不执行任何变更操作（不论用户身份，管理员也不行）——仅生成结构化
// 「操作建议卡」，用户点击跳转平台对应页面自行操作。担忧根因：AI 执行存在
// 漂移与未知问题，不会按固定规则执行。白名单三件套：容器重启 / cron 手动
// 触发 / 配置文件下发。同时杜绝 AI 触及安装类操作（skill/MCP 管理仅 admin
// 经 REST，AI 工具集为只读白名单，system prompt 明确拒答此类请求）。
package ai

import (
	"context"
	"encoding/json"
	"fmt"
)

// 白名单操作类型（意图工具名 = 建议类型，模型 function calling 触发生成建议卡）。
const (
	ActRestartContainer = "restart_container"
	ActTriggerCron      = "trigger_cron"
	ActDeployConfig     = "deploy_config"
)

// ActionTrace 消息留痕与 SSE 事件载荷（建议卡无状态机——生成即展示，
// 不存在确认/执行/过期语义）。
type ActionTrace struct {
	Type    string `json:"type"`
	Summary string `json:"summary"`
	Route   string `json:"route,omitempty"` // 「去处理」跳转目标（平台页面）
}

// ChatActionDef 建议卡白名单项：Type 即意图工具名；Parameters 为 JSON Schema；
// Summary 渲染人读摘要；Route 为前端跳转路径。校验（ValidateChatAction）
// 拒绝幻觉 ID——建议卡的摘要必须指向真实存在的对象。
type ChatActionDef struct {
	Type        string
	Description string
	Parameters  map[string]any
	Summary     func(params map[string]any) string
	Route       func(params map[string]any) string
}

// ChatActionSource app 层注入（与 ChatToolSource 同一桥接纪律）：
// 仅提供建议卡定义与参数校验——没有执行语义，AI 与确认执行链路不复存在。
type ChatActionSource interface {
	ChatActions() []ChatActionDef
	ValidateChatAction(ctx context.Context, typ string, params map[string]any) (summary string, err error)
}

// renderAction 建议卡三元组（校验通过后调用；params 已含校验回填字段）。
func renderAction(def ChatActionDef, params map[string]any) ActionTrace {
	route := ""
	if def.Route != nil {
		route = def.Route(params)
	}
	return ActionTrace{Type: def.Type, Summary: def.Summary(params), Route: route}
}

// suggestAction 意图工具调用处理：校验参数真实存在 → 生成建议卡（onAction
// 回调 + 留痕）→ 回给模型的说明（AI 不执行，引导用户点卡跳转操作）。
// 校验失败（幻觉 ID）以文本回填，模型可用只读工具核实后重试。
func (s *ChatService) suggestAction(ctx context.Context, def *ChatActionDef, call ToolCall, used *[]ActionTrace, onAction func(ActionTrace)) string {
	args := map[string]any{}
	if call.Function.Arguments != "" {
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return "参数解析失败: " + err.Error()
		}
	}
	summary, err := s.ActionSource.ValidateChatAction(ctx, def.Type, args)
	if err != nil {
		return "建议未生成: " + err.Error() + "（请先用查询工具核实对象是否真实存在，修正后重试，不要凭记忆构造参数）"
	}
	trace := renderAction(*def, args)
	trace.Summary = summary
	*used = append(*used, trace)
	if onAction != nil {
		onAction(trace)
	}
	return fmt.Sprintf("已生成操作建议卡：%s。你不会执行任何变更操作——请明确告知用户：点击卡片上的「去处理」跳转到平台对应页面自行操作。", trace.Summary)
}
