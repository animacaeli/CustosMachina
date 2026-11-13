package ci

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/custos-machina/backend/internal/modules/identity"
	"github.com/custos-machina/backend/internal/modules/notify"
	"github.com/custos-machina/backend/internal/modules/projects"
	cryptopkg "github.com/custos-machina/backend/internal/pkg/crypto"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// realReader 测试用：直接用同库的 projects.Service 作只读投影
// （测试库建了 projects 同构表，真实实现比 fake 覆盖更全）。
func realReader(db *gorm.DB) projects.Reader {
	return projects.NewService(db, nil)
}

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	if err := db.AutoMigrate(&Build{}, &Registry{}, &GlobalConfig{}, &projectRow{}, &notifyGroupRow{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	return db
}

type projectRow struct {
	CIToken             string
	DeletedAt           gorm.DeletedAt
	ID                  uint `gorm:"primarykey"`
	Name                string
	RepoPath            string
	Provider            string
	CIJob               string
	ComposePath         string
	DefaultBranch       string
	TestSlotCount       int
	TestSlotPeak        int
	SlotGraceDays       int
	TrafficCap          int
	NotifyOnSuccess     bool
	NotifyProdGroupID   *uint
	NotifyCanaryGroupID *uint
	NotifyTestGroupID   *uint
}

func (projectRow) TableName() string { return "projects" }

type notifyGroupRow struct {
	ID    uint   `gorm:"primarykey"`
	Scope string `gorm:"size:8"`
}

func (notifyGroupRow) TableName() string { return "notify_groups" }

func TestCIRefParts(t *testing.T) {
	// 合法 job#number
	job, num, ok := ciRefParts("starveil-drama-server#123")
	if !ok || job != "starveil-drama-server" || num != 123 {
		t.Fatalf("合法 CIRef 解析异常: %q %d %v", job, num, ok)
	}
	// 非法形态：无 #、number 非正整数、空段
	for _, bad := range []string{"", "123", "job#", "#123", "job#0", "job#-5", "job#abc"} {
		if _, _, ok := ciRefParts(bad); ok {
			t.Fatalf("%q 应解析失败", bad)
		}
	}
	// job 名含 #：取最后一个 # 为分隔（number 段必须是纯数字）
	job, num, ok = ciRefParts("a#b#7")
	if !ok || job != "a#b" || num != 7 {
		t.Fatalf("多 # CIRef 应取末段: %q %d %v", job, num, ok)
	}
}

func TestValidCanaryTag(t *testing.T) {
	for tag, ok := range map[string]bool{
		"canary-20260923-gh": true,
		"canary-2026092-gh":  false, // 日期不是 8 位
		"canary-20260923-":   false, // 缩写为空
		"v1.0.0":             false,
		"canary-20260923a-x": false,
	} {
		if got := validCanaryTag(tag); got != ok {
			t.Errorf("validCanaryTag(%q) = %v, want %v", tag, got, ok)
		}
	}
}

func giteaSig(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestGiteaWebhook(t *testing.T) {
	db := testDB(t)
	svc := NewService(db, nil, nil, realReader(db))
	body := []byte(`{}`)
	// 未初始化全局配置 → 拒绝
	if err := svc.VerifyGiteaWebhook(t.Context(), body, ""); err == nil {
		t.Fatal("未配置应拒绝")
	}
	db.Create(&GlobalConfig{ID: 1, WebhookSecret: "s3cret"})
	// 空对象是合法 payload（无 ref）：签名正确则静默忽略
	if err := svc.VerifyGiteaWebhook(t.Context(), body, giteaSig("s3cret", body)); err != nil {
		t.Fatalf("正确签名应通过: %v", err)
	}
	if b, err := svc.HandleGiteaPush(t.Context(), body); err != nil || b != nil {
		t.Fatalf("空 payload 应忽略: %v %+v", err, b)
	}
	if err := svc.VerifyGiteaWebhook(t.Context(), body, "deadbeef"); err == nil {
		t.Fatal("错误签名应拒绝")
	}
}

func TestHandleGiteaTagPush(t *testing.T) {
	db := testDB(t)
	svc := NewService(db, nil, nil, realReader(db))
	db.Create(&GlobalConfig{ID: 1, WebhookSecret: "s"})
	for _, p := range []projectRow{
		{ID: 1, RepoPath: "org/demo", Provider: ProviderGitee},
		{ID: 2, RepoPath: "org/demo", Provider: ProviderGitea},
	} {
		if err := db.Create(&p).Error; err != nil {
			t.Fatal(err)
		}
	}

	payload := func(ref, tag string) []byte {
		b, _ := json.Marshal(map[string]any{
			"ref": ref, "after": "abc123",
			"repo":   map[string]any{"full_name": "Org/Demo"}, // 大小写不敏感匹配
			"sender": map[string]any{"login": "gh"},
		})
		return b
	}
	push := func(ref string) (*Build, error) {
		b := payload(ref, "")
		if err := svc.VerifyGiteaWebhook(t.Context(), b, giteaSig("s", b)); err != nil {
			return nil, err
		}
		return svc.HandleGiteaPush(t.Context(), b)
	}

	// v 标签 → prod
	b, err := push("refs/tags/v1.2.3")
	if err != nil || b == nil || b.EnvType != "prod" || b.Status != BuildPending || b.Provider != ProviderGitea || b.ProjectID != 2 {
		t.Fatalf("v 标签落库异常: %v %+v", err, b)
	}
	// canary 标签 → canary
	b, err = push("refs/tags/canary-20260923-gh")
	if err != nil || b == nil || b.EnvType != "canary" {
		t.Fatalf("canary 标签落库异常: %v %+v", err, b)
	}
	// 不合规 canary 标签 → 报错
	if _, err = push("refs/tags/canary-bad"); err == nil {
		t.Fatal("不合规 canary 标签应报错")
	}
	// 分支 push（非标签）→ 忽略
	if b, err = push("refs/heads/main"); err != nil || b != nil {
		t.Fatalf("分支 push 应忽略: %v %+v", err, b)
	}
	// 未登记项目 → 忽略
	p2, _ := json.Marshal(map[string]any{"ref": "refs/tags/v9", "repo": map[string]any{"full_name": "other/repo"}})
	if b, err = svc.HandleGiteaPush(t.Context(), p2); err != nil || b != nil {
		t.Fatalf("未登记项目应忽略: %v %+v", err, b)
	}
	// 总数：2 条
	var cnt int64
	db.Model(&Build{}).Count(&cnt)
	if cnt != 2 {
		t.Fatalf("构建记录数 = %d, want 2", cnt)
	}
}

func TestHandleGiteeTagPush(t *testing.T) {
	db := testDB(t)
	svc := NewService(db, nil, nil, realReader(db))
	db.Create(&GlobalConfig{ID: 1, GiteeWebhook: "gitee-pass"})
	db.Create(&projectRow{ID: 2, RepoPath: "org/demo2", Provider: ProviderGitee, CIJob: "demo2-build"})

	payload := func(ref string) []byte {
		b, _ := json.Marshal(map[string]any{
			"ref": ref, "after": "def456",
			"repository": map[string]any{"full_name": "org/demo2"},
			"pusher":     map[string]any{"name": "someone"},
		})
		return b
	}
	push := func(ref string) (*Build, error) {
		return svc.HandleGiteePush(t.Context(), payload(ref))
	}

	// 错误 token 拒绝（Verify 段）
	if err := svc.VerifyGiteeWebhook(t.Context(), "wrong"); err == nil {
		t.Fatal("错误 X-Gitee-Token 应拒绝")
	}
	// v 标签 → prod，Provider=gitee
	b, err := push("refs/tags/v2.0.0")
	if err != nil || b == nil || b.EnvType != "prod" || b.Provider != ProviderGitee {
		t.Fatalf("gitee v 标签落库异常: %v %+v", err, b)
	}
	// 分支推送 → 忽略
	if b, err = push("refs/heads/master"); err != nil || b != nil {
		t.Fatalf("gitee 分支 push 应忽略: %v %+v", err, b)
	}
	var cnt int64
	db.Model(&Build{}).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("构建记录数 = %d, want 1", cnt)
	}
}

func TestHandleGiteaPush_NotifiesEveryMatchingProject(t *testing.T) {
	// Arrange
	db := testDB(t)
	if err := db.AutoMigrate(&projects.Project{}, &projects.Follow{}, &identity.User{}, &notify.InboxItem{}); err != nil {
		t.Fatal(err)
	}
	bindings := []projects.Project{
		{Name: "case-variant", RepoPath: "Org/Demo", Provider: ProviderGitea},
		{Name: "exact-first", RepoPath: "org/demo", Provider: ProviderGitea},
		{Name: "exact-second", RepoPath: "org/demo", Provider: ProviderGitea},
		{Name: "other-provider", RepoPath: "org/demo", Provider: ProviderGitee},
		{Name: "other-repo", RepoPath: "org/other", Provider: ProviderGitea},
		{Name: "deleted-project", RepoPath: "org/demo", Provider: ProviderGitea},
	}
	if err := db.Create(&bindings).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&bindings[5]).Error; err != nil {
		t.Fatal(err)
	}
	admin := identity.User{DisplayName: "admin", IsLocalAdmin: true}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&GlobalConfig{ID: 1, WebhookSecret: "s"}).Error; err != nil {
		t.Fatal(err)
	}
	notifier := notify.NewService(db, nil)
	t.Cleanup(notifier.Stop)
	svc := NewService(db, nil, notifier, realReader(db))

	// Act
	branch := []byte(`{"ref":"refs/heads/main","repository":{"full_name":"org/demo"}}`)
	for range 2 {
		if b, err := svc.HandleGiteaPush(t.Context(), branch, "branch-delivery"); err != nil || b != nil {
			t.Fatalf("branch webhook should not create a build: %v, %+v", err, b)
		}
	}
	tag := []byte(`{"ref":"refs/tags/v1.2.3","after":"abc123","repository":{"full_name":"org/demo"}}`)
	build, err := svc.HandleGiteaPush(t.Context(), tag, "tag-delivery")
	if err != nil || build == nil {
		t.Fatalf("tag webhook should create a build: %v, %+v", err, build)
	}

	// Assert
	if build.ProjectID != bindings[1].ID {
		t.Fatalf("tag build should still select the first exact match, got project %d", build.ProjectID)
	}
	var items []notify.InboxItem
	if err := db.Where("user_id = ?", admin.ID).Order("id").Find(&items).Error; err != nil {
		t.Fatal(err)
	}
	var got []uint
	for _, item := range items {
		got = append(got, item.ProjectID)
	}
	want := []uint{bindings[0].ID, bindings[1].ID, bindings[2].ID}
	want = append(want, want...)
	if !slices.Equal(got, want) {
		t.Fatalf("want one inbox item per matching project and delivery: got %v, want %v", got, want)
	}
}

func TestListBuildsPaged(t *testing.T) {
	db := testDB(t)
	svc := NewService(db, nil, nil, realReader(db))
	for i := 0; i < 5; i++ {
		db.Create(&Build{ProjectID: 1, EnvType: "prod", Tag: "v1.0." + string(rune('0'+i)), Status: BuildSuccess})
	}
	list, total, err := svc.ListBuilds(t.Context(), BuildQuery{ProjectID: 1, Page: 3, Size: 2})
	if err != nil || total != 5 || len(list) != 1 {
		t.Fatalf("分页异常: total=%d len=%d err=%v", total, len(list), err)
	}
}

// binding tag 回归（约定：新 input struct 必须过绑定测试）。
func TestInputsBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{
		`{"giteaBaseUrl":"https://g.co","giteaToken":"t","webhookSecret":"s"}`,
		`{"giteeBaseUrl":"https://gitee.com","giteeToken":"t","giteeWebhookPass":"p","jenkinsUrl":"https://j.co","jenkinsUser":"u","jenkinsToken":"k"}`,
		`{"name":"ali-cr","type":"aliyun","address":"registry.cn-hangzhou.aliyuncs.com","credential":"u:p"}`,
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("PUT", "/", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		if strings.Contains(body, "ali-cr") {
			var in SaveRegistryInput
			if err := c.ShouldBindJSON(&in); err != nil {
				t.Fatalf("绑定失败 body=%s: %v", body, err)
			}
		} else {
			var in SaveGlobalInput
			if err := c.ShouldBindJSON(&in); err != nil {
				t.Fatalf("绑定失败 body=%s: %v", body, err)
			}
		}
	}
}

func TestPollPendingNotifiesEarlyFailures(t *testing.T) {
	db := testDB(t)
	if err := db.AutoMigrate(&projects.Project{}, &projects.Follow{}, &identity.User{}, &notify.InboxItem{}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	cipher, err := cryptopkg.NewCipher(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	token, err := cipher.Encrypt("test-token", aadGiteaToken)
	if err != nil {
		t.Fatal(err)
	}
	p := projects.Project{Name: "demo", RepoPath: "org/demo", Provider: ProviderGitea}
	admin := identity.User{DisplayName: "admin", IsLocalAdmin: true}
	for _, row := range []any{&p, &admin, &GlobalConfig{ID: 1, GiteaBaseURL: srv.URL, GiteaToken: token}} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, sha := range []string{"", "unknown-commit"} {
		b := Build{ProjectID: p.ID, EnvType: "prod", Tag: "v1", SHA: sha}
		if err := db.Create(&b).Error; err != nil {
			t.Fatal(err)
		}
	}
	notifier := notify.NewService(db, nil)
	t.Cleanup(notifier.Stop)
	svc := NewService(db, cipher, notifier, realReader(db))
	for range 2 {
		if err := svc.PollPending(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	var failed, delivered int64
	if err := db.Model(&Build{}).Where("status = ?", BuildFailed).Count(&failed).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&notify.InboxItem{}).Where("user_id = ? AND project_id = ?", admin.ID, p.ID).Count(&delivered).Error; err != nil {
		t.Fatal(err)
	}
	if failed != 2 || delivered != 2 {
		t.Fatalf("expected one inbox notification per failure, got %d failures and %d notifications", failed, delivered)
	}
}
