package cron

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/modules/resources"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	// execute 在后台 goroutine 跑：:memory: 库每连接独立，限制单连接保证同库
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(&CronScript{}, &CronJob{}, &CronRun{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	return db
}

// fakeRunner 记录命令调用，可编程返回结果。
// mu：Trigger 起 execute 后台 goroutine，测试 goroutine 轮询后读记录——
// append/读取跨 goroutine，-race（pre-push 门禁）下必须加锁。
type fakeRunner struct {
	mu        sync.Mutex
	commands  []string
	stdins    []string
	out       string
	err       error
	errOn     string // 非空时：仅命中该关键词的命令返回 err（其余成功）
	events    []string
	blockRuns map[string]bool // 命中关键词的命令直接模拟"仍卡着"（不返回）
}

// snapshotCommands/snapshotEvents 拷贝读取（测试断言用）。
func (f *fakeRunner) snapshotCommands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.commands...)
}

func (f *fakeRunner) snapshotEvents() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}

func (f *fakeRunner) RunCommandOn(_ context.Context, _ uint, cmd, stdin string, _ time.Duration) (string, error) {
	f.mu.Lock()
	f.commands = append(f.commands, cmd)
	f.stdins = append(f.stdins, stdin)
	f.mu.Unlock()
	for kw := range f.blockRuns {
		if strings.Contains(cmd, kw) {
			time.Sleep(2 * time.Second)
			return f.out, f.err
		}
	}
	if f.errOn != "" {
		if strings.Contains(cmd, f.errOn) {
			return f.out, f.err
		}
		return f.out, nil
	}
	return f.out, f.err
}

func (f *fakeRunner) RecordEvent(_ context.Context, _ uint, typ, msg string) {
	f.mu.Lock()
	f.events = append(f.events, typ+":"+msg)
	f.mu.Unlock()
}

func (f *fakeRunner) RunCommandStreamOn(_ context.Context, _ uint, cmd, stdin string, _ time.Duration, onChunk func(string)) (string, error) {
	if onChunk != nil && f.out != "" {
		onChunk(f.out) // 模拟首个输出块
	}
	return f.RunCommandOn(context.Background(), 0, cmd, stdin, 0)
}

func newSvc(t *testing.T) (*Service, *fakeRunner) {
	t.Helper()
	r := &fakeRunner{}
	return NewService(testDB(t), r), r
}

func mustScript(t *testing.T, svc *Service, typ string) *CronScript {
	t.Helper()
	sc, err := svc.SaveScript(context.Background(), 0, ScriptInput{
		Name: "s-" + typ, Type: typ, Content: "echo hi",
	}, "tester")
	if err != nil {
		t.Fatalf("建脚本失败: %v", err)
	}
	return sc
}

// ---- 调度表达式解析 ----

func TestParseSchedule(t *testing.T) {
	cases := []struct {
		expr  string
		ok    bool
		about string
	}{
		{"*/5 * * * *", true, "5 段式"},
		{"0 3 * * *", true, "每日 3 点"},
		{"30 8 * * 1-5", true, "工作日早上"},
		{"@every 1h", false, "@ 描述符不收（只支持标准 crontab）"},
		{"bad expr", false, "非法表达式"},
		{"* * * *", false, "4 段不合法"},
	}
	for _, c := range cases {
		_, err := parseSchedule(c.expr, time.Now())
		if (err == nil) != c.ok {
			t.Errorf("%s（%q）: 期望 ok=%v, got err=%v", c.about, c.expr, c.ok, err)
		}
	}
}

// ---- 脚本类型 × 执行载体矩阵 ----

func TestValidateCarrierMatrix(t *testing.T) {
	cases := []struct {
		scriptType, carrier, image, project, service string
		ok                                           bool
	}{
		{ScriptShell, CarrierRun, "alpine:3", "", "", true},
		{ScriptShell, CarrierRun, "", "", "", false},              // 缺镜像
		{ScriptShell, CarrierCompose, "", "proj", "worker", true}, // shell 走 compose 合法
		{ScriptPython, CarrierRun, "python:3.12", "", "", true},
		{ScriptPython, CarrierCompose, "", "proj", "worker", true},
		{ScriptComposeRun, CarrierCompose, "", "proj", "worker", true},
		{ScriptComposeRun, CarrierRun, "alpine:3", "", "", false}, // compose-run 只能 compose 载体
		{ScriptComposeRun, CarrierCompose, "", "proj", "", false}, // 缺服务名
	}
	for i, c := range cases {
		err := validateCarrier(c.scriptType, c.carrier, c.image, c.project, c.service)
		if (err == nil) != c.ok {
			t.Errorf("case %d: type=%s carrier=%s 期望 ok=%v, got err=%v", i, c.scriptType, c.carrier, c.ok, err)
		}
	}
}

// ---- 任务保存：合法输入推进 next_run_at；非法组合/表达式拒绝 ----

func TestSaveJobValidatesAndSchedules(t *testing.T) {
	svc, _ := newSvc(t)
	sc := mustScript(t, svc, ScriptShell)
	ctx := context.Background()

	in := JobInput{
		Name: "cleanup", ScriptID: sc.ID, Schedule: "0 3 * * *", ServerID: 1,
		Carrier: CarrierRun, Image: "alpine:3", Enabled: boolPtr(true),
	}
	job, err := svc.SaveJob(ctx, 0, in, "tester")
	if err != nil {
		t.Fatalf("保存合法任务失败: %v", err)
	}
	if job.NextRunAt == nil || job.NextRunAt.Before(time.Now()) {
		t.Errorf("启用任务应推进 next_run_at 到未来, got %v", job.NextRunAt)
	}

	// compose-run 脚本 + run 载体：矩阵拒绝
	cr := mustScript(t, svc, ScriptComposeRun)
	_, err = svc.SaveJob(ctx, 0, JobInput{
		Name: "bad", ScriptID: cr.ID, Schedule: "0 3 * * *", ServerID: 1,
		Carrier: CarrierRun, Image: "alpine:3",
	}, "tester")
	if err == nil {
		t.Error("compose-run 脚本配 run 载体应被拒绝")
	}

	// 坏表达式拒绝
	_, err = svc.SaveJob(ctx, 0, JobInput{
		Name: "bad2", ScriptID: sc.ID, Schedule: "not a cron", ServerID: 1,
		Carrier: CarrierRun, Image: "alpine:3",
	}, "tester")
	if err == nil {
		t.Error("非法表达式应被拒绝")
	}

	// 禁用任务 next_run_at 清空
	in2 := in
	in2.Enabled = boolPtr(false)
	disabled, err := svc.SaveJob(ctx, job.ID, in2, "tester")
	if err != nil {
		t.Fatalf("更新任务失败: %v", err)
	}
	if disabled.NextRunAt != nil {
		t.Errorf("禁用任务 next_run_at 应为空, got %v", disabled.NextRunAt)
	}
}

// ---- 脚本删除：被任务绑定时拒绝 ----

func TestDeleteScriptBlockedWhenBound(t *testing.T) {
	svc, _ := newSvc(t)
	sc := mustScript(t, svc, ScriptShell)
	ctx := context.Background()
	if _, err := svc.SaveJob(ctx, 0, JobInput{
		Name: "j", ScriptID: sc.ID, Schedule: "0 3 * * *", ServerID: 1,
		Carrier: CarrierRun, Image: "alpine:3",
	}, "t"); err != nil {
		t.Fatalf("建任务失败: %v", err)
	}
	if err := svc.DeleteScript(ctx, sc.ID); err == nil {
		t.Error("被绑定的脚本删除应被拒绝")
	}
}

// ---- Forbid：上次未结束，手动触发与调度都跳过 ----

func TestForbidSkipsWhenRunning(t *testing.T) {
	svc, r := newSvc(t)
	r.blockRuns = map[string]bool{"docker run": true}
	sc := mustScript(t, svc, ScriptShell)
	ctx := context.Background()
	svc.db.Create(&CronJob{
		Name: "j", ScriptID: sc.ID, Schedule: "0 3 * * *", ServerID: 1,
		Carrier: CarrierRun, Image: "alpine:3", Enabled: true, TimeoutSecs: 600,
	})
	var job CronJob
	svc.db.First(&job)

	// 第一次：落 running 并分发（fake 卡 2s）
	run1, ok := svc.startRun(&job, TriggerManual)
	if !ok {
		t.Fatal("首次应允许执行")
	}
	if run1.Status != RunRunning {
		t.Fatalf("首次运行状态应为 running, got %s", run1.Status)
	}
	// 第二次立即触发：Forbid 拒绝
	if _, err := svc.Trigger(ctx, job.ID, TriggerManual); err == nil {
		t.Error("running 中手动触发应被 Forbid 拒绝")
	}
	// startRun 直接判定
	if _, ok2 := svc.startRun(&job, TriggerManual); ok2 {
		t.Error("running 中再次 startRun 应被 Forbid 拒绝")
	}
}

// ---- 手动触发全链路：上传脚本 → docker run → 成功留痕 + 审计事件 ----

func TestTriggerExecutesAndAudits(t *testing.T) {
	svc, r := newSvc(t)
	r.out = "hello"
	sc := mustScript(t, svc, ScriptShell)
	ctx := context.Background()
	job, err := svc.SaveJob(ctx, 0, JobInput{
		Name: "j", ScriptID: sc.ID, Schedule: "0 3 * * *", ServerID: 7,
		Carrier: CarrierRun, Image: "alpine:3", Command: "--dry-run",
	}, "tester")
	if err != nil {
		t.Fatalf("建任务失败: %v", err)
	}
	run, err := svc.Trigger(ctx, job.ID, TriggerManual)
	if err != nil {
		t.Fatalf("触发失败: %v", err)
	}
	// execute 是异步的，等它落结果
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var got CronRun
		svc.db.First(&got, run.ID)
		if got.Status != RunRunning {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	var got CronRun
	svc.db.First(&got, run.ID)
	if got.Status != RunSuccess {
		t.Fatalf("期望 success, got %s output=%s", got.Status, got.Output)
	}
	if len(r.snapshotCommands()) < 2 {
		t.Fatalf("应有上传脚本+执行两条命令, got %d: %v", len(r.snapshotCommands()), r.commands)
	}
	if !strings.Contains(r.snapshotCommands()[0], "cat > /opt/custos-machina/cron/task-") {
		t.Errorf("第一条命令应是上传脚本, got %q", r.snapshotCommands()[0])
	}
	if r.stdins[0] != "echo hi" {
		t.Errorf("脚本内容应经 stdin 上传, got %q", r.stdins[0])
	}
	if !strings.Contains(r.snapshotCommands()[1], "docker run --rm --label custos.cron.run=") ||
		!strings.Contains(r.snapshotCommands()[1], "alpine:3") ||
		!strings.Contains(r.snapshotCommands()[1], "sh /tmp/cron-task") ||
		!strings.Contains(r.snapshotCommands()[1], "--dry-run") {
		t.Errorf("执行命令不符合预期: %q", r.snapshotCommands()[1])
	}
	if len(r.snapshotEvents()) == 0 || !strings.Contains(r.snapshotEvents()[0], "cron_run") {
		t.Errorf("应落审计事件, got %v", r.events)
	}
}

// ---- compose 载体命令拼装 ----

func TestBuildCommandCompose(t *testing.T) {
	job := &CronJob{Carrier: CarrierCompose, ProjectName: "demo", Service: "migrate"}
	sc := &CronScript{Type: ScriptComposeRun}
	cmd := buildCommand(job, sc, "", "--label custos.cron.run=1", "demo")
	want := "docker compose -p demo -f /opt/custos-machina/compose/demo/compose.yaml run --rm --label custos.cron.run=1 migrate"
	if cmd != want {
		t.Errorf("compose 命令不符\n got %q\nwant %q", cmd, want)
	}
}

// ---- 调度扫描：到点分发、missed 记 skipped 不执行 ----

func TestScanDueDispatchAndMissed(t *testing.T) {
	svc, r := newSvc(t)
	r.out = "ok"
	sc := mustScript(t, svc, ScriptShell)
	ctx := context.Background()

	// 30 分钟前已到点 → missed
	past := time.Now().Add(-30 * time.Minute)
	svc.db.Create(&CronJob{
		Name: "missed", ScriptID: sc.ID, Schedule: "0 3 * * *", ServerID: 1,
		Carrier: CarrierRun, Image: "alpine:3", Enabled: true, NextRunAt: &past,
	})
	if err := svc.scanDue(ctx); err != nil {
		t.Fatalf("scanDue 失败: %v", err)
	}
	var runs []CronRun
	svc.db.Where("job_id = ?", 1).Find(&runs)
	if len(runs) != 1 || runs[0].Status != RunSkipped {
		t.Fatalf("错失应记 skipped 不执行, got %+v", runs)
	}
	if !strings.Contains(runs[0].Output, "跳过不补跑") {
		t.Errorf("skipped 输出应注明跳过: %q", runs[0].Output)
	}
	var job CronJob
	svc.db.First(&job, 1)
	if job.NextRunAt.Before(time.Now()) {
		t.Errorf("missed 后 next_run_at 应推进到未来, got %v", job.NextRunAt)
	}
	if len(r.snapshotCommands()) != 0 {
		t.Errorf("missed 不应有执行命令, got %v", r.commands)
	}

	// 刚到点 → 正常分发执行
	now := time.Now().Add(-5 * time.Second)
	svc.db.Create(&CronJob{
		Name: "due", ScriptID: sc.ID, Schedule: "0 3 * * *", ServerID: 1,
		Carrier: CarrierRun, Image: "alpine:3", Enabled: true, NextRunAt: &now,
	})
	if err := svc.scanDue(ctx); err != nil {
		t.Fatalf("scanDue 失败: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	var got CronRun
	for time.Now().Before(deadline) {
		svc.db.Where("job_id = ?", 2).First(&got)
		if got.Status != "" && got.Status != RunRunning {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got.Status != RunSuccess {
		t.Fatalf("到点任务应执行成功, got %s out=%s", got.Status, got.Output)
	}
}

// ---- 平台重启：悬空 running 标 unknown ----

func TestMarkDanglingUnknown(t *testing.T) {
	svc, _ := newSvc(t)
	svc.db.Create(&CronRun{JobID: 1, Trigger: TriggerSchedule, Status: RunRunning, StartedAt: time.Now()})
	svc.recoverDangling()
	var got CronRun
	svc.db.First(&got, 1)
	if got.Status != RunUnknown {
		t.Fatalf("悬空记录应标 unknown, got %s", got.Status)
	}
}

// ---- 超时强杀链路 ----

func TestExecuteTimeoutKills(t *testing.T) {
	svc, r := newSvc(t)
	r.blockRuns = map[string]bool{"__never__": true} // 不用 block：超时由 errOn 模拟
	r.errOn = "docker run"
	r.err = fmt.Errorf("%w（600ms）", resources.ErrCommandTimeout) // 哨兵包装，与真实 sshRunOutput 一致
	sc := mustScript(t, svc, ScriptShell)
	job := &CronJob{ScriptID: sc.ID, ServerID: 1, Carrier: CarrierRun,
		Image: "alpine:3", TimeoutSecs: 1, Name: "t"}
	run, ok := svc.startRun(job, TriggerManual)
	if !ok {
		t.Fatal("startRun 失败")
	}
	start := time.Now()
	svc.execute(context.Background(), job, run)
	var got CronRun
	svc.db.First(&got, run.ID)
	if got.Status != RunTimeout {
		t.Fatalf("期望 timeout, got %s out=%s", got.Status, got.Output)
	}
	// 强杀命令应已发出（按 label 过滤）
	joined := strings.Join(r.snapshotCommands(), "\n")
	if !strings.Contains(joined, "docker ps -q --filter label=custos.cron.run=") {
		t.Errorf("超时后应按标签强杀, commands=%v", r.commands)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("execute 应在强杀后尽快返回")
	}
}

func boolPtr(b bool) *bool { return &b }

// Command 注入面：shell 元字符拒绝；@ 描述符拒绝；镜像前导 - 拒绝。
func TestCronInputHardening(t *testing.T) {
	svc, _ := newSvc(t)
	sc := mustScript(t, svc, ScriptShell)
	ctx := context.Background()

	for _, badCmd := range []string{"; curl evil | sh", "`id`", "$(id)", "a && b", "a || b"} {
		_, err := svc.SaveJob(ctx, 0, JobInput{
			Name: "j", ScriptID: sc.ID, Schedule: "0 3 * * *", ServerID: 1,
			Carrier: CarrierRun, Image: "alpine:3", Command: badCmd,
		}, "t")
		if err == nil {
			t.Errorf("Command %q 应被拒绝", badCmd)
		}
	}
	// @ 描述符拒绝
	if _, err := parseSchedule("@daily", time.Now()); err == nil {
		t.Error("@daily 应被拒绝（只支持标准 5 段）")
	}
	// 前导 - 镜像拒绝（--privileged 注入）
	_, err := svc.SaveJob(ctx, 0, JobInput{
		Name: "j2", ScriptID: sc.ID, Schedule: "0 3 * * *", ServerID: 1,
		Carrier: CarrierRun, Image: "--privileged",
	}, "t")
	if err == nil {
		t.Error("前导 - 的镜像名应被拒绝")
	}
}

// truncateUTF8 不切碎多字节字符。
func TestTruncateUTF8(t *testing.T) {
	s := strings.Repeat("中", 40*1024) // 120KB 全多字节
	got := truncateUTF8(s, 64*1024)
	if len(got) > 64*1024+64 {
		t.Fatalf("截断后超长: %d", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatal("截断结果必须是合法 UTF-8")
	}
}

// 删除运行中的任务被拒绝。
func TestDeleteJobBlockedWhileRunning(t *testing.T) {
	svc, _ := newSvc(t)
	sc := mustScript(t, svc, ScriptShell)
	svc.db.Create(&CronJob{ScriptID: sc.ID, Schedule: "0 3 * * *", ServerID: 1,
		Carrier: CarrierRun, Image: "alpine:3", Name: "j"})
	svc.db.Create(&CronRun{JobID: 1, Status: RunRunning, StartedAt: time.Now()})
	if err := svc.DeleteJob(context.Background(), 1); err == nil {
		t.Error("运行中的任务删除应被拒绝")
	}
}

// GORM 坑回归：Create 跳过带 default 标签的零值字段——禁用任务建出来必须还是禁用
// （真机测试实测：enabled=false 落库变 true，next_run_at 为 NULL 被调度器立即扫到）。
func TestSaveJobDisabledCreate(t *testing.T) {
	svc, _ := newSvc(t)
	sc := mustScript(t, svc, ScriptShell)
	job, err := svc.SaveJob(context.Background(), 0, JobInput{
		Name: "off", ScriptID: sc.ID, Schedule: "0 3 * * *", ServerID: 1,
		Carrier: CarrierRun, Image: "alpine:3", Enabled: boolPtr(false),
	}, "t")
	if err != nil {
		t.Fatal(err)
	}
	var got CronJob
	svc.db.First(&got, job.ID)
	if got.Enabled {
		t.Fatal("禁用任务落库后变启用（GORM default 零值跳过）")
	}
	if got.NextRunAt != nil {
		t.Fatalf("禁用任务不应推进 next_run_at: %v", got.NextRunAt)
	}
}

// SchedulePreview：未来 N 次触发时间单调递增且符合表达式。
func TestSchedulePreview(t *testing.T) {
	svc, _ := newSvc(t)
	times, err := svc.SchedulePreview("*/5 * * * *", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(times) != 5 {
		t.Fatalf("应返回 5 次, got %d", len(times))
	}
	for i := 1; i < len(times); i++ {
		if !times[i].After(times[i-1]) {
			t.Fatalf("触发时间应递增: %v -> %v", times[i-1], times[i])
		}
		if times[i].Minute()%5 != 0 {
			t.Fatalf("应落在 5 分钟边界: %v", times[i])
		}
	}
	if _, err := svc.SchedulePreview("@every 1h", 5); err == nil {
		t.Error("@ 描述符应被拒绝")
	}
	if _, err := svc.SchedulePreview("bad", 5); err == nil {
		t.Error("非法表达式应被拒绝")
	}
}

// compose 载体域名跟随蓝绿活跃色（DomainResolver 注入后替换，未注入用原名）。
type fakeResolver struct{ m map[string]string }

func (f fakeResolver) ActiveDomainFor(_ context.Context, base string) string {
	if d, ok := f.m[base]; ok {
		return d
	}
	return base // 与真实实现一致：未启用蓝绿返回原名
}

func TestComposeDomainFollowsActiveColor(t *testing.T) {
	svc, _ := newSvc(t)
	sc := mustScript(t, svc, ScriptComposeRun)
	ctx := context.Background()
	job, err := svc.SaveJob(ctx, 0, JobInput{
		Name: "j", ScriptID: sc.ID, Schedule: "0 3 * * *", ServerID: 1,
		Carrier: CarrierCompose, ProjectName: "user-manage-v2", Service: "cron-cleanup",
	}, "t")
	if err != nil {
		t.Fatal(err)
	}
	// 未注入 resolver：原名执行
	if got := svc.composeDomain(ctx, job); got != "user-manage-v2" {
		t.Fatalf("未注入 resolver 应返回原名, got %q", got)
	}
	svc.SetDomainResolver(fakeResolver{m: map[string]string{
		"user-manage-v2": "user-manage-v2-prod-green",
	}})
	if got := svc.composeDomain(ctx, job); got != "user-manage-v2-prod-green" {
		t.Fatalf("应解析为活跃颜色域, got %q", got)
	}
	// 未启用蓝绿的项目原样返回
	svc.db.Create(&CronJob{Name: "x", ScriptID: sc.ID, Schedule: "0 3 * * *", ServerID: 1,
		Carrier: CarrierCompose, ProjectName: "other-proj", Service: "s"})
	var other CronJob
	svc.db.First(&other, "name = ?", "x")
	_ = other
	noop := fakeResolver{m: map[string]string{}}
	if got := noop.ActiveDomainFor(ctx, "other-proj"); got != "other-proj" {
		t.Fatalf("未启用蓝绿应原名, got %q", got)
	}
}

// 仅手动任务：表达式可空、不推进 next_run_at、调度器排除；network 注入校验与拼装。
func TestManualJobAndNetwork(t *testing.T) {
	svc, _ := newSvc(t)
	sc := mustScript(t, svc, ScriptShell)
	ctx := context.Background()

	// 手动任务（schedule 空 + enabled true）
	job, err := svc.SaveJob(ctx, 0, JobInput{
		Name: "oneoff", ScriptID: sc.ID, Schedule: "", ServerID: 1,
		Carrier: CarrierRun, Image: "alpine:3", Enabled: boolPtr(true),
	}, "t")
	if err != nil {
		t.Fatalf("手动任务创建失败: %v", err)
	}
	if job.NextRunAt != nil {
		t.Fatalf("手动任务不应推进 next_run_at: %v", job.NextRunAt)
	}
	// 调度器扫描不产生任何运行记录
	if err := svc.scanDue(ctx); err != nil {
		t.Fatal(err)
	}
	var n int64
	svc.db.Model(&CronRun{}).Where("job_id = ?", job.ID).Count(&n)
	if n != 0 {
		t.Fatalf("手动任务不应被调度执行, got %d 条", n)
	}

	// network 注入拒绝
	_, err = svc.SaveJob(ctx, 0, JobInput{
		Name: "bad", ScriptID: sc.ID, Schedule: "", ServerID: 1,
		Carrier: CarrierRun, Image: "alpine:3", Network: "x; rm -rf /",
	}, "t")
	if err == nil {
		t.Error("network 含 shell 元字符应被拒绝")
	}

	// network 拼装
	cmd := buildCommand(&CronJob{Carrier: CarrierRun, Image: "alpine:3", Network: "demo-prod-blue_default"},
		&CronScript{Type: ScriptShell}, "", "--label L", "")
	if !strings.Contains(cmd, "--network demo-prod-blue_default") {
		t.Errorf("docker run 应带 --network: %q", cmd)
	}
	// 未设 network 不加标志
	cmd2 := buildCommand(&CronJob{Carrier: CarrierRun, Image: "alpine:3"},
		&CronScript{Type: ScriptShell}, "", "--label L", "")
	if strings.Contains(cmd2, "--network") {
		t.Errorf("未设 network 不应加 --network: %q", cmd2)
	}
}

// compose 载体 serverId 留空：按项目正式环境部署目标解析；run 载体留空被拒。
type projTbl struct {
	ID   uint
	Name string
}

func (projTbl) TableName() string { return "projects" }

type envTargetTbl struct {
	ProjectID uint
	EnvType   string
	ServerID  uint
}

func (envTargetTbl) TableName() string { return "project_env_targets" }

func TestResolveServerByProject(t *testing.T) {
	svc, _ := newSvc(t)
	if err := svc.db.AutoMigrate(&projTbl{}, &envTargetTbl{}); err != nil {
		t.Fatal(err)
	}
	svc.db.Create(&projTbl{ID: 2, Name: "demo"})
	svc.db.Create(&envTargetTbl{ProjectID: 2, EnvType: "prod", ServerID: 7})
	ctx := context.Background()

	// 显式绑定优先
	got, err := svc.resolveServer(ctx, &CronJob{ServerID: 3})
	if err != nil || got != 3 {
		t.Fatalf("显式绑定应优先, got %d err %v", got, err)
	}
	// compose 载体按项目解析
	got, err = svc.resolveServer(ctx, &CronJob{Carrier: CarrierCompose, ProjectName: "demo"})
	if err != nil || got != 7 {
		t.Fatalf("项目解析应得 7, got %d err %v", got, err)
	}
	// 项目不存在
	if _, err := svc.resolveServer(ctx, &CronJob{Carrier: CarrierCompose, ProjectName: "nope"}); err == nil {
		t.Error("项目不存在应报错")
	}
	// run 载体不选主机：保存被拒
	sc := mustScript(t, svc, ScriptShell)
	if _, err := svc.SaveJob(ctx, 0, JobInput{
		Name: "j", ScriptID: sc.ID, Schedule: "", ServerID: 0,
		Carrier: CarrierRun, Image: "alpine:3",
	}, "t"); err == nil {
		t.Error("run 载体缺主机应被拒绝")
	}
}
