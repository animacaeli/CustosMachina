package ci

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// giteeClient gitee 适配器（GitProvider）。CI 引擎配套 Jenkins（见 jenkins.go）。
// API v5；认证头 `Authorization: token <access_token>`（项目级 token 优先、全局兜底由调用方决定）。
type giteeClient struct {
	base     string // 如 https://gitee.com（不含 /api/v5）
	token    string
	secret   string // webhook 密码（X-Gitee-Token 明文比对）
	http     *http.Client
	notReady error
}

func newGiteeClient(base, token, secret string) *giteeClient {
	c := &giteeClient{
		base:   strings.TrimRight(base, "/"),
		token:  token,
		secret: secret,
		http:   &http.Client{Timeout: apiTimeout},
	}
	if c.base == "" || c.token == "" {
		c.notReady = errors.New("gitee 全局配置未填写（base url / 私人令牌）")
	}
	return c
}

func (g *giteeClient) Name() string { return ProviderGitee }

func (g *giteeClient) do(ctx context.Context, method, path string, out any) error {
	if g.notReady != nil {
		return g.notReady
	}
	req, err := http.NewRequestWithContext(ctx, method, g.base+"/api/v5"+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+g.token)
	req.Header.Set("Accept", "application/json")
	resp, err := g.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("gitee %s %d: %s", path, resp.StatusCode, body)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// giteePushPayload gitee push / tag_push webhook 的感兴趣字段
// （结构与 GitHub 风格相近：repository.full_name + pusher.name；sender 兜底）。
type giteePushPayload struct {
	Ref        string `json:"ref"` // refs/tags/v1.0.0 / refs/heads/<branch>
	After      string `json:"after"`
	Repository struct {
		FullName string `json:"full_name"`
		HTMLURL  string `json:"html_url"`
		Path     string `json:"path"`
	} `json:"repository"`
	Pusher struct {
		Login string `json:"login"`
		Name  string `json:"name"`
	} `json:"pusher"`
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
}

// VerifyWebhook gitee webhook 的"密码"以 X-Gitee-Token 头原样回传（非 HMAC），
// 常量时间比较防时序侧信道。
func (g *giteeClient) VerifyWebhook(_ []byte, token string) error {
	if g.secret == "" {
		return errors.New("gitee webhook 密码未配置，拒绝回调")
	}
	if subtle.ConstantTimeCompare([]byte(g.secret), []byte(token)) != 1 {
		return errors.New("webhook token 校验失败")
	}
	return nil
}

func (g *giteeClient) ParsePush(body []byte) (*PushEvent, error) {
	var p giteePushPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("webhook payload 解析失败: %w", err)
	}
	pusher := p.Pusher.Login
	if pusher == "" {
		pusher = p.Pusher.Name
	}
	if pusher == "" {
		pusher = p.Sender.Login
	}
	repo := p.Repository.FullName
	if repo == "" {
		repo = p.Repository.Path // 兜底：部分事件类型 path 才带 owner/repo
	}
	return &PushEvent{
		Provider: ProviderGitee, Ref: p.Ref, SHA: p.After,
		RepoPath: repo, Pusher: pusher,
	}, nil
}

func (g *giteeClient) Branches(ctx context.Context, repoPath string) ([]string, error) {
	var bs []branch
	if err := g.do(ctx, http.MethodGet, "/repos/"+repoPath+"/branches", &bs); err != nil {
		return nil, err
	}
	names := make([]string, len(bs))
	for i, b := range bs {
		names[i] = b.Name
	}
	return names, nil
}

// RawFile gitee raw 端点直接返回文件内容（无 base64 包装）。
func (g *giteeClient) RawFile(ctx context.Context, repoPath, path, ref string) ([]byte, error) {
	if g.notReady != nil {
		return nil, g.notReady
	}
	url := fmt.Sprintf("%s/api/v5/repos/%s/raw/%s?ref=%s", g.base, repoPath, strings.TrimPrefix(path, "/"), ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "token "+g.token)
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
		return nil, fmt.Errorf("gitee raw %d: %s", resp.StatusCode, truncateStr(string(body), 200))
	}
	return body, nil
}

// ActionsURL gitee 无平台内流水线页（CI 在 Jenkins），外链指向仓库提交历史。
// Commits P7-M2：GET /api/v5 /repos/{repo}/commits（归一化文本输出）。
func (g *giteeClient) Commits(ctx context.Context, repoPath, ref string, since time.Time, limit int) (string, error) {
	return listCommitsText(ctx, func(pageLim int) ([]commitRow, error) {
		q := "/repos/" + repoPath + "/commits?limit=" + strconv.Itoa(pageLim)
		if ref != "" {
			q += "&sha=" + url.QueryEscape(ref)
		}
		var rows []struct {
			SHA    string `json:"sha"`
			Commit struct {
				Author struct {
					Name string `json:"name"`
					Date string `json:"date"`
				} `json:"author"`
				Message string `json:"message"`
			} `json:"commit"`
		}
		if err := g.do(ctx, http.MethodGet, q, &rows); err != nil {
			return nil, err
		}
		out := make([]commitRow, 0, len(rows))
		for _, r := range rows {
			out = append(out, commitRow{SHA: r.SHA, Author: r.Commit.Author.Name,
				Date: r.Commit.Author.Date, Message: r.Commit.Message})
		}
		return out, nil
	}, since, limit)
}

func (g *giteeClient) ActionsURL(repoPath string) string {
	return g.base + "/" + repoPath + "/commits"
}
