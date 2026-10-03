package observ

import (
	"context"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/pkg/crypto"
)

func templateTestEnv(t *testing.T) *Service {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&Alert{}, &AlertTemplate{}); err != nil {
		t.Fatal(err)
	}
	// o2NameFor 前缀反查用的项目表
	if err := db.Exec(`CREATE TABLE projects (id INTEGER PRIMARY KEY, name TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO projects (id, name) VALUES (7, 'Demo Project')`).Error; err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("ab", 32)
	cipher, err := crypto.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	return NewService(db, nil, cipher)
}

// 模板占位符与查询体双向一致性校验。
func TestTemplateValidation(t *testing.T) {
	svc := templateTestEnv(t)
	_, err := svc.CreateTemplate(context.Background(), SaveTemplateInput{
		Name: "bad", QueryType: "sql",
		Query:        "select * from logs where svc = {{service_name}}",
		Placeholders: []Placeholder{{Key: "other", Label: "其他"}},
	})
	if err == nil || !strings.Contains(err.Error(), "未被") {
		t.Fatalf("悬空元数据应报错, got %v", err)
	}
	_, err = svc.CreateTemplate(context.Background(), SaveTemplateInput{
		Name: "bad2", QueryType: "sql",
		Query: "select * from logs where svc = {{missing}}",
	})
	if err == nil || !strings.Contains(err.Error(), "缺少占位符定义") {
		t.Fatalf("缺元数据应报错, got %v", err)
	}
}

// 渲染：字符串转义（注入防护）、数字校验、默认值、必填缺失。
func TestRenderTemplate(t *testing.T) {
	svc := templateTestEnv(t)
	ctx := context.Background()
	tpl, err := svc.CreateTemplate(ctx, SaveTemplateInput{
		Name: "err-spike", Category: "日志", QueryType: "sql",
		Query: "select count(*) as c from \"err_logs\" where service_name = {{service_name}} and retry > {{min_retry}}",
		Placeholders: []Placeholder{
			{Key: "service_name", Label: "服务名", Type: "string", Required: true},
			{Key: "min_retry", Label: "最小重试", Type: "number", Default: "3"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := svc.RenderTemplate(ctx, tpl.ID, map[string]string{"service_name": "x' or '1'='1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "'x'' or ''1''=''1'") {
		t.Fatalf("单引号转义形态不符: %s", out)
	}
	if !strings.Contains(out, "retry > 3") {
		t.Fatalf("数字默认值未生效: %s", out)
	}
	if _, err := svc.RenderTemplate(ctx, tpl.ID, map[string]string{"service_name": "svc", "min_retry": "abc"}); err == nil {
		t.Fatal("非数字应报错")
	}
	if _, err := svc.RenderTemplate(ctx, tpl.ID, map[string]string{}); err == nil {
		t.Fatal("必填缺失应报错")
	}
}

// 模板实例化：SQL 服务端渲染（客户端 SQL 被忽略）、触发参数取模板默认、归属强制。
func TestInstantiateFromTemplate(t *testing.T) {
	svc := templateTestEnv(t)
	ctx := context.Background()
	tpl, err := svc.CreateTemplate(ctx, SaveTemplateInput{
		Name: "err-spike", QueryType: "sql", Period: 5, Threshold: 3, Level: "critical",
		Query:        "select count(*) as c from \"err_logs\" where service_name = {{service_name}}",
		Placeholders: []Placeholder{{Key: "service_name", Type: "string", Required: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	in := SaveAlertInput{
		Name: "user-manage-err", StreamName: "err_logs", SQL: "drop table x",
		Operator: ">=", Frequency: 1, Enabled: true, Level: "warn",
		ProjectID: 7, TemplateID: tpl.ID, Params: map[string]string{"service_name": "user-manage"},
	}
	out, err := svc.InstantiateFromTemplate(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.SQL, "drop table") {
		t.Fatal("客户端 SQL 不应透传")
	}
	if !strings.Contains(out.SQL, "'user-manage'") {
		t.Fatalf("渲染不符: %s", out.SQL)
	}
	// 显式覆盖生效（level 允许覆盖）；未显式给的字段取模板默认
	if out.Level != "warn" || out.Period != 5 || out.Threshold != 3 {
		t.Fatalf("覆盖/默认不符: %+v", out)
	}
	inBare := in
	inBare.Level = ""
	out2, err := svc.InstantiateFromTemplate(ctx, inBare)
	if err != nil {
		t.Fatal(err)
	}
	if out2.Level != "critical" {
		t.Fatalf("缺省应取模板级别: %+v", out2)
	}
	in2 := in
	in2.ProjectID = 0
	if _, err := svc.InstantiateFromTemplate(ctx, in2); err == nil {
		t.Fatal("无项目归属应拒绝")
	}
	in3 := in
	in3.TemplateID = 0
	if _, err := svc.InstantiateFromTemplate(ctx, in3); err == nil {
		t.Fatal("无模板应拒绝")
	}
}

// O2 名前缀 + webhook 回名反解 + 参数快照。
func TestO2NamePrefixAndLookup(t *testing.T) {
	svc := templateTestEnv(t)
	ctx := context.Background()
	tpl, _ := svc.CreateTemplate(ctx, SaveTemplateInput{
		Name: "tpl", QueryType: "sql",
		Query: "select count(*) as c from \"default\"",
	})
	a, err := svc.CreateAlert(ctx, SaveAlertInput{
		Name: "err-spike", StreamName: "default", SQL: "select count(*) as c from \"default\"",
		Operator: ">=", Frequency: 1, Enabled: true, Level: "warn",
		ProjectID: 7, TemplateID: tpl.ID, Params: map[string]string{"k": "v"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := svc.o2NameFor(a); got != "demo-project-err-spike" {
		t.Fatalf("O2 名前缀不符: %q", got)
	}
	if _, err := svc.alertByName(ctx, "demo-project-err-spike"); err != nil {
		t.Fatalf("回流名反解失败: %v", err)
	}
	if !strings.Contains(a.ParamsSnapshot, "\"k\"") {
		t.Fatalf("参数快照缺失: %s", a.ParamsSnapshot)
	}
}
