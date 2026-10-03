package observ

import (
	"context"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	if err := db.Exec(`CREATE TABLE platform_settings (key TEXT PRIMARY KEY, value TEXT)`).Error; err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	return db
}

func TestRenderTemplates(t *testing.T) {
	byName := map[string]*Component{}
	for _, c := range components {
		byName[c.Name] = c
	}
	c := byName["cadvisor"].Compose
	for _, want := range []string{cadvisorImage, "custos-cadvisor", "/var/run:ro", "/dev/kmsg"} {
		if !strings.Contains(c, want) {
			t.Errorf("cadvisor 模板缺 %q:\n%s", want, c)
		}
	}
	vectorYAML := renderVectorYAML("http://10.0.0.1:5080/api/default/custos/_json", "default")
	if !strings.Contains(vectorYAML, "uri: http://10.0.0.1:5080/api/default/custos/_json") {
		t.Errorf("vector 配置应指向 O2 地址:\n%s", vectorYAML)
	}
	// 自激环过滤：O2 访问日志中间件行不得回流（真机 v0.91 教训：每次 POST
	// 生成一条新访问日志，无限自我循环）
	if !strings.Contains(vectorYAML, "middlewares::access_log") ||
		!strings.Contains(vectorYAML, "inputs: [drop_o2_access_log]") {
		t.Errorf("vector 模板应过滤 O2 访问日志行:\n%s", vectorYAML)
	}
	if !strings.Contains(byName["vector"].Compose, "./vector.yaml:/etc/vector/vector.yaml:ro") {
		t.Errorf("vector compose 应相对路径挂载同目录配置")
	}
	// R3：指标 remote write（scrape exporter → O2 _metrics 端点）
	if !strings.Contains(vectorYAML, "prometheus_scrape") ||
		!strings.Contains(vectorYAML, "http://127.0.0.1:8081/metrics") {
		t.Errorf("vector 模板应采集宿主 exporter 指标:\n%s", vectorYAML)
	}
	if !strings.Contains(vectorYAML, "prometheus_remote_write") ||
		!strings.Contains(vectorYAML, "http://10.0.0.1:5080/api/default/prometheus/api/v1/write") {
		t.Errorf("vector 模板应 remote_write 到 O2 指标端点:\n%s", vectorYAML)
	}
	fb := renderFluentBitConf("http://u:p@10.0.0.1:5080/api/x/_json")
	if !strings.Contains(fb, "http_User u") || !strings.Contains(fb, "http_Passwd p") {
		t.Errorf("fluent-bit basic auth 渲染缺失:\n%s", fb)
	}
	if !strings.Contains(fb, "URI http://10.0.0.1:5080/api/x/_json") {
		t.Errorf("fluent-bit URI 应去凭据:\n%s", fb)
	}
}

func TestO2URLSetting(t *testing.T) {
	svc := NewService(testDB(t), nil, nil)
	ctx := context.Background()

	if err := svc.SetO2URL(ctx, "not a url"); err == nil {
		t.Error("非法 URL 应被拒绝")
	}
	if err := svc.SetO2URL(ctx, "http://o2.internal:5080/api/default/custos/_json"); err != nil {
		t.Fatalf("合法 URL 保存失败: %v", err)
	}
	if got := svc.O2URL(ctx); got != "http://o2.internal:5080/api/default/custos/_json" {
		t.Fatalf("读取不符: %q", got)
	}
	// 覆盖更新
	if err := svc.SetO2URL(ctx, "http://o2b:5080/api/x/_json"); err != nil {
		t.Fatal(err)
	}
	if got := svc.O2URL(ctx); got != "http://o2b:5080/api/x/_json" {
		t.Fatalf("覆盖更新不符: %q", got)
	}
}

func TestComponentLookup(t *testing.T) {
	for _, n := range []string{"cadvisor", "node-exporter", "vector", "fluent-bit"} {
		if componentOf(n) == nil {
			t.Fatalf("内置组件 %s 应可查到", n)
		}
	}
	if componentOf("nope") != nil {
		t.Fatal("未知组件应返回 nil")
	}
	if !safeFilename("vector.yaml") || safeFilename("a;rm") || safeFilename("../x") {
		t.Fatal("配置文件名白名单失效")
	}
}

// O2 地址内嵌 basic auth 时拆出渲染 sink auth 块。
func TestRenderVectorAuth(t *testing.T) {
	vectorYAML := renderVectorYAML("http://foo:bar@10.0.0.1:5080/api/default/custos/_json", "default")
	if !strings.Contains(vectorYAML, "uri: http://10.0.0.1:5080/api/default/custos/_json") {
		t.Errorf("uri 应去掉 userinfo:\n%s", vectorYAML)
	}
	if !strings.Contains(vectorYAML, "user: foo") || !strings.Contains(vectorYAML, "password: bar") {
		t.Errorf("basic auth 块缺失:\n%s", vectorYAML)
	}
	// 无凭据时不渲染 auth 块
	plain := renderVectorYAML("http://10.0.0.1:5080/api/default/custos/_json", "default")
	if strings.Contains(plain, "strategy: basic") {
		t.Errorf("无凭据不应渲染 auth 块:\n%s", plain)
	}
}
