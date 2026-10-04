package ci

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGiteeParsePush(t *testing.T) {
	gc := newGiteeClient("https://gitee.com", "tok", "s")
	// pusher.login 优先
	ev, err := gc.ParsePush([]byte(`{"ref":"refs/tags/v1.0.0","after":"abc","repository":{"full_name":"org/repo"},"pusher":{"login":"alice","name":"Bob"}}`))
	if err != nil || ev.RepoPath != "org/repo" || ev.Pusher != "alice" || ev.Provider != ProviderGitee {
		t.Fatalf("解析异常: %v %+v", err, ev)
	}
	// pusher.name 兜底 → sender.login 兜底
	ev, err = gc.ParsePush([]byte(`{"ref":"refs/heads/main","repository":{"path":"org/repo2"},"pusher":{"name":"Bob"},"sender":{"login":"carl"}}`))
	if err != nil || ev.RepoPath != "org/repo2" || ev.Pusher != "Bob" {
		t.Fatalf("name 兜底异常: %v %+v", err, ev)
	}
	ev, err = gc.ParsePush([]byte(`{"ref":"refs/heads/main","repository":{"path":"org/repo2"},"sender":{"login":"carl"}}`))
	if err != nil || ev.Pusher != "carl" {
		t.Fatalf("sender 兜底异常: %v %+v", err, ev)
	}
}

func TestGiteeVerifyWebhook(t *testing.T) {
	gc := newGiteeClient("https://gitee.com", "tok", "pass")
	if err := gc.VerifyWebhook(nil, "pass"); err != nil {
		t.Fatalf("正确 token 应通过: %v", err)
	}
	if err := gc.VerifyWebhook(nil, "Pass"); err == nil {
		t.Fatal("错误 token 应拒绝")
	}
	noPass := newGiteeClient("https://gitee.com", "tok", "")
	if err := noPass.VerifyWebhook(nil, ""); err == nil {
		t.Fatal("未配置密码应拒绝")
	}
}

func TestGiteeRawFileAndBranches(t *testing.T) {
	var gotAuth, gotPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v5/repos/org/repo/raw/deploy/compose.yaml", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Query().Get("ref")
		_, _ = w.Write([]byte("services: {}"))
	})
	mux.HandleFunc("/api/v5/repos/org/repo/branches", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"name":"main"},{"name":"dev"}]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	gc := newGiteeClient(srv.URL, "tok", "s")
	body, err := gc.RawFile(t.Context(), "org/repo", "deploy/compose.yaml", "v1.0.0")
	if err != nil || string(body) != "services: {}" {
		t.Fatalf("RawFile 异常: %v %q", err, body)
	}
	if gotAuth != "token tok" || gotPath != "v1.0.0" {
		t.Fatalf("RawFile 请求异常: auth=%q ref=%q", gotAuth, gotPath)
	}
	bs, err := gc.Branches(t.Context(), "org/repo")
	if err != nil || len(bs) != 2 || bs[0] != "main" {
		t.Fatalf("Branches 异常: %v %v", err, bs)
	}
	// 未配置 → notReady
	empty := newGiteeClient("", "", "")
	if _, err := empty.RawFile(t.Context(), "o/r", "f", "v1"); err == nil || !strings.Contains(err.Error(), "未填写") {
		t.Fatalf("未配置应报 notReady: %v", err)
	}
}
