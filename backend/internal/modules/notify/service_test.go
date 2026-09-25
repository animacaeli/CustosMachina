package notify

import (
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
	if err := db.AutoMigrate(&Group{}, &SendRecord{}, &identitySettingTable{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	return db
}

// identitySettingTable 仅用于建表（platform_settings 属 identity 模块，这里只建同名表避免跨模块依赖）。
type identitySettingTable struct {
	Key   string `gorm:"primarykey;size:64"`
	Value string `gorm:"type:text"`
}

func (identitySettingTable) TableName() string { return "platform_settings" }

func TestValidScope(t *testing.T) {
	for scope, ok := range map[string]bool{ScopeProd: true, ScopeDev: true, "other": false, "": false} {
		if got := validScope(scope); got != ok {
			t.Errorf("validScope(%q) = %v, want %v", scope, got, ok)
		}
	}
}

func TestDetectProvider(t *testing.T) {
	cases := map[string]provider{
		"https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=x": provWecom,
		"https://oapi.dingtalk.com/robot/send?access_token=x":    provDingtalk,
		"https://open.feishu.cn/open-apis/bot/v2/hook/x":         provFeishu,
		"https://example.com/hook":                               provUnknown,
	}
	for url, want := range cases {
		if got := detectProvider(url); got != want {
			t.Errorf("detectProvider(%q) = %v, want %v", url, got, want)
		}
	}
}

func TestGroupCRUD_WebhookMasked(t *testing.T) {
	db := testDB(t)
	svc := NewService(db, nil)

	if _, err := svc.Create(t.Context(), SaveGroupInput{Name: "值班群", Scope: "bad"}); err == nil {
		t.Fatal("非法 scope 应被拒绝")
	}

	g, err := svc.Create(t.Context(), SaveGroupInput{Name: "值班群", Scope: ScopeProd})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if g.Scope != ScopeProd || g.HasWebhook {
		t.Fatalf("scope/webhook 状态异常: %+v", g)
	}

	// 更新留空 webhook = 保留
	if _, err := svc.Update(t.Context(), g.ID, SaveGroupInput{Name: "值班群", Scope: ScopeProd, Remark: "r"}); err != nil {
		t.Fatalf("更新失败: %v", err)
	}

	list, err := svc.List(t.Context())
	if err != nil || len(list) != 1 {
		t.Fatalf("列表异常: %v %d", err, len(list))
	}
	if list[0].Webhook != "" {
		t.Fatal("列表不得回传 webhook（即便密文）")
	}
}

func TestSend_NoWebhook(t *testing.T) {
	db := testDB(t)
	svc := NewService(db, nil)
	g, err := svc.Create(t.Context(), SaveGroupInput{Name: "测试群", Scope: ScopeDev})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	full, err := svc.Get(t.Context(), g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Send(t.Context(), full, "标题", "内容"); err == nil {
		t.Fatal("未配置 webhook 的群发送应报错")
	}
	// 留痕应有 failed 记录
	var cnt int64
	db.Model(&SendRecord{}).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("发送留痕数 = %d, want 1", cnt)
	}
}

// binding tag 回归：未知 tag 会 panic（第二阶段审核教训），新 input struct 必须过绑定测试。
func TestSaveGroupInputBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{`{"name":"值班群","scope":"prod"}`, `{"name":"x","scope":"dev","webhook":"https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=k","remark":"r"}`} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("PUT", "/", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		var in SaveGroupInput
		if err := c.ShouldBindJSON(&in); err != nil {
			t.Fatalf("绑定失败 body=%s: %v", body, err)
		}
	}
}
