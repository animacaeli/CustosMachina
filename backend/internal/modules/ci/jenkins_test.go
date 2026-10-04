package ci

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cryptopkg "github.com/custos-machina/backend/internal/pkg/crypto"
)

// fakeJenkins 最小 Jenkins：参数化 job（TAG），两次构建（#3 TAG=v2.0.0 SUCCESS、#2 TAG=v1.0.0 FAILURE）。
func fakeJenkins(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	consoleHits := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/job/demo/api/json", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("tree") == "" || !strings.HasPrefix(r.URL.Query().Get("tree"), "builds[number]") {
			http.Error(w, "unexpected tree param", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"builds": []map[string]any{{"number": 3}, {"number": 2}},
		})
	})
	build := func(w http.ResponseWriter, num int, tag, result string, building bool) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number": num, "result": result, "building": building,
			"actions": []map[string]any{{
				"parameters": []map[string]any{{"name": "TAG", "value": tag}},
			}},
		})
	}
	mux.HandleFunc("/job/demo/3/api/json", func(w http.ResponseWriter, r *http.Request) {
		build(w, 3, "v2.0.0", "SUCCESS", false)
	})
	mux.HandleFunc("/job/demo/2/api/json", func(w http.ResponseWriter, r *http.Request) {
		build(w, 2, "v1.0.0", "FAILURE", false)
	})
	mux.HandleFunc("/job/demo/4/api/json", func(w http.ResponseWriter, r *http.Request) {
		build(w, 4, "v3.0.0", "", true) // 进行中
	})
	mux.HandleFunc("/job/demo/3/consoleText", func(w http.ResponseWriter, r *http.Request) {
		consoleHits++
		_, _ = w.Write([]byte("step1 ok\nstep2 ok\n"))
	})
	mux.HandleFunc("/job/other/api/json", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"builds": []map[string]any{}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &consoleHits
}

func TestJenkinsStatusAndLog(t *testing.T) {
	srv, consoleHits := fakeJenkins(t)
	jc := newJenkinsClient(srv.URL, "admin", "tok")
	ref := BuildRef{Job: "demo", Tag: "v2.0.0", RepoPath: "org/demo2"}

	// TAG 参数匹配到 #3 → SUCCESS
	st, err := jc.Status(t.Context(), ref)
	if err != nil || st != BuildSuccess {
		t.Fatalf("状态异常: %v %s", err, st)
	}
	log, err := jc.Log(t.Context(), ref)
	if err != nil || !strings.Contains(log, "step1 ok") {
		t.Fatalf("日志异常: %v %q", err, log)
	}
	if *consoleHits != 1 {
		t.Fatalf("consoleText 调用数 = %d, want 1", *consoleHits)
	}

	// 另一 tag → FAILURE
	st, err = jc.Status(t.Context(), BuildRef{Job: "demo", Tag: "v1.0.0"})
	if err != nil || st != BuildFailed {
		t.Fatalf("v1.0.0 状态异常: %v %s", err, st)
	}

	// 未知 tag（构建尚在队列）→ pending 不报错
	st, err = jc.Status(t.Context(), BuildRef{Job: "demo", Tag: "v9.9.9"})
	if err != nil || st != BuildPending {
		t.Fatalf("未知 tag 应 pending: %v %s", err, st)
	}

	// job 无历史 → pending
	st, err = jc.Status(t.Context(), BuildRef{Job: "other", Tag: "v2.0.0"})
	if err != nil || st != BuildPending {
		t.Fatalf("空 job 应 pending: %v %s", err, st)
	}

	// 未配 job → 明确报错
	if _, err := jc.Status(t.Context(), BuildRef{Tag: "v2.0.0"}); err == nil {
		t.Fatal("未配 job 名应报错")
	}
}

func TestJenkinsUnreachable(t *testing.T) {
	// 立即关闭的假服务器：瞬时错误语义（调用方保留 pending 下轮再试，不标失败）
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()
	jc := newJenkinsClient(srv.URL, "admin", "tok")
	if _, err := jc.Status(context.Background(), BuildRef{Job: "demo", Tag: "v1"}); err == nil {
		t.Fatal("不可达应返回错误")
	}
}

// 非参数化 job：无 TAG 参数 → 退化为 lastBuild（最新一次构建）。
func TestJenkinsNonParameterized(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/job/plain/api/json", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"builds": []map[string]any{{"number": 7}},
		})
	})
	mux.HandleFunc("/job/plain/7/api/json", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number": 7, "result": "SUCCESS", "building": false, "actions": []map[string]any{},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	jc := newJenkinsClient(srv.URL, "admin", "tok")
	st, err := jc.Status(t.Context(), BuildRef{Job: "plain", Tag: "ignored"})
	if err != nil || st != BuildSuccess {
		t.Fatalf("非参数化 job 应取 lastBuild: %v %s", err, st)
	}
}

// PollPending 的 Jenkins 路由：gitee 项目的构建经 ciFor 走 Jenkins 并落终态。
func TestPollPendingViaJenkins(t *testing.T) {
	db := testDB(t)
	srv, _ := fakeJenkins(t)
	cipher, err := cryptopkg.NewCipher("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("构造 cipher 失败: %v", err)
	}
	encTok, err := cipher.Encrypt("jenkins-api-token")
	if err != nil {
		t.Fatalf("加密 token 失败: %v", err)
	}
	svc := NewService(db, cipher, nil, realReader(db))
	db.Create(&GlobalConfig{ID: 1, GiteeBaseURL: "https://gitee.com", JenkinsURL: srv.URL, JenkinsUser: "u", JenkinsToken: encTok})
	db.Create(&projectRow{ID: 2, RepoPath: "org/demo2", Provider: ProviderGitee, CIJob: "demo"})
	db.Create(&Build{ProjectID: 2, EnvType: "prod", Tag: "v2.0.0", SHA: "def456", Provider: ProviderGitee, Status: BuildPending})

	if err := svc.PollPending(t.Context()); err != nil {
		t.Fatalf("轮询失败: %v", err)
	}
	var b Build
	if err := db.First(&b, "project_id = ?", 2).Error; err != nil {
		t.Fatal(err)
	}
	if b.Status != BuildSuccess {
		t.Fatalf("Jenkins 构建状态 = %s, want success", b.Status)
	}
}
