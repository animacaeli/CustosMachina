package projects

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
	if err := db.AutoMigrate(&Project{}, &EnvTarget{}, &serverRow{}, &notifyGroupRow{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	return db
}

// 跨模块表只建最小同构（servers / notify_groups 属其他模块）。
type serverRow struct {
	ID uint `gorm:"primarykey"`
}

func (serverRow) TableName() string { return "servers" }

type notifyGroupRow struct {
	ID    uint   `gorm:"primarykey"`
	Scope string `gorm:"size:8"`
}

func (notifyGroupRow) TableName() string { return "notify_groups" }

func TestCreateAndNotifyScopeValidation(t *testing.T) {
	db := testDB(t)
	db.Create(&notifyGroupRow{ID: 1, Scope: "prod"})
	db.Create(&notifyGroupRow{ID: 2, Scope: "dev"})
	svc := NewService(db, nil)

	in := SaveProjectInput{
		Name: "demo", RepoURL: "https://git.internal/org/demo", RepoPath: "org/demo",
		NotifyProdGroupID: ptr(2), // dev 群不能用于正式环境
	}
	if _, err := svc.Create(t.Context(), in); err == nil {
		t.Fatal("正式环境绑【dev】群应被拒绝")
	}
	in.NotifyProdGroupID = ptr(1)
	in.NotifyTestGroupID = ptr(1) // prod 群不能用于测试环境
	if _, err := svc.Create(t.Context(), in); err == nil {
		t.Fatal("测试环境绑【P】群应被拒绝")
	}
	in.NotifyTestGroupID = ptr(2)
	p, err := svc.Create(t.Context(), in)
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if p.TrafficCap != 50 || p.TestSlotCount != 3 {
		t.Fatalf("默认值异常: %+v", p)
	}
}

func TestSaveTargets(t *testing.T) {
	db := testDB(t)
	svc := NewService(db, nil)
	p, err := svc.Create(t.Context(), SaveProjectInput{
		Name: "demo", RepoURL: "https://git.internal/org/demo", RepoPath: "org/demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	db.Create(&serverRow{ID: 7})

	err = svc.SaveTargets(t.Context(), p.ID, SaveTargetsInput{Targets: []TargetInput{
		{EnvType: EnvProd, ServerID: 7},
		{EnvType: EnvProd, ServerID: 7},
	}})
	if err == nil {
		t.Fatal("重复环境应被拒绝")
	}

	err = svc.SaveTargets(t.Context(), p.ID, SaveTargetsInput{Targets: []TargetInput{
		{EnvType: EnvProd, ServerID: 7},
		{EnvType: EnvTest, ServerID: 999}, // 不存在的服务器
	}})
	if err == nil {
		t.Fatal("不存在的服务器应被拒绝")
	}

	err = svc.SaveTargets(t.Context(), p.ID, SaveTargetsInput{Targets: []TargetInput{
		{EnvType: EnvProd, ServerID: 7},
	}})
	if err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	_, targets, err := svc.Get(t.Context(), p.ID)
	if err != nil || len(targets) != 1 {
		t.Fatalf("查询异常: %v %+v", err, targets)
	}
	if targets[0].Runtime != RuntimeCompose {
		t.Fatalf("runtime 默认应为 compose, got %s", targets[0].Runtime)
	}
}

func ptr(v uint) *uint { return &v }

// binding tag 回归（同 notify/resources 约定）。
func TestInputsBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{
		`{"name":"demo","repoUrl":"https://g.co/a/b","repoPath":"a/b"}`,
		`{"targets":[{"envType":"prod","serverId":1,"runtime":"compose"}]}`,
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("PUT", "/", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		if strings.Contains(body, "targets") {
			var in SaveTargetsInput
			if err := c.ShouldBindJSON(&in); err != nil {
				t.Fatalf("绑定失败 body=%s: %v", body, err)
			}
		} else {
			var in SaveProjectInput
			if err := c.ShouldBindJSON(&in); err != nil {
				t.Fatalf("绑定失败 body=%s: %v", body, err)
			}
		}
	}
}
