package notify

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func rulesTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	if err := db.AutoMigrate(&Group{}, &SendRecord{}, &Rule{}, &BusinessToken{}, &identitySettingTable{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	return db
}

func TestSilenced(t *testing.T) {
	at := func(h, m int) time.Time {
		return time.Date(2026, 10, 2, h, m, 0, 0, time.Local)
	}
	r := Rule{SilentStart: "22:00", SilentEnd: "06:00"}
	cases := []struct {
		h, m  int
		want  bool
		level string
	}{
		{23, 0, true, LevelWarn},      // 区间内
		{2, 30, true, LevelWarn},      // 跨零点后仍在区间
		{12, 0, false, LevelWarn},     // 区间外
		{21, 59, false, LevelWarn},    // 边界前一分钟
		{23, 0, false, LevelCritical}, // critical 穿透静默
	}
	for _, c := range cases {
		if got := silenced(r, c.level, at(c.h, c.m)); got != c.want {
			t.Errorf("silenced(%02d:%02d, %s) = %v, want %v", c.h, c.m, c.level, got, c.want)
		}
	}
	// 正向区间
	r2 := Rule{SilentStart: "01:00", SilentEnd: "03:00"}
	if !silenced(r2, LevelInfo, at(2, 0)) || silenced(r2, LevelInfo, at(4, 0)) {
		t.Error("正向静默区间判断错误")
	}
	// 无静默配置
	if silenced(Rule{}, LevelInfo, at(2, 0)) {
		t.Error("未配置静默时段不应静默")
	}
}

func TestRuleValidation(t *testing.T) {
	svc := NewService(rulesTestDB(t), nil)
	ctx := context.Background()
	g := Group{Name: "g", Scope: ScopeProd, Webhook: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=x"}
	if err := svc.db.Create(&g).Error; err != nil {
		t.Fatal(err)
	}
	bad := []SaveRuleInput{
		{Name: "x", Source: "nonexistent", MinLevel: LevelWarn, GroupID: g.ID},
		{Name: "x", Source: SourceCronFailed, MinLevel: LevelWarn, GroupID: 9999},
		{Name: "x", Source: SourceCronFailed, MinLevel: LevelWarn, GroupID: g.ID, SilentStart: "22:00"},
		{Name: "x", Source: SourceCronFailed, MinLevel: LevelWarn, GroupID: g.ID, SilentStart: "25:00", SilentEnd: "06:00"},
	}
	for i, in := range bad {
		if _, err := svc.CreateRule(ctx, in); err == nil {
			t.Errorf("case %d 应校验失败", i)
		}
	}
	r, err := svc.CreateRule(ctx, SaveRuleInput{
		Name: "cron→prod", Source: SourceCronFailed, MinLevel: LevelWarn,
		GroupID: g.ID, Enabled: true,
	})
	if err != nil {
		t.Fatalf("合法规则应通过: %v", err)
	}
	if r.ID == 0 {
		t.Error("应落库拿到 ID")
	}
}

// fakeSender 拦截投递（绕过真实 webhook）：直接向 SendRecord 插 ok 行留痕计数。
type ruleTestEnv struct {
	svc   *Service
	db    *gorm.DB
	group Group
}

func newRuleTestEnv(t *testing.T) *ruleTestEnv {
	t.Helper()
	db := rulesTestDB(t)
	svc := NewService(db, nil)
	g := Group{Name: "g", Scope: ScopeProd, Webhook: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=x"}
	if err := db.Create(&g).Error; err != nil {
		t.Fatal(err)
	}
	return &ruleTestEnv{svc: svc, db: db, group: g}
}

// lastDelivery 最新一条留痕的投递正文（两阶段断言分析段用）。
func (e *ruleTestEnv) lastDelivery() string {
	var r SendRecord
	if err := e.db.Order("id DESC").First(&r).Error; err != nil {
		return ""
	}
	return r.Content
}

// deliveries 读发送留痕（真实 Send 会因假 webhook 网络失败落 failed 行，
// 仍可证明"走没走到投递"这一层；聚合内容断言用标题行数近似）。
func (e *ruleTestEnv) deliveryCount() int {
	var n int64
	e.db.Model(&SendRecord{}).Count(&n)
	return int(n)
}

func TestNotifyEvent_MatchAndMinLevel(t *testing.T) {
	env := newRuleTestEnv(t)
	ctx := context.Background()
	if _, err := env.svc.CreateRule(ctx, SaveRuleInput{
		Name: "仅 critical", Source: SourceO2Alert, MinLevel: LevelCritical, GroupID: env.group.ID, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	before := env.deliveryCount()
	// warn < minLevel(critical)：无匹配规则 → 走 ops 兜底（未配置 ops 群则无投递）
	env.svc.NotifyEvent(ctx, SourceO2Alert, LevelWarn, "k", "t", "d")
	if got := env.deliveryCount(); got != before {
		t.Errorf("低于 minLevel 且无 ops 群时不应投递，before=%d after=%d", before, got)
	}
	// critical 命中规则 → 尝试投递（失败留痕 failed 行）
	env.svc.NotifyEvent(ctx, SourceO2Alert, LevelCritical, "k", "t", "d")
	if got := env.deliveryCount(); got != before+1 {
		t.Errorf("critical 应投递一次，before=%d after=%d", before, got)
	}
}

func TestNotifyEvent_Aggregate(t *testing.T) {
	env := newRuleTestEnv(t)
	ctx := context.Background()
	if _, err := env.svc.CreateRule(ctx, SaveRuleInput{
		Name: "聚合5秒", Source: SourceCronFailed, MinLevel: LevelWarn,
		GroupID: env.group.ID, AggregateSec: 5, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	// 窗口内 3 条同类事件
	for i := 0; i < 3; i++ {
		env.svc.NotifyEvent(ctx, SourceCronFailed, LevelWarn, "job-1", "任务失败", fmt.Sprintf("第%d条", i+1))
	}
	// 未到窗口：仅缓冲，无投递
	if got := env.deliveryCount(); got != 0 {
		t.Fatalf("聚合窗口内不应投递，got %d", got)
	}
	// 窗口内条目数应为 1（同 dedupKey 合并）
	env.svc.agg.mu.Lock()
	n := len(env.svc.agg.items)
	env.svc.agg.mu.Unlock()
	if n != 1 {
		t.Fatalf("聚合缓冲应合并为 1 项，got %d", n)
	}
	// 人工把 deadline 拨到过期再 flush，应只发 1 条
	env.svc.agg.mu.Lock()
	for _, it := range env.svc.agg.items {
		it.deadline = time.Now().Add(-time.Second)
	}
	env.svc.agg.mu.Unlock()
	env.svc.flushDue()
	if got := env.deliveryCount(); got != 1 {
		t.Errorf("聚合到期应合并投递 1 条，got %d", got)
	}
	var rec SendRecord
	env.db.First(&rec)
	if rec.Title != "任务失败" {
		t.Errorf("聚合投递标题不符: %q", rec.Title)
	}
}

func TestNotifyEvent_FallbackOps(t *testing.T) {
	env := newRuleTestEnv(t)
	ctx := context.Background()
	// 配置 ops 群 = 同一群
	if err := env.svc.SetOpsGroup(ctx, env.group.ID); err != nil {
		t.Fatal(err)
	}
	// 无任何规则：事件应走 ops 兜底
	env.svc.NotifyEvent(ctx, SourceBackupFailed, LevelWarn, "k", "备份失败", "d")
	if got := env.deliveryCount(); got != 1 {
		t.Errorf("无规则应走 ops 兜底投递一次，got %d", got)
	}
}
