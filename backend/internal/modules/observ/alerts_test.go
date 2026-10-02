package observ

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/pkg/crypto"
)

func alertsTestEnv(t *testing.T) (*Service, *httptest.Server, *int64, *int64) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1) // :memory: 多连接=多库
	if err := db.AutoMigrate(&Alert{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS platform_settings (key TEXT PRIMARY KEY, value TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	var tplCalls, destCalls, alertCalls int64
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
	mux.HandleFunc("/api/default/alerts", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&alertCalls, 1)
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
	h := NewHandler(svc)
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
