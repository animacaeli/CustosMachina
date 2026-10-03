package configs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type fakeExec struct {
	written   map[uint]map[string]string // serverID -> path -> content
	commands  []string
	events    []string
	failWrite bool
}

func newFakeExec() *fakeExec { return &fakeExec{written: map[uint]map[string]string{}} }

func (f *fakeExec) SftpWrite(serverID uint, name string, content []byte) error {
	if f.failWrite {
		return errors.New("sftp down")
	}
	if f.written[serverID] == nil {
		f.written[serverID] = map[string]string{}
	}
	f.written[serverID][name] = string(content)
	return nil
}

func (f *fakeExec) RunCommandOn(_ context.Context, _ uint, cmd, _ string, _ time.Duration) (string, error) {
	f.commands = append(f.commands, cmd)
	return "", nil
}

func (f *fakeExec) RecordEvent(_ context.Context, _ uint, typ, msg string) {
	f.events = append(f.events, typ+": "+msg)
}

func testSvc(t *testing.T) (*Service, *gorm.DB, *fakeExec) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&File{}, &Version{}); err != nil {
		t.Fatal(err)
	}
	exec := newFakeExec()
	return NewService(db, exec, nil), db, exec
}

func TestMaskSensitive(t *testing.T) {
	in := `# 注释保留
server:
  host: db.prod.internal
  password: s3cret-token
PORT=8080
API_KEY=abcd1234
plain line
---`
	out := MaskSensitive(in)
	for _, keep := range []string{"host: ***", "password: ***", "PORT=***", "API_KEY=***", "# 注释保留", "plain line", "---"} {
		if !strings.Contains(out, keep) {
			t.Errorf("脱敏输出应含 %q:\n%s", keep, out)
		}
	}
	if strings.Contains(out, "s3cret") || strings.Contains(out, "abcd1234") || strings.Contains(out, "db.prod") {
		t.Errorf("敏感值泄漏:\n%s", out)
	}
}

func TestLifecycleAndVersions(t *testing.T) {
	svc, _, exec := testSvc(t)
	ctx := context.Background()
	f, err := svc.Create(ctx, SaveFileInput{
		Name: "app 配置", ServerID: 1, Path: "/etc/app/config.yaml", Format: FormatYAML,
		Content: "k: v1\n", ApplyAction: ApplyNone,
	}, "op-a")
	if err != nil {
		t.Fatal(err)
	}
	// 编辑 v2
	if err := svc.SaveContent(ctx, f.ID, "k: v2\n", "op-b"); err != nil {
		t.Fatal(err)
	}
	vs, _ := svc.ListVersions(ctx, f.ID)
	if len(vs) != 2 {
		t.Fatalf("应有 2 个版本, got %d", len(vs))
	}
	// 下发（v2 内容落盘 + deploy 快照）
	if err := svc.Deploy(ctx, f.ID, "op-b"); err != nil {
		t.Fatal(err)
	}
	if got := exec.written[1]["/etc/app/config.yaml"]; got != "k: v2\n" {
		t.Errorf("落盘内容不符: %q", got)
	}
	// 回滚到 v1
	if err := svc.Rollback(ctx, f.ID, vs[1].ID, "op-a"); err != nil {
		t.Fatal(err)
	}
	content, _, err := svc.GetContent(ctx, f.ID, false, "x")
	if err != nil || content != "k: v1\n" {
		t.Fatalf("回滚后内容应为 v1: %q %v", content, err)
	}
	// 审计事件：config_deploy 至少一条
	found := false
	for _, e := range exec.events {
		if strings.Contains(e, "config_deploy") {
			found = true
		}
	}
	if !found {
		t.Error("下发应落审计事件")
	}
	// 版本链：edit/deploy/rollback
	vs2, _ := svc.ListVersions(ctx, f.ID)
	if len(vs2) != 4 { // edit v1, edit v2, deploy, rollback
		t.Errorf("版本链数量不符: %d", len(vs2))
	}
}

func TestSensitiveMaskAndReveal(t *testing.T) {
	svc, _, exec := testSvc(t)
	ctx := context.Background()
	f, err := svc.Create(ctx, SaveFileInput{
		Name: "env", ServerID: 1, Path: "/etc/app/.env", Format: FormatEnv,
		Sensitive: true, Content: "DB_PASS=topsecret\n", ApplyAction: ApplyNone,
	}, "op")
	if err != nil {
		t.Fatal(err)
	}
	masked, _, _ := svc.GetContent(ctx, f.ID, false, "x")
	if strings.TrimSpace(masked) != "DB_PASS=***" {
		t.Errorf("默认应脱敏: %q", masked)
	}
	if len(exec.events) != 0 {
		t.Error("未 reveal 不应落审计")
	}
	clear, _, _ := svc.GetContent(ctx, f.ID, true, "op-x")
	if clear != "DB_PASS=topsecret\n" {
		t.Errorf("reveal 应返回明文: %q", clear)
	}
	if len(exec.events) != 1 || !strings.Contains(exec.events[0], "op-x") {
		t.Errorf("reveal 应落审计: %v", exec.events)
	}
}

func TestApplyActions(t *testing.T) {
	svc, _, exec := testSvc(t)
	ctx := context.Background()
	// SIGHUP
	f1, _ := svc.Create(ctx, SaveFileInput{Name: "a", ServerID: 1, Path: "/x/a.conf",
		Format: FormatINI, Content: "k=1\n", ApplyAction: ApplySighup, ApplyTarget: "myapp"}, "op")
	if err := svc.Deploy(ctx, f1.ID, "op"); err != nil {
		t.Fatal(err)
	}
	if len(exec.commands) != 1 || !strings.Contains(exec.commands[0], "pkill -HUP") {
		t.Errorf("应执行 SIGHUP: %v", exec.commands)
	}
	// restart
	f2, _ := svc.Create(ctx, SaveFileInput{Name: "b", ServerID: 1, Path: "/x/b.yaml",
		Format: FormatYAML, Content: "k: 1\n", ApplyAction: ApplyRestart, ApplyTarget: "myapp"}, "op")
	if err := svc.Deploy(ctx, f2.ID, "op"); err != nil {
		t.Fatal(err)
	}
	if len(exec.commands) != 2 || !strings.Contains(exec.commands[1], "docker restart myapp") {
		t.Errorf("应执行 docker restart: %v", exec.commands)
	}
	// 目标缺失校验
	if _, err := svc.Create(ctx, SaveFileInput{Name: "c", ServerID: 1, Path: "/x/c",
		Format: FormatYAML, ApplyAction: ApplySighup}, "op"); err == nil {
		t.Error("SIGHUP 无目标应拒绝")
	}
	if _, err := svc.Create(ctx, SaveFileInput{Name: "d", ServerID: 1, Path: "/x/d",
		Format: FormatYAML, ApplyAction: ApplyHTTP, ApplyTarget: "not-url"}, "op"); err == nil {
		t.Error("非法 refresh URL 应拒绝")
	}
}

func TestDeployFailureKeepsAudit(t *testing.T) {
	svc, _, exec := testSvc(t)
	exec.failWrite = true
	ctx := context.Background()
	f, _ := svc.Create(ctx, SaveFileInput{Name: "e", ServerID: 1, Path: "/x/e",
		Format: FormatYAML, Content: "k: 1\n", ApplyAction: ApplyNone}, "op")
	if err := svc.Deploy(ctx, f.ID, "op"); err == nil {
		t.Fatal("写入失败应报错")
	}
	if len(exec.events) == 0 {
		t.Error("失败也应落审计")
	}
}

// R2 层级路径强语义：首段环境校验 + 项目环境绑定检查 + 深层自由。
func TestValidateRelPath(t *testing.T) {
	svc, db, _ := testSvc(t)
	ctx := context.Background()
	if err := db.Exec(`CREATE TABLE project_env_targets (id INTEGER PRIMARY KEY, project_id INTEGER, env_type TEXT, server_id INTEGER)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO project_env_targets (project_id, env_type, server_id) VALUES (1, 'prod', 1)`).Error; err != nil {
		t.Fatal(err)
	}
	// 合法：已配置环境 + 深层子目录（槽位）
	if err := svc.validateRelPath(ctx, 1, "prod/application.yaml"); err != nil {
		t.Fatalf("合法路径被拒: %v", err)
	}
	if err := svc.validateRelPath(ctx, 1, "prod/sub/dev1/app.env"); err != nil {
		t.Fatalf("深层路径被拒: %v", err)
	}
	// 空路径（旧形态）放行
	if err := svc.validateRelPath(ctx, 0, ""); err != nil {
		t.Fatalf("空路径应放行: %v", err)
	}
	// 首段非环境
	if err := svc.validateRelPath(ctx, 1, "dev1/app.yaml"); err == nil {
		t.Fatal("首段非环境应拒绝")
	}
	// 项目未配置该环境
	if err := svc.validateRelPath(ctx, 1, "canary/app.yaml"); err == nil {
		t.Fatal("未配置环境应拒绝")
	}
	// 有项目环境但未挂项目
	if err := svc.validateRelPath(ctx, 0, "prod/app.yaml"); err == nil {
		t.Fatal("无项目归属应拒绝")
	}
	// 路径段非法
	if err := svc.validateRelPath(ctx, 1, "prod/../etc/passwd"); err == nil {
		t.Fatal("非法段应拒绝")
	}
	// 创建链路也校验（Create 走 validateRelPath）
	if _, err := svc.Create(ctx, SaveFileInput{
		Name: "x", ServerID: 1, Path: "/opt/x.yaml", Format: "yaml",
		ApplyAction: "none", ProjectID: 1, RelPath: "prod/x.yaml", Content: "a: 1",
	}, "t"); err != nil {
		t.Fatalf("合法创建被拒: %v", err)
	}
	var f File
	if err := db.First(&f).Error; err != nil {
		t.Fatal(err)
	}
	if f.RelPath != "prod/x.yaml" || f.ProjectID != 1 {
		t.Fatalf("字段未落库: %+v", f)
	}
}
