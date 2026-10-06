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

// k3s 集群表最小同构（k3s 模块的表，本模块只读计数）。
type k3sClusterRow struct {
	ID uint `gorm:"primarykey"`
}

func (k3sClusterRow) TableName() string { return "k3s_clusters" }

// v0.12.1 复核 N1 回归：清空部署目标绝不落 server_id=0 零行（零行会击穿
// release/configs/slots/canary 的 First/Count 存在性守卫，报出误导性错误）。
func TestSaveTargetsClearWritesNoZeroRows(t *testing.T) {
	db := testDB(t)
	if err := db.AutoMigrate(&k3sClusterRow{}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(db, nil)
	ctx := t.Context()
	p, err := svc.Create(ctx, SaveProjectInput{Name: "clr", RepoURL: "https://g.example/a/b.git", RepoPath: "a/b"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&serverRow{ID: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveTargets(ctx, p.ID, SaveTargetsInput{Targets: []TargetInput{
		{EnvType: "prod", ServerID: 1},
	}}); err != nil {
		t.Fatalf("配置目标失败: %v", err)
	}
	// 清空：三条环境全 0/0 → 目标行应被删除而不是落零行
	if err := svc.SaveTargets(ctx, p.ID, SaveTargetsInput{Targets: []TargetInput{
		{EnvType: "prod"}, {EnvType: "canary"}, {EnvType: "test"},
	}}); err != nil {
		t.Fatalf("清空目标失败: %v", err)
	}
	var n int64
	db.Model(&EnvTarget{}).Where("project_id = ?", p.ID).Count(&n)
	if n != 0 {
		t.Errorf("清空后不应残留目标行，got %d", n)
	}
}

// k3s 目标（server_id=0, cluster_id>0）：走集群存在性校验，不被
// 「服务器 0 不存在」误杀；集群不存在要报明确错误。
func TestSaveTargetsK3sValidation(t *testing.T) {
	db := testDB(t)
	if err := db.AutoMigrate(&k3sClusterRow{}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(db, nil)
	ctx := t.Context()
	p, _ := svc.Create(ctx, SaveProjectInput{Name: "k3sp", RepoURL: "https://g.example/a/c.git", RepoPath: "a/c"})
	if err := db.Create(&k3sClusterRow{ID: 7}).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveTargets(ctx, p.ID, SaveTargetsInput{Targets: []TargetInput{
		{EnvType: "prod", Runtime: "k3s", ClusterID: 7},
	}}); err != nil {
		t.Fatalf("合法 k3s 目标应保存成功: %v", err)
	}
	var et EnvTarget
	if err := db.Where("project_id = ? AND env_type = ?", p.ID, "prod").First(&et).Error; err != nil || et.ClusterID != 7 || et.ServerID != 0 {
		t.Fatalf("k3s 目标行不符: %+v err=%v", et, err)
	}
	if err := svc.SaveTargets(ctx, p.ID, SaveTargetsInput{Targets: []TargetInput{
		{EnvType: "prod", Runtime: "k3s", ClusterID: 999},
	}}); err == nil {
		t.Error("不存在的集群应报错")
	}
}
