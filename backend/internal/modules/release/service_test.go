package release

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
	if err := db.AutoMigrate(&Release{}, &projectTbl{}, &targetTbl{}, &buildTbl{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	return db
}

type projectTbl struct {
	ID                  uint `gorm:"primarykey"`
	Name                string
	RepoPath            string
	ComposePath         string
	NotifyProdGroupID   *uint
	NotifyCanaryGroupID *uint
	NotifyTestGroupID   *uint
}

func (projectTbl) TableName() string { return "projects" }

type targetTbl struct {
	ID        uint `gorm:"primarykey"`
	ProjectID uint
	EnvType   string
	ServerID  uint
	Runtime   string
}

func (targetTbl) TableName() string { return "project_env_targets" }

type buildTbl struct {
	ID        uint `gorm:"primarykey"`
	ProjectID uint
	EnvType   string
	Tag       string
	Status    string
}

func (buildTbl) TableName() string { return "builds" }

func TestExecuteRequiresPassedBuild(t *testing.T) {
	db := testDB(t)
	svc := NewService(db, nil, nil, nil)
	db.Create(&projectTbl{ID: 1, Name: "Demo App", RepoPath: "org/demo", ComposePath: "deploy/c.yml"})

	// 无构建记录
	_, err := svc.Execute(t.Context(), ReleaseInput{ProjectID: 1, EnvType: "prod", Tag: "v1.0.0"}, "u")
	if err == nil || !strings.Contains(err.Error(), "没有已通过 CI") {
		t.Fatalf("应拒绝未过 CI 的标签: %v", err)
	}
	// 失败的构建也不行
	db.Create(&buildTbl{ProjectID: 1, EnvType: "prod", Tag: "v1.0.0", Status: "failed"})
	_, err = svc.Execute(t.Context(), ReleaseInput{ProjectID: 1, EnvType: "prod", Tag: "v1.0.0"}, "u")
	if err == nil {
		t.Fatal("失败构建应拒绝")
	}
	// 通过的构建但未配部署目标
	db.Create(&buildTbl{ProjectID: 1, EnvType: "prod", Tag: "v2.0.0", Status: "success"})
	_, err = svc.Execute(t.Context(), ReleaseInput{ProjectID: 1, EnvType: "prod", Tag: "v2.0.0"}, "u")
	if err == nil || !strings.Contains(err.Error(), "部署目标") {
		t.Fatalf("应提示未配部署目标: %v", err)
	}
}

func TestNormalizeName(t *testing.T) {
	for in, want := range map[string]string{
		"Demo App": "demo-app",
		"我的项目":     "----", // 非白名单全替换（语义上可用，长度对齐中文字符数）
		"A_B.c-1":  "a_b.c-1",
		"  x  ":    "x",
	} {
		if got := normalizeName(in); got != want {
			t.Errorf("normalizeName(%q) = %q, want %q", in, got, want)
		}
	}
	if normalizeName("　") == "" {
		t.Fatal("全非法字符不应返回空串（兜底 project）")
	}
}

func TestPassedTags(t *testing.T) {
	db := testDB(t)
	svc := NewService(db, nil, nil, nil)
	db.Create(&buildTbl{ProjectID: 1, EnvType: "prod", Tag: "v1", Status: "success"})
	db.Create(&buildTbl{ProjectID: 1, EnvType: "prod", Tag: "v2", Status: "success"})
	db.Create(&buildTbl{ProjectID: 1, EnvType: "prod", Tag: "v3", Status: "failed"})
	tags, err := svc.PassedTags(t.Context(), 1, "prod")
	if err != nil || len(tags) != 2 {
		t.Fatalf("passedTags 异常: %v %v", tags, err)
	}
}

// binding tag 回归（约定）。
func TestInputsBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(`{"projectId":1,"envType":"prod","tag":"v1.0.0"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	var in ReleaseInput
	if err := c.ShouldBindJSON(&in); err != nil {
		t.Fatalf("绑定失败: %v", err)
	}
}
