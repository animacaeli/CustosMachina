package ai

import (
	"context"
	"encoding/json"
	"github.com/custos-machina/backend/internal/modules/notify"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	cryptopkg "github.com/custos-machina/backend/internal/pkg/crypto"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&Usage{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS platform_settings (key TEXT PRIMARY KEY, value TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func TestApplyDLP(t *testing.T) {
	in := `联系人 13812345678
AWS AKIAIOSFODNN7EXAMPLE 泄漏
Authorization: Bearer abcdef1234567890abcdef
password = SuperSecret123
jwt: eyJhbGciOiJI.eyJzdWIiOiIxMjM0NTY3.SflKxwRJSMeKKF2QT4
普通文本不受影响`
	out, hits := ApplyDLP(in)
	for _, leak := range []string{"13812345678", "AKIAIOSFODNN7EXAMPLE", "SuperSecret123", "eyJhbGciOiJI"} {
		if strings.Contains(out, leak) {
			t.Errorf("泄漏 %q: %s", leak, out)
		}
	}
	if !strings.Contains(out, "普通文本不受影响") {
		t.Error("正常文本不应被误伤")
	}
	for _, typ := range []string{"phone", "aws_key", "bearer", "password_kv", "jwt"} {
		if hits[typ] == 0 {
			t.Errorf("应命中 %s", typ)
		}
	}
	if !strings.Contains(out, "[REDACTED:phone]") {
		t.Error("应带类型标注")
	}
}

func TestBuildPackRoleFilter(t *testing.T) {
	in := PackInput{
		AlertName: "err", AlertBody: "rows: password=hunter2secret",
		RecentServerEvents: "server=1 unreachable",
		RecentCronFailures: "job A failed",
		ConfigSnapshot:     "app 配置（yaml）",
		ViewerRoles:        []string{"dev"},
	}
	p := BuildPack(in)
	if len(p.Blocks) != 1 {
		t.Fatalf("dev 视角应只有告警主体块, got %d", len(p.Blocks))
	}
	if p.Blocks[0].Source != "o2_alert" {
		t.Errorf("唯一块应为告警主体: %s", p.Blocks[0].Source)
	}
	if p.DLPHits["password_kv"] == 0 {
		t.Error("告警体中的密码应被 DLP 拦截")
	}
	// 特权视角四块全有
	p2 := BuildPack(PackInput{AlertName: "e", AlertBody: "x",
		RecentServerEvents: "s", RecentCronFailures: "c", ConfigSnapshot: "k",
		ViewerRoles: []string{"ops"}})
	if len(p2.Blocks) != 4 {
		t.Errorf("ops 视角应有 4 块, got %d", len(p2.Blocks))
	}
	// 渲染含数据围栏声明
	r := p2.Render()
	if !strings.Contains(r, "不可信输入") || !strings.Contains(r, "来源 config_snapshot_masked") {
		t.Error("渲染应含数据围栏与来源声明")
	}
}

func TestRelayComplete(t *testing.T) {
	db := testDB(t)
	var mu sync.Mutex
	var gotBody map[string]any
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"诊断：正常"}}]}`))
	}))
	defer srv.Close()

	key := strings.Repeat("ef", 32)
	cipher, _ := cryptopkg.NewCipher(key)
	svc := NewService(db, cipher)
	ctx := context.Background()
	if err := svc.SaveSettings(ctx, srv.URL+"/v1", "test-model", "sk-test"); err != nil {
		t.Fatal(err)
	}
	out, err := svc.Complete(ctx, "alert_digest", []Message{{Role: "user", Content: "hi"}}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if out != "诊断：正常" {
		t.Errorf("输出不符: %q", out)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotAuth != "Bearer sk-test" {
		t.Errorf("应带 Bearer key: %q", gotAuth)
	}
	if gotBody["model"] != "test-model" {
		t.Errorf("model 不符: %v", gotBody["model"])
	}
	// 用量落库
	var u Usage
	if err := db.First(&u).Error; err != nil || !u.OK || u.Caller != "alert_digest" {
		t.Fatalf("用量应留痕: %+v %v", u, err)
	}
}

func TestAlertAnalysisDegradedWhenNotConfigured(t *testing.T) {
	db := testDB(t)
	relay := NewService(db, nil)
	a := NewAlertAnalysisService(relay)
	// 未配置中转层 → AnalyzeAlert 直接降级（不 panic、不产生分析段）
	if _, ok := a.AnalyzeAlert(context.Background(), "cron_failed", "t", "d"); ok {
		t.Error("未配置 AI 时分析应降级返回 false")
	}
}

// fakeAnalyzer notify.AlertAnalyzer 测试桩。
type fakeAnalyzer struct {
	fn func()
}

func (f fakeAnalyzer) AnalyzeAlert(ctx context.Context, source, title, detail string) (string, bool) {
	f.fn()
	return "", false
}

func (f fakeAnalyzer) NotifyFollowup(ctx context.Context, n notify.EventNotifier, source, title, detail string) {
	f.fn()
}

type fakeN struct {
	onCall func()
}

func (f *fakeN) NotifyEvent(context.Context, string, string, string, string, string) {
	f.onCall()
}
