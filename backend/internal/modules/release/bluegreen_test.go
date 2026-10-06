package release

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
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

// runBG 触发蓝绿并轮询至终态（异步执行，最长等 5s）。
func runBG(t *testing.T, svc *Service, p *projectRow, in ReleaseInput) (*Release, error) {
	t.Helper()
	ctx := context.Background()
	target := &EnvTargetRow{ServerID: 1, Runtime: "compose"}
	rel, err := svc.startBlueGreen(ctx, p, target, in, "tester", "yaml: demo", nil)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var got Release
		svc.db.First(&got, rel.ID)
		if got.Status != ReleaseRunning {
			rel = &got
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return rel, nil
}

// ---- 首次发布：落 blue 域，BGState 推进，不销毁任何域 ----

func TestBlueGreenFirstRelease(t *testing.T) {
	old := drainWindowDefault
	drainWindowDefault = 10 * time.Millisecond
	defer func() { drainWindowDefault = old }()

	svc, d, r := setupBG(t)
	rel, err := runBG(t, svc, &projectRow{Name: "demo"},
		ReleaseInput{ProjectID: 1, EnvType: "prod", Tag: "v1"})
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
	old := drainWindowDefault
	drainWindowDefault = 10 * time.Millisecond
	defer func() { drainWindowDefault = old }()

	svc, d, _ := setupBG(t)
	ctx := context.Background()
	p := &projectRow{Name: "demo"}
	if _, err := runBG(t, svc, p, ReleaseInput{ProjectID: 1, EnvType: "prod", Tag: "v1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := runBG(t, svc, p, ReleaseInput{ProjectID: 1, EnvType: "prod", Tag: "v2"}); err != nil {
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

	rel, err := runBG(t, svc, &projectRow{Name: "demo"},
		ReleaseInput{ProjectID: 1, EnvType: "prod", Tag: "v2"})
	if err != nil {
		// 异步化后 startBlueGreen 本身不报执行错误，终态在记录里
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
	// 注入落库失败：更新回调里报错（表结构不动，语义只针对本次 Save）
	svc.db.Callback().Update().Before("gorm:update").Register("test-inject-fail", func(tx *gorm.DB) {
		if tx.Statement.Table == "blue_green_states" {
			_ = tx.AddError(errors.New("injected save failure"))
		}
	})
	defer svc.db.Callback().Update().Remove("test-inject-fail")
	rel, err := runBG(t, svc, &projectRow{Name: "demo"},
		ReleaseInput{ProjectID: 1, EnvType: "prod", Tag: "v2"})
	if err != nil {
		t.Fatal(err)
	}
	if rel.Status != ReleaseFailed {
		t.Fatalf("BGState 落库失败终态应为 failed, got %s", rel.Status)
	}
	if len(d.destroyed) != 0 {
		t.Errorf("conf 已切换后绝不能销毁新域, got %v", d.destroyed)
	}
	if !strings.Contains(rel.Output, "保留运行") {
		t.Errorf("输出应注明新域保留运行:\n%s", rel.Output)
	}
}

// ---- conf 切换失败（nginx -t 不过）：线上零影响，销毁新域 ----

func TestBlueGreenConfFailureNoImpact(t *testing.T) {
	svc, d, _ := setupBG(t)
	svc.db.Save(&BGState{ProjectID: 1, ActiveColor: ColorBlue})
	d.confErr = errors.New("nginx -t failed")
	ctx := context.Background()

	rel, err := runBG(t, svc, &projectRow{Name: "demo"},
		ReleaseInput{ProjectID: 1, EnvType: "prod", Tag: "v2"})
	if err != nil {
		t.Fatal(err)
	}
	if rel.Status != ReleaseFailed {
		t.Fatalf("conf 失败终态应为 failed, got %s", rel.Status)
	}
	if c := svc.ActiveColor(ctx, 1); c != ColorBlue {
		t.Errorf("活跃色不应变化, got %q", c)
	}
	if len(d.destroyed) != 1 || d.destroyed[0] != "demo-prod-green" {
		t.Errorf("新域应回收, got %v", d.destroyed)
	}
}
