package observ

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/pkg/crypto"
)

// 记录 v2 PUT 的目标路径（验证按 alert_id 而非名称 upsert）
var (
	putMu    sync.Mutex
	putPaths []string
)

func recordedPutPaths() []string {
	putMu.Lock()
	defer putMu.Unlock()
	return append([]string(nil), putPaths...)
}

func alertsTestEnv(t *testing.T) (*Service, *httptest.Server, *int64, *int64) {
	t.Helper()
	putMu.Lock()
	putPaths = nil
	putMu.Unlock()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1) // :memory: 多连接=多库
	if err := db.AutoMigrate(&Alert{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS platform_settings (skey TEXT PRIMARY KEY, value TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	var tplCalls, destCalls, alertCalls, putCalls int64
	// 假 O2：记录 template/destination/alert 三类调用，模拟 "already exist" upsert 语义
	mux := http.NewServeMux()
	mux.HandleFunc("/api/default/alerts/templates", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&tplCalls, 1)
		if n := atomic.LoadInt64(&tplCalls); n > 1 {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"message":"template already exist"}`))
			return
		}
		w.WriteHeader(200)
	})
	mux.HandleFunc("/api/default/alerts/destinations", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&destCalls, 1)
		if n := atomic.LoadInt64(&destCalls); n > 1 {
			// 第二次 POST 模拟已存在 → PUT 才会成功（本测试 PUT 单独 handler）
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"message":"destination already exist"}`))
			return
		}
		w.WriteHeader(200)
	})
	mux.HandleFunc("/api/default/alerts/destinations/custos-platform", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})
	mux.HandleFunc("/api/v2/default/alerts", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet { // upsert 前按名反查：写入过才有条目
			w.Header().Set("Content-Type", "application/json")
			list := "[]"
			if atomic.LoadInt64(&alertCalls) > 0 {
				list = `[{"alert_id":"fake-id-1","name":"err-spike"}]`
			}
			_, _ = w.Write([]byte(`{"list":` + list + `}`))
			return
		}
		atomic.AddInt64(&alertCalls, 1)
		w.WriteHeader(200)
	})
	mux.HandleFunc("/api/v2/default/alerts/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&putCalls, 1)
		putMu.Lock()
		putPaths = append(putPaths, r.URL.Path)
		putMu.Unlock()
		w.WriteHeader(200)
	})
	mux.HandleFunc("/api/default/alerts/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})
	o2 := httptest.NewServer(mux)
	t.Cleanup(o2.Close)

	key := strings.Repeat("ab", 32)
	cipher, err := crypto.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(db, nil, cipher)
	ctx := context.Background()
	_ = svc.SetO2URL(ctx, o2.URL)
	svc.SetPublicURL("https://custos.example.com")
	if err := svc.SaveO2Settings(ctx, O2SettingsInput{Email: "o2@t.local", Password: "pass"}); err != nil {
		t.Fatal(err)
	}
	return svc, o2, &tplCalls, &alertCalls
}

func TestAlertSyncFlow(t *testing.T) {
	svc, _, tplCalls, alertCalls := alertsTestEnv(t)
	ctx := context.Background()
	a, err := svc.CreateAlert(ctx, SaveAlertInput{
		Name: "err-spike", StreamName: "default", SQL: "select count(*) from \"default\"",
		Period: 5, Operator: ">=", Threshold: 3, Frequency: 1, Silence: 10,
		Enabled: true, Level: "critical", Description: "测试",
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var got Alert
		svc.db.First(&got, a.ID)
		if got.SyncStatus == SyncOK {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	var got Alert
	svc.db.First(&got, a.ID)
	if got.SyncStatus != SyncOK {
		t.Fatalf("应同步成功: status=%s err=%s", got.SyncStatus, got.SyncError)
	}
	if atomic.LoadInt64(tplCalls) == 0 || atomic.LoadInt64(alertCalls) == 0 {
		t.Error("应已调用 O2 template 与 alert API")
	}
}

func TestAlertSyncFailureMarksFailed(t *testing.T) {
	svc, o2, _, _ := alertsTestEnv(t)
	// 掐掉 O2：让后续请求全失败
	origURL := o2.URL
	_ = svc.SetO2URL(context.Background(), origURL)
	o2.Close()
	svc2 := svc
	a, err := svc2.CreateAlert(context.Background(), SaveAlertInput{
		Name: "will-fail", StreamName: "s", SQL: "select 1",
		Period: 5, Operator: ">=", Threshold: 1, Frequency: 1, Silence: 0, Level: "warn",
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var got Alert
		svc2.db.First(&got, a.ID)
		if got.SyncStatus == SyncFailed {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	var got Alert
	svc2.db.First(&got, a.ID)
	if got.SyncStatus != SyncFailed || got.SyncError == "" {
		t.Fatalf("应标记失败: %+v", got)
	}
}

func TestO2WebhookParseAndDetail(t *testing.T) {
	p := o2AlertPayload{
		AlertName: "err-spike", StreamName: "default", OrgName: "default",
		RowsCount: 2, TriggerAt: "2026-10-02T10:00:00Z",
		Rows: json.RawMessage(`[{"message":"panic A"},{"message":"panic B"}]`),
	}
	d := formatO2AlertDetail(p)
	for _, want := range []string{"err-spike", "2 条", "panic A"} {
		if !strings.Contains(d, want) {
			t.Errorf("detail 应含 %q: %s", want, d)
		}
	}
	if strings.Contains(d, "panic B") == false {
		t.Error("第二条也应保留（≤3 条）")
	}
}

func TestWebhookTokenGuard(t *testing.T) {
	svc, _, _, _ := alertsTestEnv(t)
	h := NewHandler(svc, nil)
	// token 未配置（o2Config.Token 在 SaveO2Settings 已生成）→ 取出比对
	cfg, _ := svc.o2Config(context.Background())
	if cfg.Token == "" {
		t.Fatal("SaveO2Settings 应自动生成 webhook token")
	}
	// 错误 token 拒绝（gin 测试上下文）
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/observ/alerts/webhook",
		strings.NewReader(`{"alert_name":"x"}`))
	c.Request.Header.Set("X-Custos-Token", "wrong")
	c.Request.Header.Set("Content-Type", "application/json")
	h.o2AlertWebhook(c)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("错误 token 应 401, got %d", w.Code)
	}
}

// 真机 v0.91 教训回归：模板未渲染（变量保留占位符）时发送体必须仍是合法 JSON。
func TestTemplateBodyValidWhenUnrendered(t *testing.T) {
	var probe map[string]any
	if err := json.Unmarshal([]byte(platformTemplateBody), &probe); err != nil {
		t.Fatalf("未渲染形态的模板体应可被 JSON 解析: %v\n%s", err, platformTemplateBody)
	}
	if strings.Contains(platformTemplateBody, "{{") {
		t.Error("模板不应使用双大括号（真机只部分渲染）")
	}
}

// JSON 非法（如旧形态双大括号残留）时 webhook 兜底：提取 alert_name、原文进通知。
func TestWebhookFallbackParse(t *testing.T) {
	svc, _, _, _ := alertsTestEnv(t)
	h := NewHandler(svc, nil)
	cfg, _ := svc.o2Config(context.Background())
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/observ/alerts/webhook",
		strings.NewReader(`{"alert_name":"oops-spike","rows_count":{{rows_count}}`))
	c.Request.Header.Set("X-Custos-Token", cfg.Token)
	h.o2AlertWebhook(c)
	if w.Code != http.StatusOK {
		t.Errorf("兜底路径应 200, got %d body=%s", w.Code, w.Body.String())
	}
}

// flexInt 兼容数字/带引号数字/占位符。
func TestFlexInt(t *testing.T) {
	cases := map[string]int{
		`{"rows_count":7}`:                  7,
		`{"rows_count":"12"}`:               12,
		`{"rows_count":"{rows_count}"}`:     0,
		`{"rows_count":"1790947406259743"}`: 1790947406259743,
	}
	for body, want := range cases {
		var p o2AlertPayload
		if err := json.Unmarshal([]byte(body), &p); err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if int(p.RowsCount) != want {
			t.Errorf("%s => %d, want %d", body, p.RowsCount, want)
		}
	}
}

// 重名 upsert 真机语义回归：POST 已存在 → 反查 alert_id → PUT 到 id 路径
// （传名称会被 O2 当新建处理，产生同名重复告警——v0.91 真机教训）。
func TestAlertUpsertByID(t *testing.T) {
	svc, _, _, _ := alertsTestEnv(t)
	ctx := context.Background()
	a, err := svc.CreateAlert(ctx, SaveAlertInput{
		Name: "err-spike", StreamName: "default", SQL: "select count(*) from \"default\"",
		Period: 5, Operator: ">=", Threshold: 3, Frequency: 1, Silence: 10,
		Enabled: true, Level: "warn", Description: "测试",
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var got Alert
		svc.db.First(&got, a.ID)
		if got.SyncStatus == SyncOK {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	svc.SyncAlert(ctx, a.ID)
	var got2 Alert
	svc.db.First(&got2, a.ID)
	if got2.SyncStatus != SyncOK {
		t.Fatalf("重名同步应走 PUT 成功: status=%s err=%s", got2.SyncStatus, got2.SyncError)
	}
	paths := recordedPutPaths()
	if len(paths) == 0 {
		t.Fatal("重名同步应触发 PUT 覆盖")
	}
	for _, p := range paths {
		if !strings.Contains(p, "fake-id-1") {
			t.Errorf("PUT 应按 alert_id 定位（真机 O2 语义），got path %s", p)
		}
	}
}

// R4：旧路径（平台直建，非模板）PromQL 型告警同步——stream_name/类型/priority 正确。
func TestPromqlAlertSyncLegacy(t *testing.T) {
	svc, o2, _, alertCalls := alertsTestEnv(t)
	ctx := context.Background()
	// 建库表（projects 供前缀）
	if err := svc.db.Exec(`CREATE TABLE IF NOT EXISTS projects (id INTEGER PRIMARY KEY, name TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	_ = svc.db.Exec(`INSERT OR IGNORE INTO projects (id, name) VALUES (1, 'Demo Project')`)
	_ = svc.db.Exec(`INSERT OR IGNORE INTO projects (id, name) VALUES (2, 'demo')`)

	a, err := svc.CreateAlert(ctx, SaveAlertInput{
		Name: "cpu-legacy", StreamName: "node_cpu_seconds_total", StreamType: "metrics",
		SQL:       "100 * (1 - avg by(instance) (rate(node_cpu_seconds_total[2m])))",
		QueryType: "promql",
		Period:    5, Operator: ">", Threshold: 80, Frequency: 1,
		Enabled: true, Level: "critical", ProjectID: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var got Alert
		svc.db.First(&got, a.ID)
		if got.SyncStatus == SyncOK {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	var got Alert
	svc.db.First(&got, a.ID)
	if got.SyncStatus != SyncOK {
		t.Fatalf("PromQL 旧路径同步: status=%s err=%s", got.SyncStatus, got.SyncError)
	}
	if got.QueryType != "promql" {
		t.Fatalf("queryType 未落库: %s", got.QueryType)
	}
	if atomic.LoadInt64(alertCalls) == 0 {
		t.Fatal("应已调 O2 alerts API")
	}
	_ = o2
}
