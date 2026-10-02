// Package observ 观测组件一键部署（第四阶段 M3 + 2026-09-28 UI 反馈增强）：
// 多采集器可选（指标：cadvisor/node-exporter；日志：vector/fluent-bit），
// compose 与采集器配置均可编辑覆盖（Monaco 编辑器编辑后随部署下发）。
// 语义对齐 K8s DaemonSet 的手动版：k3s 期把模板翻译成 DaemonSet 即可。
package observ

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/modules/notify"
	"github.com/custos-machina/backend/internal/modules/resources"
	"github.com/custos-machina/backend/internal/pkg/crypto"
)

var ErrBadComponent = errors.New("未知组件")

const (
	CategoryMetrics = "metrics"
	CategoryLogs    = "logs"

	settingO2URL = "observ.o2_url" // platform_settings 键：日志采集的输出目标

	cadvisorImage     = "gcr.m.daocloud.io/cadvisor/cadvisor:v0.49.1" // gcr.io 的 daocloud 代理（真机实测 gcr/docker hub 直连均不可达）
	nodeExporterImage = "prom/node-exporter:v1.8.2"                   // docker hub 热门镜像，mirror 可拉（真机实测同档镜像可达）
	vectorImage       = "timberio/vector:0.46.1-alpine"
	fluentBitImage    = "fluent/fluent-bit:3.1.4"
)

// ConfigFile 采集器的伴随配置文件（部署时写到隔离域目录，compose 相对路径挂载）。
type ConfigFile struct {
	Filename string `json:"filename"`
	Content  string `json:"content"`
}

// Component 可部署的采集器。
type Component struct {
	Name        string       `json:"name"`
	Category    string       `json:"category"` // metrics | logs
	Image       string       `json:"image"`
	Remark      string       `json:"remark"`
	NeedsO2URL  bool         `json:"needsO2Url"`  // 日志类：输出指向平台配置的 O2 地址
	ConfigFiles []ConfigFile `json:"configFiles"` // 默认伴随配置（可编辑覆盖）
	Compose     string       `json:"compose"`     // 默认 compose 模板（可编辑覆盖）
}

// 各组件默认模板。vector 管线参考 self-hosted 实践（/deploy/self-hosted）。
var components = []*Component{
	{
		Name:     "cadvisor",
		Category: CategoryMetrics,
		Image:    cadvisorImage,
		Remark: "容器指标采集（半弃维护，k3s 期被 kubelet cAdvisor 淘汰）；\n" +
			"宿主端口 8081 仅本机可抓取",
		Compose: fmt.Sprintf(`services:
  cadvisor:
    image: %s
    container_name: custos-cadvisor
    restart: unless-stopped
    privileged: true
    ports:
      - "127.0.0.1:8081:8080"
    volumes:
      - /:/rootfs:ro
      - /var/run:/var/run:ro
      - /sys:/sys:ro
      - /var/lib/docker/:/var/lib/docker:ro
      - /dev/disk/:/dev/disk:ro
    devices:
      - /dev/kmsg
`, cadvisorImage),
	},
	{
		Name:     "node-exporter",
		Category: CategoryMetrics,
		Image:    nodeExporterImage,
		Remark: "主机指标采集（CPU/内存/磁盘/网络；与 cadvisor 互补：\n" +
			"一个看容器层、一个看宿主机层）；宿主端口 9100 仅本机可抓取",
		Compose: fmt.Sprintf(`services:
  node-exporter:
    image: %s
    container_name: custos-node-exporter
    restart: unless-stopped
    ports:
      - "127.0.0.1:9100:9100"
    volumes:
      - /proc:/host/proc:ro
      - /sys:/host/sys:ro
      - /:/rootfs:ro
    command:
      - --path.procfs=/host/proc
      - --path.sysfs=/host/sys
      - --path.rootfs=/rootfs
`, nodeExporterImage),
	},
	{
		Name: "vector", Category: CategoryLogs, Image: vectorImage,
		Remark: "容器日志采集（docker.sock），输出指向平台配置的 OpenObserve 地址\n" +
			"（O2 地址支持 http://user:pass@host 内嵌 basic auth）；\n" +
			"vector.yaml 可自由定制管线（transform 路由、多源多出口等，参考 self-hosted 实践）",
		NeedsO2URL:  true,
		ConfigFiles: []ConfigFile{{Filename: "vector.yaml"}}, // 内容渲染时填充
		Compose: fmt.Sprintf(`services:
  vector:
    image: %s
    container_name: custos-vector
    restart: unless-stopped
    environment:
      VECTOR_LOG: warn
    volumes:
      - ./vector.yaml:/etc/vector/vector.yaml:ro
      - /var/run/docker.sock:/var/run/docker.sock:ro
`, vectorImage),
	},
	{
		Name: "fluent-bit", Category: CategoryLogs, Image: fluentBitImage,
		Remark: "容器日志采集（轻量替代 vector：内存占用更小）；\n" +
			"fluent-bit.conf 可自由定制（多输入/过滤器等）",
		NeedsO2URL:  true,
		ConfigFiles: []ConfigFile{{Filename: "fluent-bit.conf"}},
		Compose: fmt.Sprintf(`services:
  fluent-bit:
    image: %s
    container_name: custos-fluent-bit
    restart: unless-stopped
    volumes:
      - ./fluent-bit.conf:/fluent-bit/etc/fluent-bit.conf:ro
      - /var/run/docker.sock:/var/run/docker.sock:ro
`, fluentBitImage),
	},
}

// renderVectorYAML vector.yaml 默认模板（docker_logs 源 → O2 http sink；
// O2 地址可内嵌 basic auth，拆出渲染进 auth 块）。
func renderVectorYAML(o2URL string) string {
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
	return fmt.Sprintf(`sources:
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
}

// renderFluentBitConf fluent-bit.conf 默认模板（docker 输入 → O2 http json_lines 输出）。
func renderFluentBitConf(o2URL string) string {
	uri, user, pass := o2URL, "", ""
	if at := strings.LastIndex(o2URL, "@"); at > len("https://") && strings.Contains(o2URL[:at], ":") {
		schemeEnd := strings.Index(o2URL, "://") + 3
		creds := o2URL[schemeEnd:at]
		if sep := strings.Index(creds, ":"); sep > 0 {
			uri = o2URL[:schemeEnd] + o2URL[at+1:]
			user, pass = creds[:sep], creds[sep+1:]
		}
	}
	auth := ""
	if user != "" {
		auth = fmt.Sprintf("    http_User %s\n    http_Passwd %s\n", user, pass)
	}
	return fmt.Sprintf(`[SERVICE]
    Flush 5
    Daemon Off
    Log_Level warn

[INPUT]
    Name docker
    Dockerd unix:///var/run/docker.sock

[OUTPUT]
    Name http
    Match *
    URI %s
    Format json_lines
    json_date_key timestamp
    json_date_format iso8601
%s    Retry_Limit 3
`, uri, auth)
}

// componentDefaults 填充组件默认配置内容（日志类按当前 O2 地址渲染）。
func componentDefaults(ctx context.Context, s *Service, c *Component) {
	for i, cf := range c.ConfigFiles {
		switch {
		case c.Name == "vector" && cf.Filename == "vector.yaml":
			c.ConfigFiles[i].Content = renderVectorYAML(s.O2URL(ctx))
		case c.Name == "fluent-bit" && cf.Filename == "fluent-bit.conf":
			c.ConfigFiles[i].Content = renderFluentBitConf(s.O2URL(ctx))
		}
	}
}

// containerName 各组件的固定容器名（状态探测/命名约定）。
func containerName(component string) string {
	switch component {
	case "cadvisor":
		return "custos-cadvisor"
	case "node-exporter":
		return "custos-node-exporter"
	case "vector":
		return "custos-vector"
	case "fluent-bit":
		return "custos-fluent-bit"
	}
	return "custos-" + component
}

// deployName 观测组件的隔离域名（resources.DeployComposeTo 落盘目录同名）。
func deployName(component string) string { return "custos-observ-" + component }

// ---- Service ----

// EventNotifier 统一通知路由出口（notify.Service 实现，app 层注入）：
// 观测组件部署/卸载失败不能静默。
type EventNotifier interface {
	NotifyEvent(ctx context.Context, source, level, dedupKey, title, detail string)
}

type Service struct {
	db        *gorm.DB
	res       *resources.Service
	notifier  EventNotifier // 可空
	digestor  Digestor      // 可空：AI 诊断摘要（P5 M6）
	cipher    *crypto.Cipher
	publicURL string // 平台对外地址（CUSTOS_IM_PUBLIC_URL，webhook 回流用）
}

func NewService(db *gorm.DB, res *resources.Service, cipher *crypto.Cipher) *Service {
	return &Service{db: db, res: res, cipher: cipher}
}

// SetPublicURL 注入平台对外地址（app 层装配，与 IM 回调同一配置）。
func (s *Service) SetDigestor(d Digestor) { s.digestor = d }
func (s *Service) SetPublicURL(u string)  { s.publicURL = u }

// SetNotifier 注入运维群推送出口。
func (s *Service) SetNotifier(n EventNotifier) { s.notifier = n }

func (s *Service) notifyFailure(ctx context.Context, serverID uint, action string, err error, out string) {
	if s.notifier == nil {
		return
	}
	detail := fmt.Sprintf("服务器 ID：%d\n操作：%s\n错误：%v\n\n%s", serverID, action, err, truncateStr(out, 500))
	go func() {
		s.notifier.NotifyEvent(context.WithoutCancel(ctx),
			notify.SourceObservFailed, notify.LevelWarn,
			fmt.Sprintf("server-%d-%s", serverID, action), "观测组件"+action+"失败", detail)
	}()
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

// List 组件清单（含默认模板，前端预填编辑器）+ O2 地址，按类别分组排序。
func (s *Service) List(ctx context.Context) (map[string]any, error) {
	out := make([]*Component, 0, len(components))
	for _, c := range components {
		cc := *c
		cc.ConfigFiles = append([]ConfigFile(nil), c.ConfigFiles...)
		componentDefaults(ctx, s, &cc)
		out = append(out, &cc)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category == CategoryMetrics
		}
		return out[i].Name < out[j].Name
	})
	return map[string]any{
		"components": out,
		"o2Url":      s.O2URL(ctx),
	}, nil
}

var urlRe = regexp.MustCompile(`^https?://[a-zA-Z0-9.:@/?=&_-]+$`) // 允许 user:pass@ 与查询串（O2 basic auth）

// DeployInput 部署入参：模板均可覆盖（前端 Monaco 编辑后回传；空 = 用默认）。
type DeployInput struct {
	ServerID    uint              `json:"serverId" binding:"required"`
	Component   string            `json:"component" binding:"required"`
	Compose     string            `json:"compose"`     // 覆盖 compose（空 = 默认模板）
	ConfigFiles map[string]string `json:"configFiles"` // 覆盖伴随配置（filename → content）
}

func componentOf(name string) *Component {
	for _, c := range components {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// Deploy 部署/升级（换模板重部署即升级；先写伴随配置再 up -d）。
func (s *Service) Deploy(ctx context.Context, in DeployInput) (string, error) {
	comp := componentOf(in.Component)
	if comp == nil {
		return "", ErrBadComponent
	}
	composeYAML := in.Compose
	if strings.TrimSpace(composeYAML) == "" {
		composeYAML = comp.Compose
	}
	if comp.NeedsO2URL && s.O2URL(ctx) == "" {
		return "", errors.New("请先配置 OpenObserve 日志接收地址（日志采集的输出目标）")
	}
	// 伴随配置：默认模板 + 用户覆盖合并
	files := map[string]string{}
	var defaults Component = *comp
	componentDefaults(ctx, s, &defaults)
	for _, cf := range defaults.ConfigFiles {
		files[cf.Filename] = cf.Content
	}
	for name, content := range in.ConfigFiles {
		if strings.TrimSpace(content) != "" {
			if !safeFilename(name) {
				return "", fmt.Errorf("非法的配置文件名 %q", name)
			}
			files[name] = content
		}
	}
	name := deployName(in.Component)
	// 配置文件与 compose 同目录（相对路径挂载依赖部署固定目录约定）
	for fname, content := range files {
		cmd := fmt.Sprintf("mkdir -p /opt/custos-machina/compose/%s && cat > /opt/custos-machina/compose/%s/%s",
			name, name, fname)
		if out, err := s.res.RunCommandOn(ctx, in.ServerID, cmd, content, 30*time.Second); err != nil {
			return out, fmt.Errorf("写入 %s 失败: %w", fname, err)
		}
	}
	out, _, err := s.res.DeployComposeTo(ctx, in.ServerID, name, composeYAML)
	s.res.RecordEvent(ctx, in.ServerID, "observ_deploy",
		fmt.Sprintf("观测组件 %s 部署/升级（%s）：%s", in.Component, comp.Image,
			map[bool]string{true: "成功", false: "失败"}[err == nil]))
	if err != nil {
		s.notifyFailure(ctx, in.ServerID, "部署/"+in.Component, err, out)
	}
	return out, err
}

// safeFilename 伴随配置文件名白名单（拼进 shell 前防注入）。
var filenameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

func safeFilename(name string) bool {
	return filenameRe.MatchString(name) && !strings.Contains(name, "..")
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
	status := map[string]string{}
	for _, c := range components {
		status[c.Name] = "absent"
	}
	for _, c := range components {
		needle := containerName(c.Name) + " "
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, needle) {
				if strings.Contains(line, "Up ") {
					status[c.Name] = "running"
				} else {
					status[c.Name] = "stopped"
				}
			}
		}
	}
	return status, nil
}
