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

// 环境同步：覆盖生成新版本 + 缺失建档（目标主机跟随目标环境）。
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
	// 同 prod → test：新建
	c1, u1, err := svc.EnvSync(ctx, EnvSyncInput{ProjectID: 1, SourceEnv: "prod", TargetEnv: "test"}, "t")
	if err != nil || c1 != 1 || u1 != 0 {
		t.Fatalf("首次同步: c=%d u=%d err=%v", c1, u1, err)
	}
	var dst File
	if err := db.Where("project_id = 1 AND rel_path = ?", "test/app.yaml").First(&dst).Error; err != nil {
		t.Fatal("目标文件未建档", err)
	}
	if dst.ServerID != 20 {
		t.Fatalf("目标主机应跟随目标环境: %d", dst.ServerID)
	}
	if dst.Content != "v: 1" {
		t.Fatalf("内容未同步: %q", dst.Content)
	}
	// 源更新后再同步：覆盖生成新版本
	if err := svc.SaveContent(ctx, src.ID, "v: 2", "t"); err != nil {
		t.Fatal(err)
	}
	c2, u2, err := svc.EnvSync(ctx, EnvSyncInput{ProjectID: 1, SourceEnv: "prod", TargetEnv: "test"}, "t")
	if err != nil || c2 != 0 || u2 != 1 {
		t.Fatalf("二次同步: c=%d u=%d err=%v", c2, u2, err)
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
	// 同环境拒绝 / 未绑定环境拒绝
	if _, _, err := svc.EnvSync(ctx, EnvSyncInput{ProjectID: 1, SourceEnv: "prod", TargetEnv: "prod"}, "t"); err == nil {
		t.Fatal("同环境应拒绝")
	}
	if _, _, err := svc.EnvSync(ctx, EnvSyncInput{ProjectID: 1, SourceEnv: "prod", TargetEnv: "canary"}, "t"); err == nil {
		t.Fatal("未绑定目标环境应拒绝")
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
	if err := db.Exec(`CREATE TABLE projects (id INTEGER PRIMARY KEY, name TEXT)`).Error; err != nil {
		t.Fatalf("建 projects 表失败: %v", err)
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
		File{Name: "a", RelPath: "test/a.env", Path: "/x/a.env", Format: "env", Content: "DB_HOST=localhost\n# 注释\nDB_PORT=5432\n"},
		File{Name: "b", RelPath: "test/b.env", Path: "/x/b.env", Format: "env", Content: "CACHE=redis\n"},
	)
	svc := &Service{db: db}
	out, err := svc.PullConfig(t.Context(), "demo", "test")
	if err != nil {
		t.Fatalf("拉取失败: %v", err)
	}
	body := string(out.Body)
	for _, kv := range []string{"DB_HOST=localhost", "DB_PORT=5432", "CACHE=redis"} {
		if !strings.Contains(body, kv) {
			t.Fatalf("平铺合并缺 %s: %s", kv, body)
		}
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
	out, err := svc.MergedPreview(t.Context(), 1, "prod", "json")
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
	out2, err := svc.MergedPreview(t.Context(), 1, "prod", "yaml")
	if err != nil || !strings.Contains(out2.Body, "feature_x: \"true\"") && !strings.Contains(out2.Body, "feature_x: true") {
		t.Fatalf("yaml 输出异常: %v %s", err, out2.Body)
	}
}
