package ci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// jenkinsClient Jenkins 适配器（CIProvider，配套 gitee）。
// 平台只读状态与日志：构建由 gitee 仓库 webhook 驱动 Jenkins（gitee 插件），
// 平台不做 CI 控制面——不需要触发接口，也就不需要 CSRF crumb（那是写操作门槛）。
// 定位约定：job 参数化构建，参数名 TAG=git tag；找不到参数化痕迹时退化为
// lastBuild 语义（单 job 串行场景）。Basic 认证（用户 + API token）。
type jenkinsClient struct {
	base     string // 如 https://jenkins.example.com（不带尾斜杠）
	user     string
	token    string
	http     *http.Client
	notReady error
}

func newJenkinsClient(base, user, token string) *jenkinsClient {
	c := &jenkinsClient{
		base:  strings.TrimRight(base, "/"),
		user:  user,
		token: token,
		http:  &http.Client{Timeout: apiTimeout},
	}
	if c.base == "" || c.user == "" || c.token == "" {
		c.notReady = errors.New("Jenkins 全局配置未填写（地址 / 账号 / API token）")
	}
	return c
}

func (j *jenkinsClient) Name() string { return "jenkins" }

func (j *jenkinsClient) do(ctx context.Context, path string, out any) error {
	if j.notReady != nil {
		return j.notReady
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, j.base+path, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(j.user, j.token)
	req.Header.Set("Accept", "application/json")
	resp, err := j.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("jenkins %s %d: %s", path, resp.StatusCode, body)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// jenkinsBuild 单次构建的元信息（api/json 视图）。
type jenkinsBuild struct {
	Number   int    `json:"number"`
	Result   string `json:"result"` // SUCCESS | FAILURE | UNSTABLE | ABORTED | null（进行中/排队）
	Building bool   `json:"building"`
	Actions  []struct {
		Parameters []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"parameters"`
	} `json:"actions"`
}

// locateBuild 按参数 TAG 定位目标构建；返回 nil 表示尚未出现（排队中）。
// 倒序查最近 maxScan 次（webhook 与 Jenkins 触发几乎同时，目标 build 必在近端）；
// 若这些构建都无 TAG 参数（非参数化 job）则退化为最新一次构建。
func (j *jenkinsClient) locateBuild(ctx context.Context, ref BuildRef) (*jenkinsBuild, error) {
	if ref.Job == "" {
		return nil, errors.New("项目未配置 Jenkins job 名（项目管理 → CI job）")
	}
	var list struct {
		Builds []struct {
			Number int `json:"number"`
		} `json:"builds"`
	}
	if err := j.do(ctx, fmt.Sprintf("/job/%s/api/json?tree=builds[number]{0,10}", ref.Job), &list); err != nil {
		return nil, err
	}
	if len(list.Builds) == 0 {
		return nil, nil // job 无历史构建：目标尚在队列
	}
	sawParameterized := false
	for _, b := range list.Builds {
		var jb jenkinsBuild
		if err := j.do(ctx, fmt.Sprintf("/job/%s/%d/api/json", ref.Job, b.Number), &jb); err != nil {
			return nil, err
		}
		for _, act := range jb.Actions {
			for _, p := range act.Parameters {
				if p.Name == "TAG" {
					sawParameterized = true
					if p.Value == ref.Tag {
						return &jb, nil
					}
				}
			}
		}
	}
	if sawParameterized {
		return nil, nil // 参数化 job 但本 tag 的构建还没出现（排队中）
	}
	// 非参数化 job：最新一次构建即目标（单 job 串行约定）
	latest := list.Builds[0].Number
	var jb jenkinsBuild
	if err := j.do(ctx, fmt.Sprintf("/job/%s/%d/api/json", ref.Job, latest), &jb); err != nil {
		return nil, err
	}
	return &jb, nil
}

// Status 实现 CIProvider。Jenkins 不可达等瞬时错误返回 error，
// 调用方保留 pending 下轮再试（45 分钟超时兜底），不阻塞已有成功记录的发布。
func (j *jenkinsClient) Status(ctx context.Context, ref BuildRef) (string, error) {
	jb, err := j.locateBuild(ctx, ref)
	if err != nil {
		return "", err
	}
	if jb == nil {
		return BuildPending, nil
	}
	if jb.Building {
		return BuildRunning, nil
	}
	switch jb.Result {
	case "SUCCESS":
		return BuildSuccess, nil
	case "FAILURE", "UNSTABLE", "ABORTED":
		return BuildFailed, nil
	default: // result 为空且非 building：异常态，视为仍在途
		return BuildPending, nil
	}
}

// Log 实现 CIProvider：consoleText 全量拉取。
func (j *jenkinsClient) Log(ctx context.Context, ref BuildRef) (string, error) {
	jb, err := j.locateBuild(ctx, ref)
	if err != nil {
		return "", err
	}
	if jb == nil {
		return "", errors.New("Jenkins 上尚未出现该标签的构建（可能仍在队列中）")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/job/%s/%d/consoleText", j.base, ref.Job, jb.Number), nil)
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(j.user, j.token)
	resp, err := j.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("jenkins consoleText %d: %s", resp.StatusCode, truncateStr(string(body), 200))
	}
	return string(body), nil
}
