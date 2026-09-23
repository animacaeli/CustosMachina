package canary

import (
	"context"
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
	if err := db.AutoMigrate(&Policy{}, &projectTbl{}, &buildTbl{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	db.Create(&projectTbl{ID: 1, TrafficCap: 50, Name: "Demo"})
	db.Create(&buildTbl{ProjectID: 1, EnvType: "canary", Tag: "canary-20260923-gh", Status: "success"})
	return db
}

type projectTbl struct {
	ID                  uint `gorm:"primarykey"`
	TrafficCap          int
	Name                string
	NotifyCanaryGroupID *uint
}

func (projectTbl) TableName() string { return "projects" }

type buildTbl struct {
	ID        uint `gorm:"primarykey"`
	ProjectID uint
	EnvType   string
	Tag       string
	Status    string
}

func (buildTbl) TableName() string { return "builds" }

// fakeSSH 记录写入内容的假承载层。
type fakeSSH struct {
	lastPath, lastContent string
	calls                 int
}

func (f *fakeSSH) WriteFileAndReload(_ context.Context, _ uint, path, content string) (string, error) {
	f.calls++
	f.lastPath, f.lastContent = path, content
	return "ok", nil
}

func mkInput(typ string, pct int) SavePolicyInput {
	in := SavePolicyInput{Type: typ, BoundTag: "canary-20260923-gh"}
	if typ == TypeHeader {
		in.HeaderKey, in.HeaderValue = "x-canary", "gh"
	} else {
		in.TrafficPercent = pct
	}
	return in
}

func TestTrafficCapValidation(t *testing.T) {
	db := testDB(t)
	svc := NewService(db, &fakeSSH{}, nil)

	if _, err := svc.Create(t.Context(), 1, mkInput(TypeTraffic, 60)); err == nil {
		t.Fatal("单条 60% 应超 50% 上限被拒")
	}
	if _, err := svc.Create(t.Context(), 1, mkInput(TypeTraffic, 30)); err != nil {
		t.Fatalf("30%% 应通过: %v", err)
	}
	// 第二条 30% 会到 60%，拒绝
	if _, err := svc.Create(t.Context(), 1, mkInput(TypeTraffic, 30)); err == nil {
		t.Fatal("总和 60% 应被拒")
	}
	// header 策略不受流量上限约束
	if _, err := svc.Create(t.Context(), 1, mkInput(TypeHeader, 0)); err != nil {
		t.Fatalf("header 策略应通过: %v", err)
	}
	// 绑定不存在的标签
	bad := mkInput(TypeHeader, 0)
	bad.BoundTag = "canary-19990101-xx"
	if _, err := svc.Create(t.Context(), 1, bad); err == nil {
		t.Fatal("未过 CI 的标签应被拒")
	}
}

func TestPublishVersioning(t *testing.T) {
	db := testDB(t)
	ssh := &fakeSSH{}
	svc := NewService(db, ssh, nil)
	// 灰度部署目标
	db.Exec("CREATE TABLE project_env_targets (id integer primary key, project_id integer, env_type text, server_id integer, runtime text)")
	db.Exec("INSERT INTO project_env_targets (project_id, env_type, server_id, runtime) VALUES (1,'canary',7,'compose')")

	h, _ := svc.Create(t.Context(), 1, mkInput(TypeHeader, 0))
	tr, _ := svc.Create(t.Context(), 1, mkInput(TypeTraffic, 20))

	v, _, err := svc.Publish(t.Context(), 1, "u")
	if err != nil || v != 1 || ssh.calls != 1 {
		t.Fatalf("发布异常: v=%d calls=%d err=%v", v, ssh.calls, err)
	}
	if !strings.Contains(ssh.lastContent, "map $http_x_canary_mux") || !strings.Contains(ssh.lastContent, "split_clients") {
		t.Fatalf("渲染缺分流配置:\n%s", ssh.lastContent)
	}
	if !strings.Contains(ssh.lastPath, "/opt/custos-machina/canary/") {
		t.Fatalf("写入路径异常: %s", ssh.lastPath)
	}
	// 两条策略都应是已发布 v1
	var ps []Policy
	db.Where("project_id = 1").Find(&ps)
	for _, p := range ps {
		if p.PublishedVersion != 1 {
			t.Fatalf("策略 %d 版本 = %d, want 1", p.ID, p.PublishedVersion)
		}
	}
	// 修改任一策略 → 回到未发布
	svc.Update(t.Context(), 1, h.ID, mkInput(TypeHeader, 0))
	var hp Policy
	db.First(&hp, h.ID)
	if hp.PublishedVersion != 0 {
		t.Fatal("修改后应回到未发布")
	}
	// 再发布 → v2，且 traffic 策略保持 v1（未变更，但聚合语义是整体替换：全部启用策略升 v2）
	v2, _, _ := svc.Publish(t.Context(), 1, "u")
	if v2 != 2 {
		t.Fatalf("第二次发布版本 = %d, want 2", v2)
	}
	db.First(&hp, h.ID)
	var tp Policy
	db.First(&tp, tr.ID)
	if hp.PublishedVersion != 2 || tp.PublishedVersion != 2 {
		t.Fatalf("聚合发布应整体替换: h=%d t=%d", hp.PublishedVersion, tp.PublishedVersion)
	}
}

func TestRenderNginxMultiTrafficSum(t *testing.T) {
	// 部署模型只有一个 canary 实例：多条 traffic 按总和切灰度
	conf, err := renderNginx("demo", []Policy{
		{Type: TypeTraffic, TrafficPercent: 10},
		{Type: TypeTraffic, TrafficPercent: 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(conf, "30.00% canary;") {
		t.Fatalf("流量总和渲染异常:\n%s", conf)
	}
}

// binding tag 回归（约定）。
func TestSavePolicyInputBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{
		`{"type":"header","headerKey":"x-canary","headerValue":"gh","boundTag":"canary-20260923-gh"}`,
		`{"type":"traffic","trafficPercent":20,"boundTag":"canary-20260923-gh"}`,
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		var in SavePolicyInput
		if err := c.ShouldBindJSON(&in); err != nil {
			t.Fatalf("绑定失败 body=%s: %v", body, err)
		}
	}
}
