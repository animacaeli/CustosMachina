package resources

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	composeloader "github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/types"
	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/pkg/httpx"
)

// 环境探测 + 安装引导 + compose 部署（M4）。

type envProbe struct {
	Distro        string `json:"distro"` // centos / ubuntu / debian / 其他原样
	DistroLike    string `json:"distroLike"`
	DockerVersion string `json:"dockerVersion"` // 空 = 未安装/不可用
	ComposeVer    string `json:"composeVer"`    // compose 插件版本，空 = 无
	DockerErr     string `json:"dockerErr"`
	ComposeErr    string `json:"composeErr"`
	Ready         bool   `json:"ready"` // docker + compose 都可用
}

var distroRe = regexp.MustCompile(`(?m)^ID=["']?([a-z0-9_.-]+)["']?`)
var likeRe = regexp.MustCompile(`(?m)^ID_LIKE=["']?([a-z0-9_. -]+)["']?`)

// probeEnv SSH 探测目标机 Docker 环境与发行版。
func (h *Handler) probeEnv(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	srv, cred, err := h.svc.serverWithCredential(c.Request.Context(), id)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	var p envProbe
	if out, err := sshRunOutput(srv, cred, "cat /etc/os-release", 10*time.Second); err == nil {
		if m := distroRe.FindStringSubmatch(out); m != nil {
			p.Distro = m[1]
		}
		if m := likeRe.FindStringSubmatch(out); m != nil {
			p.DistroLike = m[1]
		}
	}
	if out, err := sshRunOutput(srv, cred, "docker version --format {{.Server.Version}}", 15*time.Second); err != nil {
		p.DockerErr = strings.TrimSpace(out + " " + err.Error())
	} else {
		p.DockerVersion = strings.TrimSpace(out)
	}
	if out, err := sshRunOutput(srv, cred, "docker compose version --short", 10*time.Second); err != nil {
		p.ComposeErr = strings.TrimSpace(out + " " + err.Error())
	} else {
		p.ComposeVer = strings.TrimSpace(out)
	}
	p.Ready = p.DockerVersion != "" && p.ComposeVer != ""
	httpx.OK(c, p)
}

// installGuides 按发行版的安装步骤（配置化文案，便于随版本更新；只展示不代执行）。
var installGuides = map[string]string{
	"centos": `# CentOS / RHEL（dnf/yum）
sudo dnf -y install dnf-plugins-core
sudo dnf config-manager --add-repo https://download.docker.com/linux/centos/docker-ce.repo
sudo dnf -y install docker-ce docker-ce-cli containerd.io docker-compose-plugin
sudo systemctl enable --now docker
sudo usermod -aG docker $USER  # 重新登录生效`,
	"rhel": `# RHEL（dnf/yum）
sudo dnf -y install dnf-plugins-core
sudo dnf config-manager --add-repo https://download.docker.com/linux/rhel/docker-ce.repo
sudo dnf -y install docker-ce docker-ce-cli containerd.io docker-compose-plugin
sudo systemctl enable --now docker
sudo usermod -aG docker $USER`,
	"ubuntu": `# Ubuntu（apt）
sudo apt-get update
sudo apt-get -y install ca-certificates curl
sudo install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo $VERSION_CODENAME) stable" | sudo tee /etc/apt/sources.list.d/docker.list
sudo apt-get update
sudo apt-get -y install docker-ce docker-ce-cli containerd.io docker-compose-plugin
sudo usermod -aG docker $USER`,
	"debian": `# Debian（apt）
sudo apt-get update
sudo apt-get -y install ca-certificates curl
sudo install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/debian/gpg | sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/debian $(. /etc/os-release && echo $VERSION_CODENAME) stable" | sudo tee /etc/apt/sources.list.d/docker.list
sudo apt-get update
sudo apt-get -y install docker-ce docker-ce-cli containerd.io docker-compose-plugin
sudo usermod -aG docker $USER`,
}

const genericGuide = `# 未识别的发行版，参考官方文档安装：
# https://docs.docker.com/engine/install/
# 通用脚本（curl 可达外网时）：
curl -fsSL https://get.docker.com | sh
sudo systemctl enable --now docker
sudo apt-get -y install docker-compose-plugin || sudo dnf -y install docker-compose-plugin
sudo usermod -aG docker $USER`

func (h *Handler) installGuide(c *gin.Context) {
	distro := strings.ToLower(c.Param("distro"))
	guide, hit := installGuides[distro]
	// ubuntu 指南对 mint/pop 等衍生版同样适用
	if !hit && strings.Contains(distro, "ubuntu") {
		guide, hit = installGuides["ubuntu"], true
	}
	if !hit {
		guide = genericGuide
	}
	httpx.OK(c, gin.H{"distro": distro, "guide": guide})
}

type deployInput struct {
	YAML string `json:"yaml" binding:"required"`
	// 项目名：决定目标机落盘目录 /opt/custos-machina/compose/<name>/compose.yaml。
	// 缺省时后端从 YAML 的顶层 name 字段取；都没有则报错要求前端补填。
	Name string `json:"name" binding:"omitempty,max=64"`
}

// deployRoot 新建部署的固定根目录：/opt 是第三方应用部署惯例位置，持久且不随重启清空
// （此前的 /tmp 临时目录重启即丢，"查看部署文件"也对不上）。
const deployRoot = "/opt/custos-machina/compose"

var deployNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)

// deployCompose 校验 YAML（compose-go）→ 落盘目标机固定目录 → docker compose up -d。
// 第一期同步执行 + 返回全部输出（计划：事件流后置）。
func (h *Handler) deployCompose(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in deployInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	// 后端语义校验（前端已做语法校验，以后端为准）
	project, err := composeloader.LoadWithContext(c.Request.Context(), types.ConfigDetails{
		WorkingDir: ".",
		ConfigFiles: []types.ConfigFile{{
			Filename: "compose.yaml", Content: []byte(in.YAML),
		}},
	})
	if err != nil {
		httpx.FailBadRequest(c, fmt.Sprintf("compose 文件校验失败: %v", err))
		return
	}
	name := strings.ToLower(strings.TrimSpace(in.Name))
	if name == "" {
		name = strings.ToLower(project.Name)
	}
	if !deployNameRe.MatchString(name) {
		httpx.FailBadRequest(c, "项目名须为小写字母/数字开头，可含 _.-（用于生成部署目录）")
		return
	}
	srv, cred, err := h.svc.serverWithCredential(c.Request.Context(), id)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	// 固定目录：同名重新部署 = 覆盖更新（旧文件自动备份）；目录名有白名单，直接拼接安全
	dir := deployRoot + "/" + name
	const deployCmd = `mkdir -p %q && [ -f %q/compose.yaml ] && cp %q/compose.yaml %q/compose.yaml.bak.$(date +%%Y%%m%%d%%H%%M%%S) || true; ` +
		`cat > %q/compose.yaml && ` +
		`cd %q && docker compose -p %q -f compose.yaml up -d --remove-orphans 2>&1; ` +
		`rc=$?; docker compose -p %q -f compose.yaml ps 2>&1; exit $rc`
	cmd := fmt.Sprintf(deployCmd, dir, dir, dir, dir, dir, dir, name, name)
	out, err := sshRunOutputWithStdin(srv, cred, cmd, in.YAML, 3*time.Minute)
	success := err == nil
	h.svc.recordSimpleEvent(c.Request.Context(), srv.ID, "compose_deploy",
		fmt.Sprintf("%s 部署 compose 项目 %s 到 %s：%s", h.operator(c), name, srv.Host,
			map[bool]string{true: "成功", false: "失败"}[success]))
	if !success {
		httpx.FailUpstream(c, fmt.Sprintf("部署失败：\n%s\n%v", out, err))
		return
	}
	httpx.OK(c, gin.H{"output": out, "dir": dir})
}

// ---- 存量 compose 项目管理（M4.5）：查看/编辑部署文件、重建容器 ----

// compose 标签里的路径来自 docker daemon（部署时用户自己传入），
// 拼进 shell 前仍做白名单校验 + 严格引号转义，双保险防注入。
func validateComposePath(p string) error {
	if p == "" || !strings.HasPrefix(p, "/") {
		return fmt.Errorf("非法的部署文件路径（须为绝对路径）: %q", p)
	}
	if !strings.HasSuffix(p, ".yml") && !strings.HasSuffix(p, ".yaml") {
		return fmt.Errorf("部署文件须为 .yml/.yaml: %q", p)
	}
	return nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

var projectNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// composeFile GET /server-compose/:id/file?path= 读取远端部署文件。
func (h *Handler) composeFile(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	path := c.Query("path")
	if err := validateComposePath(path); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	srv, cred, err := h.svc.serverWithCredential(c.Request.Context(), id)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	content, err := sshRunOutput(srv, cred, "cat "+shellQuote(path), 10*time.Second)
	if err != nil {
		httpx.FailUpstream(c, fmt.Sprintf("读取失败（文件可能已被移动）: %v\n%s", err, content))
		return
	}
	httpx.OK(c, gin.H{"path": path, "content": content})
}

type composeFileInput struct {
	Path    string `json:"path" binding:"required"`
	Content string `json:"content" binding:"required"`
}

// saveComposeFile PUT /server-compose/:id/file 覆盖写远端部署文件（先备份）。
func (h *Handler) saveComposeFile(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in composeFileInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if err := validateComposePath(in.Path); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	// 校验时注入占位项目名：存量部署文件大多没有顶层 name 字段，
	// compose-go 会以 "project name must not be empty" 拒绝——编辑场景项目名无意义
	if _, err := composeloader.LoadWithContext(c.Request.Context(), types.ConfigDetails{
		WorkingDir: ".",
		ConfigFiles: []types.ConfigFile{{
			Filename: "compose.yaml", Content: []byte(in.Content),
		}},
	}, func(o *composeloader.Options) { o.SetProjectName("custos-validate", true) }); err != nil {
		httpx.FailBadRequest(c, fmt.Sprintf("compose 文件校验失败: %v", err))
		return
	}
	srv, cred, err := h.svc.serverWithCredential(c.Request.Context(), id)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	q := shellQuote(in.Path)
	// 备份 best-effort（文件不存在时不阻塞首次写入），写失败由 exit code 兜底
	cmd := fmt.Sprintf("[ -f %s ] && cp %s %s.bak.$(date +%%Y%%m%%d%%H%%M%%S) || true; cat > %s", q, q, q, q)
	out, err := sshRunOutputWithStdin(srv, cred, cmd, in.Content, 15*time.Second)
	if err != nil {
		httpx.FailUpstream(c, fmt.Sprintf("写入失败: %v\n%s", err, out))
		return
	}
	h.svc.recordSimpleEvent(c.Request.Context(), srv.ID, "compose_edit",
		fmt.Sprintf("%s 修改部署文件 %s", h.operator(c), in.Path))
	httpx.OK(c, gin.H{"message": "已保存（原文件已备份为 .bak.时间戳）"})
}

type recreateInput struct {
	Path    string `json:"path" binding:"required"`
	Project string `json:"project" binding:"required"`
}

// recreateCompose POST /server-compose/:id/recreate 按部署文件强制重建项目容器。
func (h *Handler) recreateCompose(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in recreateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if err := validateComposePath(in.Path); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if !projectNameRe.MatchString(in.Project) {
		httpx.FailBadRequest(c, "非法的项目名")
		return
	}
	srv, cred, err := h.svc.serverWithCredential(c.Request.Context(), id)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	cmd := fmt.Sprintf(
		"cd %s && docker compose -p %s -f %s up -d --force-recreate --remove-orphans 2>&1; "+
			"rc=$?; docker compose -p %s -f %s ps 2>&1; exit $rc",
		shellQuote(filepath.Dir(in.Path)), shellQuote(in.Project), shellQuote(in.Path),
		shellQuote(in.Project), shellQuote(in.Path))
	out, err := sshRunOutputWithStdin(srv, cred, cmd, "", 3*time.Minute)
	success := err == nil
	h.svc.recordSimpleEvent(c.Request.Context(), srv.ID, "compose_deploy",
		fmt.Sprintf("%s 重建 compose 项目 %s（%s）：%s", h.operator(c), in.Project, in.Path,
			map[bool]string{true: "成功", false: "失败"}[success]))
	if !success {
		httpx.FailUpstream(c, fmt.Sprintf("重建失败：\n%s\n%v", out, err))
		return
	}
	httpx.OK(c, gin.H{"output": out})
}
