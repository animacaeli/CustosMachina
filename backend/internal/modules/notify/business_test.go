// business_test.go P7-M5 业务告警：token 签发/校验/吊销 + 渲染契约。
package notify

import (
	"context"
	"strings"
	"testing"
)

func TestIssueAndVerifyBusinessToken(t *testing.T) {
	env := newRuleTestEnv(t)
	ctx := context.Background()

	out, err := env.svc.IssueBusinessToken(ctx, "归因服务", "app-attribution")
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	if len(out.Plaintext) < 10 || out.Plaintext[:4] != "biz_" {
		t.Fatalf("明文应为 biz_ 前缀，实际 %q", out.Plaintext)
	}
	if out.TokenHash == "" {
		t.Fatal("库内存 hash 应有值")
	}
	// 正确 app 校验通过
	if _, err := env.svc.VerifyBusinessToken(ctx, out.Plaintext, "app-attribution"); err != nil {
		t.Fatalf("正确凭证+app 应通过: %v", err)
	}
	// app 不匹配拒绝（防 token 借用串名）
	if _, err := env.svc.VerifyBusinessToken(ctx, out.Plaintext, "other-app"); err == nil {
		t.Error("凭证与 app 不匹配应拒绝")
	}
	// 错误凭证拒绝
	if _, err := env.svc.VerifyBusinessToken(ctx, "biz_notexist", "app-attribution"); err == nil {
		t.Error("错误凭证应拒绝")
	}
	// 吊销即时生效
	if err := env.svc.SetBusinessTokenEnabled(ctx, out.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := env.svc.VerifyBusinessToken(ctx, out.Plaintext, "app-attribution"); err == nil {
		t.Error("吊销后应拒绝")
	}
	// app 必填
	if _, err := env.svc.IssueBusinessToken(ctx, "x", "  "); err == nil {
		t.Error("空 app 应拒绝")
	}
}

func TestRenderBusinessAlert(t *testing.T) {
	in := BusinessAlertInput{
		App: "app-attribution", Level: LevelWarn,
		Title: "请求失败率突增", Detail: "最近 5 分钟失败率 15%",
		Metadata: map[string]string{"endpoint": "/api/v1/track"},
	}
	title, detail, dedup := RenderBusinessAlert(in)
	if title != "[app-attribution] 请求失败率突增" {
		t.Errorf("标题应带业务名前缀，实际 %q", title)
	}
	if dedup != "business/app-attribution/请求失败率突增" {
		t.Errorf("dedupKey 不符: %q", dedup)
	}
	if !strings.Contains(detail, "endpoint: /api/v1/track") {
		t.Errorf("metadata 应渲染进行文，实际 %q", detail)
	}
}

func TestBusinessSourceInValidSources(t *testing.T) {
	for _, s := range ValidSources {
		if s == SourceBusiness {
			return
		}
	}
	t.Fatal("SourceBusiness 必须在 ValidSources（规则页可按 source=business 配置路由）")
}
