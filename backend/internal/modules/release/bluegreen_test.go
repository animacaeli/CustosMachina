package release

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeDeployer 记录蓝绿链路的部署侧调用，可编程失败点。
type fakeDeployer struct {
	deployed  []string
	destroyed []string
	confProjs []string
	waitErr   map[string]error // 按域名注入健康门禁失败
	confErr   error
}

func (f *fakeDeployer) DeployComposeTo(_ context.Context, _ uint, name, _ string) (string, string, error) {
	f.deployed = append(f.deployed, name)
	return "up ok", "/opt/custos-machina/compose/" + name, nil
}
func (f *fakeDeployer) WaitComposeHealthy(_ context.Context, _ uint, name string, _ time.Duration) error {
	if err, ok := f.waitErr[name]; ok {
		return err
	}
	return nil
}
func (f *fakeDeployer) DeployNginxConf(_ context.Context, _ uint, proj, _ string) (string, error) {
	f.confProjs = append(f.confProjs, proj)
	if f.confErr != nil {
		return "nginx -t failed", f.confErr
	}
	return "reload ok", nil
}
func (f *fakeDeployer) DestroyCompose(_ context.Context, _ uint, name string) (string, error) {
	f.destroyed = append(f.destroyed, name)
	return "down ok", nil
}
func (f *fakeDeployer) RegistryLogin(context.Context, uint, string, string, string) error {
	return nil
}
func (f *fakeDeployer) RegistryLogout(context.Context, uint, string) {}

type fakeRenderer struct{ calls []string }

func (f *fakeRenderer) FullConf(_ context.Context, _ uint, activeColor string) (string, error) {
	f.calls = append(f.calls, activeColor)
	return "# conf " + activeColor, nil
}

func setupBG(t *testing.T) (*Service, *fakeDeployer, *fakeRenderer) {
	t.Helper()
	db := testDB(t)
	svc := NewService(db, nil, nil, nil, realReader(db), nil, nil)
	d := &fakeDeployer{}
	r := &fakeRenderer{}
	svc.res, svc.canary = d, r
	return svc, d, r
}

// ---- 首次发布：落 blue 域，BGState 推进，不销毁任何域 ----

func TestBlueGreenFirstRelease(t *testing.T) {
	old := drainWindow
	drainWindow = 10 * time.Millisecond
	defer func() { drainWindow = old }()

	svc, d, r := setupBG(t)
	rel, err := svc.executeBlueGreen(context.Background(),
		&projectRow{Name: "demo"}, &EnvTargetRow{ServerID: 1, Runtime: "compose"},
		ReleaseInput{ProjectID: 1, EnvType: "prod", Tag: "v1"}, "tester", "yaml")
	if err != nil {
		t.Fatalf("首发失败: %v", err)
	}
	if rel.Color != ColorBlue || rel.Status != ReleaseSuccess {
		t.Fatalf("首发应落 blue/success, got %s/%s", rel.Color, rel.Status)
	}
	if len(d.deployed) != 1 || d.deployed[0] != "demo-prod-blue" {
		t.Errorf("应部署 demo-prod-blue, got %v", d.deployed)
	}
	if len(d.confProjs) != 1 || len(r.calls) != 1 || r.calls[0] != ColorBlue {
		t.Errorf("conf 应按 blue 渲染并写入, got conf=%v colors=%v", d.confProjs, r.calls)
	}
	if len(d.destroyed) != 0 {
		t.Errorf("首发不应销毁任何域, got %v", d.destroyed)
	}
	if c := svc.ActiveColor(context.Background(), 1); c != ColorBlue {
		t.Errorf("BGState 应推进到 blue, got %q", c)
	}
	if !strings.Contains(rel.Output, "原单域") {
		t.Errorf("首发输出应提示旧单域手动下线:\n%s", rel.Output)
	}
}

// ---- 二次发布：落 green 域，drain 后销毁 blue ----

func TestBlueGreenAlternatesAndDestroysOld(t *testing.T) {
	old := drainWindow
	drainWindow = 10 * time.Millisecond
	defer func() { drainWindow = old }()

	svc, d, _ := setupBG(t)
	ctx := context.Background()
	p := &projectRow{Name: "demo"}
	target := &EnvTargetRow{ServerID: 1, Runtime: "compose"}
	if _, err := svc.executeBlueGreen(ctx, p, target, ReleaseInput{ProjectID: 1, EnvType: "prod", Tag: "v1"}, "t", "y"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.executeBlueGreen(ctx, p, target, ReleaseInput{ProjectID: 1, EnvType: "prod", Tag: "v2"}, "t", "y"); err != nil {
		t.Fatal(err)
	}
	if d.deployed[len(d.deployed)-1] != "demo-prod-green" {
		t.Errorf("二次应部署 green, got %v", d.deployed)
	}
	if len(d.destroyed) != 1 || d.destroyed[0] != "demo-prod-blue" {
		t.Errorf("切换后应销毁旧 blue 域, got %v", d.destroyed)
	}
	if c := svc.ActiveColor(ctx, 1); c != ColorGreen {
		t.Errorf("活跃色应为 green, got %q", c)
	}
}

// ---- 健康门禁失败：销毁新域、BGState 不变、线上零影响 ----

func TestBlueGreenHealthGateFailure(t *testing.T) {
	svc, d, _ := setupBG(t)
	svc.db.Save(&BGState{ProjectID: 1, ActiveColor: ColorBlue})
	d.waitErr = map[string]error{"demo-prod-green": errors.New("unhealthy")}
	ctx := context.Background()

	rel, err := svc.executeBlueGreen(ctx, &projectRow{Name: "demo"},
		&EnvTargetRow{ServerID: 1, Runtime: "compose"},
		ReleaseInput{ProjectID: 1, EnvType: "prod", Tag: "v2"}, "t", "y")
	if err == nil {
		t.Fatal("门禁失败应返回错误")
	}
	if rel.Status != ReleaseFailed {
		t.Fatalf("应落 failed 记录, got %s", rel.Status)
	}
	if len(d.destroyed) != 1 || d.destroyed[0] != "demo-prod-green" {
		t.Errorf("失败的新域应被销毁, got %v", d.destroyed)
	}
	if c := svc.ActiveColor(ctx, 1); c != ColorBlue {
		t.Errorf("活跃色不应变化, got %q", c)
	}
	if len(d.confProjs) != 0 {
		t.Errorf("门禁失败不应写 conf, got %v", d.confProjs)
	}
}

// ---- BGState 落库失败：流量已切到健康新域，绝不能销毁（P0 修复的回归） ----

func TestBlueGreenStateSaveFailureKeepsNewDomain(t *testing.T) {
	svc, d, _ := setupBG(t)
	svc.db.Save(&BGState{ProjectID: 1, ActiveColor: ColorBlue})
	// 注入落库失败：删掉 blue_green_states 表使 Save 必败
	if err := svc.db.Migrator().DropTable(&BGState{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	rel, err := svc.executeBlueGreen(ctx, &projectRow{Name: "demo"},
		&EnvTargetRow{ServerID: 1, Runtime: "compose"},
		ReleaseInput{ProjectID: 1, EnvType: "prod", Tag: "v2"}, "t", "y")
	if err == nil {
		t.Fatal("落库失败应返回错误")
	}
	_ = rel
	if len(d.destroyed) != 0 {
		t.Errorf("conf 已切换后绝不能销毁新域, got %v", d.destroyed)
	}
	// 恢复表供后续断言
	if err := svc.db.AutoMigrate(&BGState{}); err != nil {
		t.Fatal(err)
	}
}

// ---- conf 切换失败（nginx -t 不过）：线上零影响，销毁新域 ----

func TestBlueGreenConfFailureNoImpact(t *testing.T) {
	svc, d, _ := setupBG(t)
	svc.db.Save(&BGState{ProjectID: 1, ActiveColor: ColorBlue})
	d.confErr = errors.New("nginx -t failed")
	ctx := context.Background()

	if _, err := svc.executeBlueGreen(ctx, &projectRow{Name: "demo"},
		&EnvTargetRow{ServerID: 1, Runtime: "compose"},
		ReleaseInput{ProjectID: 1, EnvType: "prod", Tag: "v2"}, "t", "y"); err == nil {
		t.Fatal("conf 失败应返回错误")
	}
	if c := svc.ActiveColor(ctx, 1); c != ColorBlue {
		t.Errorf("活跃色不应变化, got %q", c)
	}
	if len(d.destroyed) != 1 || d.destroyed[0] != "demo-prod-green" {
		t.Errorf("新域应回收, got %v", d.destroyed)
	}
}
