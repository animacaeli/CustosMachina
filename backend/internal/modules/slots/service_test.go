package slots

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/custos-machina/backend/internal/modules/projects"
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
	if err := db.AutoMigrate(&Slot{}, &Override{}, &projectTbl{}, &buildTbl{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	db.Create(&projectTbl{ID: 1, Name: "demo", TestSlotCount: 3, SlotGraceDays: 3})
	db.Exec("CREATE TABLE project_env_targets (id integer primary key, project_id integer, env_type text, server_id integer, runtime text)")
	return db
}

type projectTbl struct {
	ID                  uint `gorm:"primarykey"`
	Name                string
	RepoPath            string
	ComposePath         string
	DefaultBranch       string
	TestSlotCount       int
	SlotGraceDays       int
	TrafficCap          int
	NotifyOnSuccess     bool
	NotifyProdGroupID   *uint
	NotifyCanaryGroupID *uint
	NotifyTestGroupID   *uint
}

func (projectTbl) TableName() string { return "projects" }

type buildTbl struct{ ID uint }

func (buildTbl) TableName() string { return "builds" }

func TestOccupyAndSlotBounds(t *testing.T) {
	db := testDB(t)
	svc := NewService(db, nil, nil, nil, realReader(db))

	// 超出配置个数的槽位
	if _, err := svc.Occupy(t.Context(), 1, OccupyInput{SlotName: "dev5", Branch: "feat/x", DurationValue: 1, DurationUnit: "days"}, 1, "u"); err == nil {
		t.Fatal("dev5 超出 3 个槽位应被拒")
	}
	// 不合法槽位名
	if _, err := svc.Occupy(t.Context(), 1, OccupyInput{SlotName: "prod1", Branch: "b", DurationValue: 1, DurationUnit: "days"}, 1, "u"); err == nil {
		t.Fatal("非 devN 槽位名应被拒")
	}
	s1, err := svc.Occupy(t.Context(), 1, OccupyInput{SlotName: "dev1", Branch: "feat/x", DurationValue: 2, DurationUnit: "hours"}, 7, "张三")
	if err != nil {
		t.Fatalf("占用失败: %v", err)
	}
	if time.Until(s1.ExpireAt) > 2*time.Hour+time.Minute {
		t.Fatal("占用时长换算异常")
	}
	// 同槽位二次占用被拒
	if _, err := svc.Occupy(t.Context(), 1, OccupyInput{SlotName: "dev1", Branch: "feat/y", DurationValue: 1, DurationUnit: "days"}, 8, "李四"); err == nil {
		t.Fatal("已占用槽位应拒绝")
	}
	// 列表视图
	list, _ := svc.List(t.Context(), 1)
	if len(list) != 3 || !list[0]["occupied"].(bool) || list[1]["occupied"].(bool) {
		t.Fatalf("槽位视图异常: %+v", list)
	}
}

func TestReleasePermission(t *testing.T) {
	db := testDB(t)
	svc := NewService(db, nil, nil, nil, realReader(db))
	// 未配测试部署目标时 Release 应在销毁前报错提示（项目未配置目标）
	svc.Occupy(t.Context(), 1, OccupyInput{SlotName: "dev1", Branch: "b", DurationValue: 1, DurationUnit: "days"}, 7, "张三")
	if err := svc.Release(t.Context(), 1, "dev1", 8, false); err == nil {
		t.Fatal("他人释放应被拒")
	}
}

func TestSweepExpireMarksAndRecycles(t *testing.T) {
	db := testDB(t)
	svc := NewService(db, nil, nil, nil, realReader(db))
	// 直接落一条已过期的占用
	old := Slot{
		ProjectID: 1, SlotName: "dev2", Branch: "feat/old",
		OccupiedBy: "u", OccupiedByUID: 9, Duration: time.Hour,
		ExpireAt: time.Now().Add(-2 * 24 * time.Hour), Status: StatusOccupied, // 已过宽限
	}
	db.Create(&old)
	if err := svc.SweepExpire(t.Context()); err != nil {
		t.Fatal(err)
	}
	// 未配测试部署目标 → Release 的销毁会失败但标记为 expired
	var cnt int64
	db.Model(&Slot{}).Where("status = ?", StatusExpired).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("到期未标记: %d", cnt)
	}
}

func TestSlotIndexAndDeployName(t *testing.T) {
	if slotIndex("dev3") != 3 || slotIndex("bad") <= 64 {
		t.Fatal("slotIndex 异常")
	}
	if got := deployName("我的 Demo", "dev2"); got != "---demo-test-dev2" {
		t.Fatalf("deployName = %q", got)
	}
}

// binding tag 回归（约定）。
func TestOccupyInputBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{
		`{"slotName":"dev1","branch":"feat/x","durationValue":1,"durationUnit":"days"}`,
		`{"slotName":"dev1","branch":"b","durationValue":4,"durationUnit":"hours"}`,
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		var in OccupyInput
		if err := c.ShouldBindJSON(&in); err != nil {
			t.Fatalf("绑定失败 body=%s: %v", body, err)
		}
	}
}
