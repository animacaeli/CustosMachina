// Package observ 观测组件一键部署（第四阶段 M3，docs/plan-phase4-runtime.md 第三节第 3 项）：
// 内置 cadvisor / vector 两个 compose 模板，选目标主机一键部署/升级/卸载。
// 语义对齐 K8s DaemonSet 的手动版：k3s 期把模板翻译成 DaemonSet 即可。
// cadvisor 已半弃维护（版本固定）：k3s 期被 kubelet 内置 cAdvisor 淘汰，届时只保 vector。
package observ

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/modules/resources"
)

var ErrBadComponent = errors.New("未知组件")

// ---- 组件模板（配置化下发，版本升级 = 换模板重部署）----

const (
	CompCadvisor = "cadvisor"
	CompVector   = "vector"

	settingO2URL = "observ.o2_url" // platform_settings 键：vector 的日志输出目标

	cadvisorImage = "gcr.m.daocloud.io/cadvisor/cadvisor:v0.49.1" // gcr.io 的 daocloud 代理（gcr.io 与 docker hub 非热门镜像国内均不可达，真机实测）；固定版本：上游半弃维护，不追新
	vectorImage   = "timberio/vector:0.46.1-alpine"
)

type Component struct {
	Name       string `json:"name"`
	Image      string `json:"image"`
	Remark     string `json:"remark"`
	NeedsO2URL bool   `json:"needsO2Url"` // vector 的输出指向平台配置的 O2 地址
}

var components = []Component{
	{
		Name: CompCadvisor, Image: cadvisorImage,
		Remark: "容器指标采集（半弃维护，k3s 期被 kubelet cAdvisor 淘汰；宿主端口 8081）",
	},
	{
		Name: CompVector, Image: vectorImage,
		Remark:     "容器日志采集（docker.sock），输出指向平台配置的 OpenObserve 地址",
		NeedsO2URL: true,
	},
}

func component(name string) *Component {
	for i := range components {
		if components[i].Name == name {
			return &components[i]
		}
	}
	return nil
}

var urlRe = regexp.MustCompile(`^https?://[a-zA-Z0-9.:@/?=&_-]+$`) // 允许 user:pass@ 与查询串（O2 basic auth）

// renderCadvisor cadvisor compose（挂载只读系统路径 + kmsg 设备）。
func renderCadvisor() string {
	return fmt.Sprintf(`services:
  cadvisor:
    image: %s
    container_name: custos-cadvisor
    restart: unless-stopped
    privileged: true
    ports:
      - "127.0.0.1:8081:8080" # 仅本机可抓取（O2 跨机抓取时自行改绑）
    volumes:
      - /:/rootfs:ro
      - /var/run:/var/run:ro
      - /sys:/sys:ro
      - /var/lib/docker/:/var/lib/docker:ro
      - /dev/disk/:/dev/disk:ro
    devices:
      - /dev/kmsg
`, cadvisorImage)
}

// renderVector vector compose + 采集配置（docker_logs 源 → O2 http sink）。
// vector.yaml 与 compose 同目录（部署固定目录），以相对路径挂载。
// O2 地址可内嵌 basic auth（http://user:pass@host/...），拆出后渲染进 sink 的
// auth 块（OpenObserve 默认开启 basic auth，无凭据会静默 401 丢日志）。
func renderVector(o2URL string) (composeYAML, vectorYAML string) {
	uri, authBlock := o2URL, ""
	if at := strings.LastIndex(o2URL, "@"); at > len("https://") && strings.Contains(o2URL[:at], ":") {
		schemeEnd := strings.Index(o2URL, "://") + 3
		creds := o2URL[schemeEnd:at]
		if sep := strings.Index(creds, ":"); sep > 0 {
			uri = o2URL[:schemeEnd] + o2URL[at+1:]
			authBlock = fmt.Sprintf(`    auth:
      strategy: basic
      user: %s
      password: %s
`, creds[:sep], creds[sep+1:])
		}
	}
	vectorYAML = fmt.Sprintf(`sources:
  docker_logs:
    type: docker_logs
sinks:
  o2:
    type: http
    inputs: [docker_logs]
    uri: %s
    method: post
    encoding:
      codec: json
    healthcheck:
      enabled: true
%s`, uri, authBlock)
	composeYAML = fmt.Sprintf(`services:
  vector:
    image: %s
    container_name: custos-vector
    restart: unless-stopped
    environment:
      VECTOR_LOG: warn
    volumes:
      - ./vector.yaml:/etc/vector/vector.yaml:ro
      - /var/run/docker.sock:/var/run/docker.sock:ro
`, vectorImage)
	return composeYAML, vectorYAML
}

// deployName 观测组件的隔离域名（resources.DeployComposeTo 落盘目录同名）。
func deployName(component string) string { return "custos-observ-" + component }

// ---- Service ----

// OpsNotifier 运维群推送出口（notify.Service 实现，app 层注入）：
// 观测组件部署/卸载失败不能静默。
type OpsNotifier interface {
	NotifyOps(ctx context.Context, title, detail string)
}

type Service struct {
	db       *gorm.DB
	res      *resources.Service
	notifier OpsNotifier // 可空
}

func NewService(db *gorm.DB, res *resources.Service) *Service {
	return &Service{db: db, res: res}
}

// SetNotifier 注入运维群推送出口。
func (s *Service) SetNotifier(n OpsNotifier) { s.notifier = n }

func (s *Service) notifyFailure(ctx context.Context, serverID uint, action string, err error, out string) {
	if s.notifier == nil {
		return
	}
	detail := fmt.Sprintf("服务器 ID：%d\n操作：%s\n错误：%v\n\n%s", serverID, action, err, truncateStr(out, 500))
	go func() { s.notifier.NotifyOps(context.WithoutCancel(ctx), "观测组件"+action+"失败", detail) }()
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...（截断）"
}

func (s *Service) setting(ctx context.Context, key string) (string, error) {
	var row struct{ Value string }
	err := s.db.WithContext(ctx).Table("platform_settings").
		Select("value").Where("key = ?", key).First(&row).Error
	if err != nil {
		return "", nil // 未配置
	}
	return row.Value, nil
}

func (s *Service) SetO2URL(ctx context.Context, url string) error {
	if url != "" && !urlRe.MatchString(url) {
		return errors.New("O2 地址须为 http(s)://...（含流路径，如 http://10.0.0.1:5080/api/default/custos/_json）")
	}
	return s.db.WithContext(ctx).Exec(
		`INSERT INTO platform_settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		settingO2URL, url).Error
}

func (s *Service) O2URL(ctx context.Context) string {
	v, _ := s.setting(ctx, settingO2URL)
	return v
}

// List 组件清单 + O2 地址（前端页面一次性拉取）。
func (s *Service) List(ctx context.Context) (map[string]any, error) {
	return map[string]any{
		"components": components,
		"o2Url":      s.O2URL(ctx),
	}, nil
}

// Deploy 部署/升级（换模板重部署即升级；先写 vector.yaml 再 up -d）。
func (s *Service) Deploy(ctx context.Context, serverID uint, component string) (string, error) {
	comp := componentOf(component)
	if comp == nil {
		return "", ErrBadComponent
	}
	var composeYAML, extraFile string
	switch component {
	case CompCadvisor:
		composeYAML = renderCadvisor()
	case CompVector:
		o2 := s.O2URL(ctx)
		if o2 == "" {
			return "", errors.New("请先配置 OpenObserve 日志接收地址（vector 输出目标）")
		}
		var vectorYAML string
		composeYAML, vectorYAML = renderVector(o2)
		extraFile = vectorYAML
	default:
		return "", ErrBadComponent
	}
	name := deployName(component)
	// vector.yaml 与 compose 同目录，先落盘（mkdir -p：首次部署时目录尚不存在）
	if extraFile != "" {
		cmd := fmt.Sprintf("mkdir -p %s/%s && cat > %s/%s/vector.yaml",
			"/opt/custos-machina/compose", name, "/opt/custos-machina/compose", name)
		if out, err := s.res.RunCommandOn(ctx, serverID, cmd, extraFile, 30*time.Second); err != nil {
			return out, fmt.Errorf("写入 vector 配置失败: %w", err)
		}
	}
	out, _, err := s.res.DeployComposeTo(ctx, serverID, name, composeYAML)
	s.res.RecordEvent(ctx, serverID, "observ_deploy",
		fmt.Sprintf("观测组件 %s 部署/升级（%s）：%s", component, comp.Image,
			map[bool]string{true: "成功", false: "失败"}[err == nil]))
	if err != nil {
		s.notifyFailure(ctx, serverID, "部署/"+component, err, out)
	}
	return out, err
}

// Uninstall 下线组件（down --remove-orphans；部署文件保留便于重装）。
func (s *Service) Uninstall(ctx context.Context, serverID uint, component string) (string, error) {
	if componentOf(component) == nil {
		return "", ErrBadComponent
	}
	out, err := s.res.DestroyCompose(ctx, serverID, deployName(component))
	s.res.RecordEvent(ctx, serverID, "observ_deploy",
		fmt.Sprintf("观测组件 %s 卸载：%s", component,
			map[bool]string{true: "成功", false: "失败"}[err == nil]))
	if err != nil {
		s.notifyFailure(ctx, serverID, "卸载/"+component, err, out)
	}
	return out, err
}

// Status 目标机上各组件的运行状态（按固定容器名探测）。
func (s *Service) Status(ctx context.Context, serverID uint) (map[string]string, error) {
	out, err := s.res.RunCommandOn(ctx, serverID,
		`docker ps -a --filter name=custos- --format '{{.Names}} {{.Status}}'`, "", 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("探测失败: %w\n%s", err, out)
	}
	status := map[string]string{CompCadvisor: "absent", CompVector: "absent"}
	for _, comp := range components {
		// 容器名固定为 custos-<component>（模板里 container_name 显式声明）
		needle := "custos-" + comp.Name + " "
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, needle) {
				if strings.Contains(line, "Up ") {
					status[comp.Name] = "running"
				} else {
					status[comp.Name] = "stopped"
				}
			}
		}
	}
	return status, nil
}

func componentOf(name string) *Component {
	for i := range components {
		if components[i].Name == name {
			return &components[i]
		}
	}
	return nil
}
