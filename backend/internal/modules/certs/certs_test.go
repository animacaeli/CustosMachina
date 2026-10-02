package certs

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	cryptopkg "github.com/custos-machina/backend/internal/pkg/crypto"
)

// fakeNotifier 拦截统一通知路由调用（后台 goroutine 写，测试断言读——须加锁）。
type fakeNotifier struct {
	mu     sync.Mutex
	Events []struct {
		Level  string
		Source string
		Title  string
	}
}

func (f *fakeNotifier) NotifyEvent(_ context.Context, source, level, _, title, _ string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Events = append(f.Events, struct {
		Level  string
		Source string
		Title  string
	}{Level: level, Source: source, Title: title})
}

func (f *fakeNotifier) Len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Events)
}

func (f *fakeNotifier) All() []struct {
	Level  string
	Source string
	Title  string
} {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]struct {
		Level  string
		Source string
		Title  string
	}, len(f.Events))
	copy(out, f.Events)
	return out
}

func testSvc(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&Cert{}); err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("cd", 32)
	cipher, err := cryptopkg.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(db, cipher, nil)
	// notifier 由各测试构造后设置一次（生产语义：仅启动装配时注入，
	// 中途替换会与扫描 goroutine 的读构成数据竞争）
	return svc, db
}

func TestValidateInput(t *testing.T) {
	svc, _ := testSvc(t)
	bad := []SaveInput{
		{Name: "x", Domains: "a.com", Email: "e@x.com", DNSProvider: "unknown", ServerID: 1, CertPath: "/c", KeyPath: "/k"},
		{Name: "x", Domains: "a.com", Email: "bad-email", DNSProvider: "alidns", ServerID: 1, CertPath: "/c", KeyPath: "/k"},
		{Name: "x", Domains: "", Email: "e@x.com", DNSProvider: "alidns", ServerID: 1, CertPath: "/c", KeyPath: "/k"},
		{Name: "x", Domains: "a.com", Email: "e@x.com", DNSProvider: "alidns", CADirURL: "http://insecure", ServerID: 1, CertPath: "/c", KeyPath: "/k"},
	}
	for i, in := range bad {
		if _, err := svc.Create(context.Background(), in); err == nil {
			t.Errorf("case %d 应校验失败", i)
		}
	}
	if _, err := svc.Create(context.Background(), SaveInput{
		Name: "ok", Domains: "a.animacaeli.site, b.animacaeli.site", Email: "e@x.com",
		DNSProvider: "alidns", Credentials: map[string]string{"ALICLOUD_ACCESS_KEY": "k"},
		ServerID: 1, CertPath: "/etc/nginx/certs/a.pem", KeyPath: "/etc/nginx/certs/a.key",
	}); err != nil {
		t.Fatalf("合法输入应通过: %v", err)
	}
}

func TestIssueFailsGracefullyWithoutSSH(t *testing.T) {
	svc, db := testSvc(t)
	ctx := context.Background()
	// 无 SSH：创建后手动触发——签发会在 ACME 网络层失败（本地无外网 LE 也 OK，
	// 失败路径必须正确落 status/lastError/NextTryAt 退避）
	c, err := svc.Create(ctx, SaveInput{
		Name: "dogfood", Domains: "test.animacaeli.site", Email: "ops@animacaeli.site",
		DNSProvider: "alidns", Credentials: map[string]string{"ALICLOUD_ACCESS_KEY": "x"},
		ServerID: 1, CertPath: "/c.pem", KeyPath: "/c.key", CADirURL: "https://acme-staging.invalid/dir",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RenewNow(ctx, c.ID); err == nil {
		t.Fatal("无效 CA 地址应失败")
	}
	var got Cert
	db.First(&got, c.ID)
	if got.Status != StatusFailed || got.LastError == "" {
		t.Fatalf("失败应留痕: %+v", got)
	}
	if got.NextTryAt == nil || !got.NextTryAt.After(time.Now()) {
		t.Error("失败应设置退避时间")
	}
}

func TestScanDueWarnExpiring(t *testing.T) {
	svc, db := testSvc(t)
	ctx := context.Background()
	rec := &fakeNotifier{}
	svc.SetNotifier(rec)
	// 造一张 10 天后到期的已签发证书
	exp := time.Now().Add(10 * 24 * time.Hour)
	c := Cert{Name: "near", Domains: "a.com", Status: StatusIssued, Enabled: true, ExpiresAt: &exp}
	db.Create(&c)
	if err := svc.scanDue(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if rec.Len() == 0 {
		t.Fatal("临近到期应发通知")
	}
	e := rec.All()[0]
	if !strings.Contains(e.Source, "cert") || !strings.Contains(e.Title, "near") {
		t.Errorf("事件形态不符: %+v", e)
	}
	if e.Level != "warn" {
		t.Errorf("10 天应 warn: %s", e.Level)
	}
	rec2 := &fakeNotifier{}
	svc.SetNotifier(rec2)
	// 5 天 → critical
	exp2 := time.Now().Add(5 * 24 * time.Hour)
	c2 := Cert{Name: "crit", Domains: "b.com", Status: StatusIssued, Enabled: true, ExpiresAt: &exp2}
	db.Create(&c2)
	_ = svc.scanDue(ctx)
	time.Sleep(200 * time.Millisecond)
	found := false
	for _, ev := range rec2.All() {
		if ev.Title == "证书即将到期：crit" && ev.Level == "critical" {
			found = true
		}
	}
	if !found {
		t.Error("5 天内应 critical")
	}
}
