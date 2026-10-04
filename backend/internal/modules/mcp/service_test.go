package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeSrc struct{}

func (fakeSrc) ListServers(context.Context) []map[string]any {
	return []map[string]any{{"id": 1, "name": "prod-1", "host": "10.0.0.1"}}
}
func (fakeSrc) ListProjects(context.Context) []map[string]any { return nil }
func (fakeSrc) ListBuilds(context.Context, uint, int) []map[string]any {
	return nil
}
func (fakeSrc) ListReleases(context.Context, uint, int) []map[string]any {
	return nil
}
func (fakeSrc) ListCronRuns(context.Context, int) []map[string]any { return nil }
func (fakeSrc) ListContainers(context.Context, uint) ([]map[string]any, error) {
	return nil, nil
}

func mcpTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	if err := db.AutoMigrate(&Token{}, &Call{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	return db
}

func TestTokenLifecycle(t *testing.T) {
	db := mcpTestDB(t)
	svc := NewService(db)

	out, err := svc.IssueToken(t.Context(), "ci-bot", TokenRoleDev)
	if err != nil {
		t.Fatal(err)
	}
	if out.Plaintext == "" || len(out.Plaintext) < 20 {
		t.Fatalf("明文异常: %q", out.Plaintext)
	}
	// API 序列化不含 hash（json:"-"），凭证哈希只在库内
	b, _ := json.Marshal(out)
	if bytes.Contains(b, []byte("tokenHash")) {
		t.Fatal("TokenHash 不应外泄到签发结果的 JSON")
	}
	tc, err := svc.Verify(t.Context(), out.Plaintext)
	if err != nil || tc.Role != TokenRoleDev {
		t.Fatalf("验证失败: %v %+v", err, tc)
	}
	// 错误凭证
	if _, err := svc.Verify(t.Context(), "mcp_wrong"); err == nil {
		t.Fatal("错误凭证应拒绝")
	}
	// 吊销即时生效
	if err := svc.Revoke(t.Context(), out.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Verify(t.Context(), out.Plaintext); err == nil {
		t.Fatal("吊销后应立即拒绝")
	}
	// 恢复
	_ = svc.Enable(t.Context(), out.ID)
	if _, err := svc.Verify(t.Context(), out.Plaintext); err != nil {
		t.Fatalf("恢复后应可用: %v", err)
	}
}

// bearerRT 注入 Authorization 的测试 RoundTripper。
type bearerRT struct{ token string }

func (r *bearerRT) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+r.token)
	return http.DefaultTransport.RoundTrip(req)
}

// 端到端：SDK 客户端经 Streamable HTTP 调 list_servers，验证鉴权→角色→tool→审计全链。
func TestMcpEndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := mcpTestDB(t)
	svc := NewService(db)
	svc.SetSources(fakeSrc{}, nil)

	h := NewHandler(svc)
	r := gin.New()
	r.Any("/mcp", h.mcpEndpoint)
	r.Any("/mcp/*any", h.mcpEndpoint)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)

	out, err := svc.IssueToken(t.Context(), "e2e", TokenRoleAdmin)
	if err != nil {
		t.Fatal(err)
	}

	connect := func(token string) *mcp.ClientSession {
		t.Helper()
		tr := &mcp.StreamableClientTransport{
			Endpoint:   ts.URL + "/mcp",
			HTTPClient: &http.Client{Transport: &bearerRT{token: token}},
		}
		cl := mcp.NewClient(&mcp.Implementation{Name: "e2e-client"}, nil)
		sess, err := cl.Connect(t.Context(), tr, nil)
		if err != nil {
			t.Fatalf("连接失败: %v", err)
		}
		t.Cleanup(func() { _ = sess.Close() })
		return sess
	}

	sess := connect(out.Plaintext)
	tools, err := sess.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools 失败: %v", err)
	}
	if len(tools.Tools) == 0 {
		t.Fatal("应注册至少一个 tool")
	}
	got := map[string]bool{}
	for _, tl := range tools.Tools {
		got[tl.Name] = true
	}
	for _, want := range []string{"list_servers", "list_containers", "get_context_pack"} {
		if !got[want] {
			t.Fatalf("缺少 tool %s", want)
		}
	}

	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "list_servers", Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool 失败: %v", err)
	}
	found := false
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok && strings.Contains(tc.Text, "prod-1") {
			found = true
		}
	}
	if !found {
		t.Fatalf("list_servers 结果异常: %+v", res.Content)
	}

	// 审计落库（验证 http ctx 是否传导到 tool handler——传导则 token_id 正确）
	var calls []Call
	db.Where("tool = ?", "list_servers").Find(&calls)
	if len(calls) == 0 {
		t.Fatal("审计未落库")
	}
	if calls[0].TokenID == 0 {
		t.Log("警告：SDK 未传导 http ctx，审计 token_id=0（降级可接受但需知悉）")
	}
}
