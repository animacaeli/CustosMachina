package ci

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

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
	ID                  uint   `gorm:"primarykey"`
	RepoPath            string `gorm:"size:255"`
	CIToken             string `gorm:"type:text"`
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

func TestWebhookSignature(t *testing.T) {
	db := testDB(t)
	svc := NewService(db, nil, nil)
	body := []byte(`{}`)
	// 未初始化全局配置 → 拒绝
	if err := svc.VerifySignature(t.Context(), body, ""); err == nil {
		t.Fatal("未配置应拒绝")
	}
	db.Create(&GlobalConfig{ID: 1, WebhookSecret: "s3cret"})

	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write(body)
	good := hex.EncodeToString(mac.Sum(nil))
	if err := svc.VerifySignature(t.Context(), body, good); err != nil {
		t.Fatalf("正确签名应通过: %v", err)
	}
	if err := svc.VerifySignature(t.Context(), body, "deadbeef"); err == nil {
		t.Fatal("错误签名应拒绝")
	}
}

func TestHandleTagPush(t *testing.T) {
	db := testDB(t)
	svc := NewService(db, nil, nil)
	db.Create(&GlobalConfig{ID: 1, WebhookSecret: "s"})
	db.Create(&projectRow{ID: 1, RepoPath: "org/demo"})

	payload := func(ref, tag string) []byte {
		b, _ := json.Marshal(map[string]any{
			"ref": ref, "after": "abc123",
			"repo":   map[string]any{"full_name": "Org/Demo"}, // 大小写不敏感匹配
			"sender": map[string]any{"login": "gh"},
		})
		return b
	}

	// v 标签 → prod
	b, err := svc.HandleTagPush(t.Context(), payload("refs/tags/v1.2.3", ""))
	if err != nil || b == nil || b.EnvType != "prod" || b.Status != BuildPending {
		t.Fatalf("v 标签落库异常: %v %+v", err, b)
	}
	// canary 标签 → canary
	b, err = svc.HandleTagPush(t.Context(), payload("refs/tags/canary-20260923-gh", ""))
	if err != nil || b == nil || b.EnvType != "canary" {
		t.Fatalf("canary 标签落库异常: %v %+v", err, b)
	}
	// 不合规 canary 标签 → 报错
	if _, err = svc.HandleTagPush(t.Context(), payload("refs/tags/canary-bad", "")); err == nil {
		t.Fatal("不合规 canary 标签应报错")
	}
	// 分支 push（非标签）→ 忽略
	if b, err = svc.HandleTagPush(t.Context(), payload("refs/heads/main", "")); err != nil || b != nil {
		t.Fatalf("分支 push 应忽略: %v %+v", err, b)
	}
	// 未登记项目 → 忽略
	p2, _ := json.Marshal(map[string]any{"ref": "refs/tags/v9", "repo": map[string]any{"full_name": "other/repo"}})
	if b, err = svc.HandleTagPush(t.Context(), p2); err != nil || b != nil {
		t.Fatalf("未登记项目应忽略: %v %+v", err, b)
	}
	// 总数：2 条
	var cnt int64
	db.Model(&Build{}).Count(&cnt)
	if cnt != 2 {
		t.Fatalf("构建记录数 = %d, want 2", cnt)
	}
}

func TestListBuildsPaged(t *testing.T) {
	db := testDB(t)
	svc := NewService(db, nil, nil)
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
