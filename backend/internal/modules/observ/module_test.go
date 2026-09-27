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
	c := renderCadvisor()
	for _, want := range []string{cadvisorImage, "custos-cadvisor", "/var/run:ro", "/dev/kmsg"} {
		if !strings.Contains(c, want) {
			t.Errorf("cadvisor 模板缺 %q:\n%s", want, c)
		}
	}

	composeYAML, vectorYAML := renderVector("http://10.0.0.1:5080/api/default/custos/_json")
	if !strings.Contains(vectorYAML, "uri: http://10.0.0.1:5080/api/default/custos/_json") {
		t.Errorf("vector 配置应指向 O2 地址:\n%s", vectorYAML)
	}
	if !strings.Contains(composeYAML, "./vector.yaml:/etc/vector/vector.yaml:ro") {
		t.Errorf("vector compose 应相对路径挂载同目录配置:\n%s", composeYAML)
	}
}

func TestO2URLSetting(t *testing.T) {
	svc := NewService(testDB(t), nil)
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
	if componentOf("cadvisor") == nil || componentOf("vector") == nil {
		t.Fatal("内置组件应可查到")
	}
	if componentOf("nope") != nil {
		t.Fatal("未知组件应返回 nil")
	}
}

// O2 地址内嵌 basic auth 时拆出渲染 sink auth 块。
func TestRenderVectorAuth(t *testing.T) {
	_, vectorYAML := renderVector("http://foo:bar@10.0.0.1:5080/api/default/custos/_json")
	if !strings.Contains(vectorYAML, "uri: http://10.0.0.1:5080/api/default/custos/_json") {
		t.Errorf("uri 应去掉 userinfo:\n%s", vectorYAML)
	}
	if !strings.Contains(vectorYAML, "user: foo") || !strings.Contains(vectorYAML, "password: bar") {
		t.Errorf("basic auth 块缺失:\n%s", vectorYAML)
	}
	// 无凭据时不渲染 auth 块
	_, plain := renderVector("http://10.0.0.1:5080/api/default/custos/_json")
	if strings.Contains(plain, "strategy: basic") {
		t.Errorf("无凭据不应渲染 auth 块:\n%s", plain)
	}
}
