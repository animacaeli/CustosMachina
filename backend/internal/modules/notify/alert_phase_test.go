// alert_phase_test.go P7-M2：notify 两阶段（先析后发/先发后补/自激防护）单测。
package notify

import (
	"context"
	"strings"
	"testing"
	"time"
)

// stubAnalyzer 记录调用行为的分析器桩。
type stubAnalyzer struct {
	analysis   string
	ok         bool
	analyzed   []string
	followups  []string
	blockedFor time.Duration // >0 时模拟分析耗时
}

func (s *stubAnalyzer) AnalyzeAlert(ctx context.Context, source, title, detail string) (string, bool) {
	s.analyzed = append(s.analyzed, source)
	if s.blockedFor > 0 {
		select {
		case <-time.After(s.blockedFor):
		case <-ctx.Done(): // 60s 预算超时 → 降级
			return "", false
		}
	}
	return s.analysis, s.ok
}

func (s *stubAnalyzer) NotifyFollowup(ctx context.Context, n EventNotifier, source, title, detail string) {
	s.followups = append(s.followups, source)
}

func TestNotifyEvent_WarnAnalyzeFirst(t *testing.T) {
	env := newRuleTestEnv(t)
	env.svc.SetOpsGroup(t.Context(), env.group.ID)
	st := &stubAnalyzer{analysis: "疑似 commit abc 引入", ok: true}
	env.svc.SetAlertAnalyzer(st)

	env.svc.NotifyEvent(t.Context(), SourceCronFailed, LevelWarn, "k1", "任务失败", "输出超时")
	if got := env.deliveryCount(); got != 1 {
		t.Fatalf("warn 分析成功应投递一次，got %d", got)
	}
	if len(st.analyzed) != 1 || st.analyzed[0] != SourceCronFailed {
		t.Fatalf("分析源不符: %v", st.analyzed)
	}
	last := env.lastDelivery()
	if !strings.Contains(last, "AI 分析") || !strings.Contains(last, "疑似 commit abc 引入") {
		t.Errorf("投递正文应含分析段，实际: %q", last)
	}
	if strings.Contains(last, "【AI") && !strings.Contains(last, "输出超时") {
		t.Error("原始告警内容应保留")
	}
}

func TestNotifyEvent_AnalyzeTimeoutDegrades(t *testing.T) {
	env := newRuleTestEnv(t)
	env.svc.SetOpsGroup(t.Context(), env.group.ID)
	// 预算缩短到 500ms（生产恒 60s；此处只验证"慢不阻塞"语义本身）
	old := alertAnalyzeBudget
	alertAnalyzeBudget = 500 * time.Millisecond
	t.Cleanup(func() { alertAnalyzeBudget = old })
	st := &stubAnalyzer{blockedFor: 30 * time.Second}
	env.svc.SetAlertAnalyzer(st)

	start := time.Now()
	env.svc.NotifyEvent(t.Context(), SourceBackupFailed, LevelWarn, "k2", "备份失败", "d")
	elapsed := time.Since(start)
	if elapsed > alertAnalyzeBudget+2*time.Second {
		t.Fatalf("分析超时应被预算截断，实际耗时 %v", elapsed)
	}
	if got := env.deliveryCount(); got != 1 {
		t.Fatalf("超时降级应原样投递一次，got %d", got)
	}
	last := env.lastDelivery()
	if strings.Contains(last, "AI 分析") {
		t.Error("超时降级投递不应含分析段")
	}
}

func TestNotifyEvent_CriticalSendFirstFollowupAfter(t *testing.T) {
	env := newRuleTestEnv(t)
	env.svc.SetOpsGroup(t.Context(), env.group.ID)
	st := &stubAnalyzer{analysis: "根因……", ok: true}
	env.svc.SetAlertAnalyzer(st)

	start := time.Now()
	env.svc.NotifyEvent(t.Context(), SourceO2Alert, LevelCritical, "k3", "服务不可用", "5xx 突增")
	elapsed := time.Since(start)
	if elapsed > 2*time.Second {
		t.Fatalf("critical 应立即投递（不等分析），实际耗时 %v", elapsed)
	}
	if got := env.deliveryCount(); got != 1 {
		t.Fatalf("critical 原始告警应投递一次，got %d", got)
	}
	if strings.Contains(env.lastDelivery(), "AI 分析") {
		t.Error("critical 首条不应含分析段（先发再分析）")
	}
	// followup 异步触发（goroutine，稍等）
	deadline := time.Now().Add(2 * time.Second)
	for len(st.followups) == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if len(st.followups) == 0 {
		t.Fatal("critical 应异步触发补发分析")
	}
}

func TestNotifyEvent_SelfExcitationGuard(t *testing.T) {
	env := newRuleTestEnv(t)
	env.svc.SetOpsGroup(t.Context(), env.group.ID)
	st := &stubAnalyzer{analysis: "x", ok: true}
	env.svc.SetAlertAnalyzer(st)

	// ai_digest（补发事件）与 business（业务自判）不进分析
	env.svc.NotifyEvent(t.Context(), SourceAIDigest, LevelInfo, "k4", "AI 分析", "d")
	env.svc.NotifyEvent(t.Context(), SourceBusiness, LevelWarn, "k5", "[app] t", "d")
	if len(st.analyzed) != 0 {
		t.Fatalf("ai_digest/business 不应触发分析: %v", st.analyzed)
	}
	if got := env.deliveryCount(); got != 2 {
		t.Fatalf("两事件应各投递一次，got %d", got)
	}
}

func TestNotifyEvent_NoAnalyzerUnchanged(t *testing.T) {
	env := newRuleTestEnv(t)
	env.svc.SetOpsGroup(t.Context(), env.group.ID)
	// 未注入分析器 → 直推语义
	env.svc.NotifyEvent(t.Context(), SourceCronFailed, LevelWarn, "k6", "t", "d")
	if got := env.deliveryCount(); got != 1 {
		t.Fatalf("无分析器应直推，got %d", got)
	}
}

func TestAlertAnalyzeBudgetValue(t *testing.T) {
	if alertAnalyzeBudget != 60*time.Second {
		t.Fatalf("设计定档 60s，实际 %v", alertAnalyzeBudget)
	}
}
