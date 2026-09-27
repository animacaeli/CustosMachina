// bluegreen.go 蓝绿发布（第四阶段 M2，docs/plan-phase4-runtime.md 第三节第 2 项）。
// 流程：部署新颜色域 → 健康门禁（失败中止，线上零影响）→ 整份 conf 切换
// （nginx -t 通过才 reload，失败自动还原备份）→ drain 窗口 → 销毁旧颜色域。
// 隔离域命名 <proj>-prod-blue / <proj>-prod-green；非活跃色容器不存在时其域名
// 不能出现在 conf（nginx -t 会失败），故 conf 只渲染活跃色 upstream。
package release

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/custos-machina/backend/internal/pkg/strx"
)

const (
	ColorBlue  = "blue"
	ColorGreen = "green"

	// healthGateTimeout 健康门禁超时：compose healthcheck 的 interval×retries
	// 通常 30~60s，3 分钟足够覆盖慢启动服务
	healthGateTimeout = 3 * time.Minute
)

// drainWindow 存量连接在旧颜色上跑完的窗口（计划默认 30s；长连接服务
// 依赖客户端重连，见接入规范）。var 而非 const：测试缩短。
var drainWindow = 30 * time.Second

// deployer 蓝绿链路需要的部署能力（*resources.Service 实现；接口化便于测试）。
type deployer interface {
	DeployComposeTo(ctx context.Context, serverID uint, name, yamlContent string) (string, string, error)
	WaitComposeHealthy(ctx context.Context, serverID uint, name string, timeout time.Duration) error
	DeployNginxConf(ctx context.Context, serverID uint, projName, content string) (string, error)
	DestroyCompose(ctx context.Context, serverID uint, name string) (string, error)
	RegistryLogin(ctx context.Context, serverID uint, registryAddr, username, password string) error
	RegistryLogout(ctx context.Context, serverID uint, registryAddr string)
}

// confRenderer 整份 nginx conf 单写者（*canary.Service 实现）。
type confRenderer interface {
	FullConf(ctx context.Context, projectID uint, activeColor string) (string, error)
}

// ActiveColor 实现 canary.ActiveColorGetter（canary 渲染 conf 时取活跃色）。
// 无记录返回 ""（未启用蓝绿，单域存量模式）。
func (s *Service) ActiveColor(ctx context.Context, projectID uint) string {
	var st BGState
	if err := s.db.WithContext(ctx).First(&st, "project_id = ?", projectID).Error; err != nil {
		return ""
	}
	return st.ActiveColor
}

func oppositeColor(c string) string {
	if c == ColorBlue {
		return ColorGreen
	}
	return ColorBlue // 无状态（首次）从 blue 起
}

func (s *Service) colorDomain(projName, color string) string {
	return fmt.Sprintf("%s-prod-%s", projName, color)
}

// executeBlueGreen 蓝绿发布主链路。任一步失败：销毁新颜色域、落 failed 记录、
// 线上始终跑旧颜色（首发的失败则是"什么都没上过线"）。
func (s *Service) executeBlueGreen(ctx context.Context, p *projectRow, target *EnvTargetRow, in ReleaseInput, operator, yamlContent string) (*Release, error) {
	if s.canary == nil {
		return nil, errors.New("蓝绿发布依赖的配置渲染器未装配")
	}
	norm := strx.NormalizeName(p.Name)
	oldColor := s.ActiveColor(ctx, in.ProjectID)
	newColor := oppositeColor(oldColor)
	newDomain := s.colorDomain(norm, newColor)

	rel := Release{
		ProjectID: in.ProjectID, EnvType: in.EnvType, Tag: in.Tag,
		ServerID: target.ServerID, Runtime: "compose",
		ReleaseBy: operator, Status: ReleaseFailed, Color: newColor,
	}
	startedAt := time.Now()
	var steps []string
	step := func(format string, args ...any) {
		steps = append(steps, time.Now().Format("15:04:05")+" "+fmt.Sprintf(format, args...))
	}
	fail := func(err error) (*Release, error) {
		// 新颜色域起不来或切换失败：销毁新域（容器可能半起），线上零影响
		if out, derr := s.res.DestroyCompose(ctx, target.ServerID, newDomain); derr != nil {
			step("销毁新颜色域失败（可手动清理 %s）: %v\n%s", newDomain, derr, out)
		} else {
			step("已销毁未通过门禁的新颜色域 %s", newDomain)
		}
		rel.DurationSecs = int(time.Since(startedAt).Seconds())
		rel.Output = truncate(strings.Join(steps, "\n"), 8000) + "\n发布失败: " + err.Error()
		if cerr := s.db.WithContext(ctx).Create(&rel).Error; cerr != nil {
			return nil, cerr
		}
		s.notifyRelease(ctx, p, &rel)
		return &rel, err
	}

	// 1. 部署新颜色域（先起容器再写 conf：upstream 引用的容器名必须已可解析）
	step("部署新颜色域 %s（标签 %s）", newDomain, in.Tag)
	out, _, err := s.res.DeployComposeTo(ctx, target.ServerID, newDomain, yamlContent)
	if err != nil {
		step("部署输出:\n%s", out)
		return fail(fmt.Errorf("新颜色域部署失败: %w", err))
	}

	// 2. 健康门禁：全部服务 running 且（有 healthcheck 的）healthy 才继续
	step("健康门禁：等待 %s 全部服务就绪（最长 %s）", newDomain, healthGateTimeout)
	if err := s.res.WaitComposeHealthy(ctx, target.ServerID, newDomain, healthGateTimeout); err != nil {
		return fail(fmt.Errorf("健康门禁未通过: %w", err))
	}

	// 3. 整份 conf 切换：渲染（upstream 直指新颜色域）→ nginx -t 通过才 reload
	conf, err := s.canary.FullConf(ctx, in.ProjectID, newColor)
	if err != nil {
		return fail(fmt.Errorf("渲染 nginx 配置失败: %w", err))
	}
	step("切换 nginx conf → %s（写入 + nginx -t + reload）", newColor)
	out, err = s.res.DeployNginxConf(ctx, target.ServerID, norm, conf)
	if err != nil {
		return fail(fmt.Errorf("nginx 切换失败（旧配置未受影响）: %w\n%s", err, out))
	}
	if err := s.db.WithContext(ctx).Save(&BGState{ProjectID: in.ProjectID, ActiveColor: newColor}).Error; err != nil {
		return fail(fmt.Errorf("蓝绿状态落库失败: %w", err))
	}

	// 4. drain 窗口：存量连接在旧颜色上跑完（长连接由客户端重连兜底）
	if oldColor != "" {
		step("drain %s：存量连接跑完（%s）", oldColor, drainWindow)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(drainWindow):
		}
		// 5. 销毁旧颜色域
		oldDomain := s.colorDomain(norm, oldColor)
		if out, err := s.res.DestroyCompose(ctx, target.ServerID, oldDomain); err != nil {
			step("警告：旧颜色域 %s 销毁失败（不影响流量，可手动清理）: %v\n%s", oldDomain, err, out)
		} else {
			step("已销毁旧颜色域 %s", oldDomain)
		}
	} else {
		// 首次蓝绿：存量单域 <proj>-prod 不自动销毁（无法确认是否为他用），提示手动下线
		step("首次蓝绿：原单域 %s-prod 已不承载流量，确认后可手动下线", norm)
	}

	rel.Status = ReleaseSuccess
	rel.DurationSecs = int(time.Since(startedAt).Seconds())
	rel.Output = truncate(strings.Join(steps, "\n"), 8000)
	if err := s.db.WithContext(ctx).Create(&rel).Error; err != nil {
		return nil, err
	}
	s.notifyRelease(ctx, p, &rel)
	return &rel, nil
}
