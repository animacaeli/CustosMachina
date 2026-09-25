package ci

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// giteaClient 极薄 gitea API 客户端（token 项目级优先、全局兜底由调用方决定传入哪个）。
type giteaClient struct {
	base  string
	token string
	http  *http.Client
}

func newGiteaClient(base, token string) *giteaClient {
	return &giteaClient{
		base:  strings.TrimRight(base, "/"),
		token: token,
		http:  &http.Client{Timeout: 15 * time.Second},
	}
}

func (g *giteaClient) do(ctx context.Context, method, path string, out any) error {
	if g.base == "" || g.token == "" {
		return fmt.Errorf("gitea 全局配置未填写（base url / token）")
	}
	req, err := http.NewRequestWithContext(ctx, method, g.base+"/api/v1"+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/json")
	resp, err := g.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("gitea %s %d: %s", path, resp.StatusCode, body)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// combinedStatus gitea commit 聚合状态（act_runner 会把 Actions 结果写成 commit status）。
type combinedStatus struct {
	State    string `json:"state"` // pending | success | failure | error | warning
	SHA      string `json:"sha"`
	Statuses []struct {
		Context   string `json:"context"`
		State     string `json:"state"`
		TargetURL string `json:"target_url"`
	} `json:"statuses"`
}

func (g *giteaClient) commitStatus(ctx context.Context, repoPath, sha string) (*combinedStatus, error) {
	var st combinedStatus
	if err := g.do(ctx, http.MethodGet, "/repos/"+repoPath+"/commits/"+sha+"/status", &st); err != nil {
		return nil, err
	}
	return &st, nil
}

type branch struct {
	Name string `json:"name"`
}

type actionTask struct {
	ID         uint   `json:"id"`
	HeadSHA    string `json:"head_sha"`
	HeadBranch string `json:"head_branch"`
	Status     string `json:"status"`
}

func (g *giteaClient) branches(ctx context.Context, repoPath string) ([]string, error) {
	var bs []branch
	if err := g.do(ctx, http.MethodGet, "/repos/"+repoPath+"/branches?limit=100", &bs); err != nil {
		return nil, err
	}
	names := make([]string, len(bs))
	for i, b := range bs {
		names[i] = b.Name
	}
	return names, nil
}

// actionTaskBySHA 按 commit SHA 找最新的 workflow run（tasks 列表，id 可直接用于 jobLogs）。
func (g *giteaClient) actionTaskBySHA(ctx context.Context, repoPath, sha string) (*actionTask, error) {
	var resp struct {
		WorkflowRuns []actionTask `json:"workflow_runs"`
	}
	if err := g.do(ctx, http.MethodGet, "/repos/"+repoPath+"/actions/tasks?limit=50", &resp); err != nil {
		return nil, err
	}
	tasks := resp.WorkflowRuns
	for i := range tasks {
		if tasks[i].HeadSHA == sha {
			return &tasks[i], nil
		}
	}
	return nil, fmt.Errorf("gitea 上未找到该提交的流水线记录")
}

// jobLogs 拉取某次 workflow job 的完整日志（纯文本）。
func (g *giteaClient) jobLogs(ctx context.Context, repoPath string, taskID uint) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		g.base+"/api/v1/repos/"+repoPath+"/actions/jobs/"+fmt.Sprint(taskID)+"/logs", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	resp, err := g.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gitea 日志接口 %d: %s", resp.StatusCode, truncateStr(string(body), 200))
	}
	return string(body), nil
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// actionsURL gitea Web UI 的 Actions 页（日志外链兜底）。
func (g *giteaClient) actionsURL(repoPath string) string {
	return g.base + "/" + repoPath + "/actions"
}

// HTTPDo 带鉴权执行外部请求（release 模块取 raw 文件等复用鉴权）。
func (g *giteaClient) HTTPDo(req *http.Request) (*http.Response, error) {
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}
	return g.http.Do(req)
}

// BaseURL 只读暴露 base（拼 raw 地址用）。
func (g *giteaClient) BaseURL() string { return g.base }

// mapStatus commit status → 平台构建状态。
func mapStatus(state string) string {
	switch state {
	case "success", "warning":
		return BuildSuccess
	case "failure", "error":
		return BuildFailed
	default: // pending / 空
		return BuildRunning
	}
}
