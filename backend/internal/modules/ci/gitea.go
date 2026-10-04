package ci

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// giteaClient gitea 适配器（GitProvider + gitea Actions CIProvider）。
// token 项目级优先、全局兜底由调用方决定传入哪个。
type giteaClient struct {
	base     string
	token    string
	secret   string // webhook HMAC 密钥（VerifyWebhook 用）
	http     *http.Client
	notReady error // base/token 未配置时的原因（工厂侧判定，避免空客户端静默失败）
}

func newGiteaClient(base, token, secret string) *giteaClient {
	c := &giteaClient{
		base:   strings.TrimRight(base, "/"),
		token:  token,
		secret: secret,
		http:   &http.Client{Timeout: apiTimeout},
	}
	if c.base == "" || c.token == "" {
		c.notReady = errors.New("gitea 全局配置未填写（base url / token）")
	}
	return c
}

func (g *giteaClient) Name() string { return ProviderGitea }

func (g *giteaClient) do(ctx context.Context, method, path string, out any) error {
	if g.notReady != nil {
		return g.notReady
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

type branch struct {
	Name string `json:"name"`
}

type actionTask struct {
	ID         uint   `json:"id"`
	HeadSHA    string `json:"head_sha"`
	HeadBranch string `json:"head_branch"`
	Status     string `json:"status"`
}

// ---- GitProvider ----

// giteaTagPayload gitea push webhook（POST body）的感兴趣字段。
// 注意：gitea 实际 payload 用 repository（GitHub 风格），repo 字段不存在——
// 两个都解析兼容（2026-09-26 真机联调发现）。
type giteaTagPayload struct {
	Ref   string `json:"ref"` // refs/tags/v1.0.0 或 refs/heads/<branch>
	After string `json:"after"`
	Repo  struct {
		FullName string `json:"full_name"`
		HTMLURL  string `json:"html_url"`
	} `json:"repo"`
	Repository struct {
		FullName string `json:"full_name"`
		HTMLURL  string `json:"html_url"`
	} `json:"repository"`
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
}

// repoPath 优先 repository（gitea 实际字段），repo 兜底。
func (p *giteaTagPayload) repoPath() string {
	if p.Repository.FullName != "" {
		return p.Repository.FullName
	}
	return p.Repo.FullName
}

// VerifyWebhook X-Gitea-Signature = HMAC-SHA256(body, secret)。
func (g *giteaClient) VerifyWebhook(body []byte, sigHex string) error {
	if g.secret == "" {
		return errors.New("webhook 密钥未配置，拒绝回调")
	}
	mac := hmac.New(sha256.New, []byte(g.secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(sigHex)) {
		return errors.New("webhook 签名校验失败")
	}
	return nil
}

func (g *giteaClient) ParsePush(body []byte) (*PushEvent, error) {
	var p giteaTagPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("webhook payload 解析失败: %w", err)
	}
	return &PushEvent{
		Provider: ProviderGitea, Ref: p.Ref, SHA: p.After,
		RepoPath: p.repoPath(), Pusher: p.Sender.Login,
	}, nil
}

func (g *giteaClient) Branches(ctx context.Context, repoPath string) ([]string, error) {
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

// RawFile 按标签/分支取仓库文件（release 取部署描述用）。
func (g *giteaClient) RawFile(ctx context.Context, repoPath, path, ref string) ([]byte, error) {
	if g.notReady != nil {
		return nil, g.notReady
	}
	url := fmt.Sprintf("%s/api/v1/repos/%s/raw/%s?ref=%s", g.base, repoPath, strings.TrimPrefix(path, "/"), ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	resp, err := g.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gitea raw %d: %s", resp.StatusCode, truncateStr(string(body), 200))
	}
	return body, nil
}

// actionsURL gitea Web UI 的 Actions 页（日志外链兜底）。
func (g *giteaClient) ActionsURL(repoPath string) string {
	return g.base + "/" + repoPath + "/actions"
}

// ---- CIProvider（gitea Actions）----

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

// Status 按 commit 聚合状态（act_runner 写回的 commit status）。
func (g *giteaClient) Status(ctx context.Context, ref BuildRef) (string, error) {
	var st combinedStatus
	if err := g.do(ctx, http.MethodGet, "/repos/"+ref.RepoPath+"/commits/"+ref.SHA+"/status", &st); err != nil {
		return "", err
	}
	return mapStatus(st.State), nil
}

// Log 拉取某次 workflow job 的完整日志（纯文本）。
func (g *giteaClient) Log(ctx context.Context, ref BuildRef) (string, error) {
	task, err := g.actionTaskBySHA(ctx, ref.RepoPath, ref.SHA)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		g.base+"/api/v1/repos/"+ref.RepoPath+"/actions/jobs/"+fmt.Sprint(task.ID)+"/logs", nil)
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
