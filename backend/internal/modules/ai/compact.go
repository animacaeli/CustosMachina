// compact.go P7-M4 上下文管理：token 估算 + 会话压缩（前情提要）。
// 两个入口共用一套状态（Conversation.CompactText / CompactAfterID）：
//   - 自动：构造 prompt 时估算超窗口 70% → 把较早历史压缩为摘要（保留最近
//     compactKeepN 条消息原文），失败降级为硬截断（丢最早轮次，绝不因压缩
//     失败阻塞对话）；
//   - 手动：用户输入 /compact → 全量历史压缩成 ≤500 字摘要，后续对话基于摘要继续。
package ai

import (
	"context"
	"fmt"
	"strings"
)

// compactKeepN 自动压缩时保留的最近消息条数（一轮问答约 2 条）。
const compactKeepN = 8

// autoCompactThreshold 自动压缩触发阈值（占上下文窗口比例）。
const autoCompactThreshold = 0.7

// defaultContextWindow 中转层未配置窗口时的保守默认。
const defaultContextWindow = 32768

// compactMaxSummary 压缩摘要长度上限（字符）。
const compactMaxSummary = 1200

// estimateTokens 粗估消息集 token 数：中文 1 字 ≈ 1 token、ASCII ≈ 0.3/字符
// （计划口径"中文字符 ÷ 1.5"的工程近似；只用于阈值判断，不求精确）。
func estimateTokens(msgs []Message) int {
	// 内部以 1/10 token 为单位累计，避免逐字符整数除法归零
	n := 0
	for _, m := range msgs {
		s, _ := m.Content.(string)
		for _, r := range s {
			if r > 0x2E80 { // CJK 及全角区
				n += 10
			} else {
				n += 3
			}
		}
		if len(m.ToolCalls) > 0 {
			n += 400 * len(m.ToolCalls) // 工具调用结构开销近似
		}
	}
	return n / 10
}

// maybeAutoCompact 估算超阈值时压缩较早历史（原地更新会话与 msgs）。
// 返回调整后的 msgs；压缩失败降级为截断（丢最早轮次直到回到阈值内）。
func (s *ChatService) maybeAutoCompact(ctx context.Context, c *Conversation,
	msgs []Message, history []ChatMessage) []Message {
	if s.relay == nil {
		return msgs // 测试/未装配中转层：跳过自动压缩（调用方自控）
	}
	window := s.relay.ContextWindow(ctx)
	if window <= 0 {
		window = defaultContextWindow
	}
	budget := int(float64(window) * autoCompactThreshold)
	if estimateTokens(msgs) <= budget {
		return msgs
	}
	// 压缩区 = history 中除最近 compactKeepN 条外的部分
	if len(history) <= compactKeepN {
		// 历史太短压缩无意义（大头在 system/数据块），但仍需兜底截断回预算内
		return truncateMessages(msgs, budget)
	}
	cut := len(history) - compactKeepN
	part := history[:cut]
	merged, err := s.summarize(ctx, c.CompactText, part)
	if err == nil {
		if err := s.db.WithContext(ctx).Model(&Conversation{}).
			Where("id = ?", c.ID).
			Updates(map[string]any{
				"compact_text":     merged,
				"compact_after_id": part[len(part)-1].ID,
			}).Error; err == nil {
			c.CompactText, c.CompactAfterID = merged, part[len(part)-1].ID
			return rebuildWithCompact(msgs, merged)
		}
	}
	// 降级：硬截断最早轮次（user/assistant 成对丢，保 system）
	return truncateMessages(msgs, budget)
}

// Compact 手动 /compact：全量历史压成 ≤500 字摘要；返回摘要给前端展示。
func (s *ChatService) Compact(ctx context.Context, c *Conversation) (string, error) {
	var history []ChatMessage
	if err := s.db.WithContext(ctx).
		Where("conversation_id = ? AND id > ?", c.ID, c.CompactAfterID).
		Order("id ASC").Find(&history).Error; err != nil {
		return "", err
	}
	summary, err := s.summarize(ctx, c.CompactText, history)
	if err != nil {
		return "", err
	}
	lastID := uint(0)
	if len(history) > 0 {
		lastID = history[len(history)-1].ID
	}
	if err := s.db.WithContext(ctx).Model(&Conversation{}).
		Where("id = ?", c.ID).
		Updates(map[string]any{
			"compact_text":     summary,
			"compact_after_id": lastID,
		}).Error; err != nil {
		return "", err
	}
	c.CompactText, c.CompactAfterID = summary, lastID
	return summary, nil
}

// summarize 把既有摘要 + 一段历史压成新摘要（≤500 字，滚动合并）。
func (s *ChatService) summarize(ctx context.Context, prior string, part []ChatMessage) (string, error) {
	var sb strings.Builder
	if prior != "" {
		sb.WriteString("【既有摘要】\n")
		sb.WriteString(prior)
		sb.WriteString("\n\n【待并入的新历史】\n")
	}
	for _, h := range part {
		content := h.Content
		if len(content) > 500 {
			content = content[:500] + "…"
		}
		who := "用户"
		if h.Role == "assistant" {
			who = "助手"
		}
		sb.WriteString(who + "：" + content + "\n")
	}
	sys := "你是运维平台对话的压缩器。把给定的对话历史（可能含既有摘要）压缩成不超过 500 字的「前情提要」：" +
		"保留用户的运维诉求、关键事实（主机/项目/错误信息）、已给出的结论与未决事项；" +
		"丢弃寒暄与重复。只输出摘要正文，不要任何前后缀说明。"
	out, err := s.relay.Complete(ctx, "compact",
		[]Message{{Role: "system", Content: sys}, {Role: "user", Content: sb.String()}}, 800)
	if err != nil {
		return "", err
	}
	out = strings.TrimSpace(out)
	if len(out) > compactMaxSummary {
		out = out[:compactMaxSummary] + "…"
	}
	if out == "" {
		return "", fmt.Errorf("压缩结果为空")
	}
	return out, nil
}

// rebuildWithCompact 压缩后重建 msgs：system 尾部并前情提要，去掉被压缩的
// 早期消息（它们已被摘要替代）。
func rebuildWithCompact(msgs []Message, summary string) []Message {
	if len(msgs) == 0 {
		return msgs
	}
	sys, _ := msgs[0].Content.(string)
	sys += "\n\n【前情提要（此前对话的压缩摘要）】\n" + summary
	out := []Message{{Role: "system", Content: sys}}
	// 保留 system 之后的最近 compactKeepN 条（msgs 尾部）
	if len(msgs) > compactKeepN+1 {
		out = append(out, msgs[len(msgs)-compactKeepN:]...)
	} else {
		out = append(out, msgs[1:]...)
	}
	return out
}

// truncateMessages 硬截断降级：从最早的用户轮开始丢，直到估算回到预算内。
func truncateMessages(msgs []Message, budget int) []Message {
	if len(msgs) <= 1 || estimateTokens(msgs) <= budget {
		return msgs
	}
	for len(msgs) > 3 && estimateTokens(msgs) > budget {
		msgs = append([]Message{msgs[0]}, msgs[2:]...) // 丢 msgs[1]（最早一条）
	}
	return msgs
}
