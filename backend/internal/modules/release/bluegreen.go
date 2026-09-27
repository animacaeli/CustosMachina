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

	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/pkg/logger"
	"github.com/custos-machina/backend/internal/pkg/projlock"
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
type Deployer interface {
	DeployComposeTo(ctx context.Context, serverID uint, name, yamlContent string) (string, string, error)
	WaitComposeHealthy(ctx context.Context, serverID uint, name string, timeout time.Duration) error
	DeployNginxConf(ctx context.Context, serverID uint, projName, content string) (string, error)
	DestroyCompose(ctx context.Context, serverID uint, name string) (string, error)
	RegistryLogin(ctx context.Context, serverID uint, registryAddr, username, password string) error
	RegistryLogout(ctx context.Context, serverID uint, registryAddr string)
}

// confRenderer 整份 nginx conf 单写者（*canary.Service 实现）。
type ConfRenderer interface {
	FullConf(ctx context.Context, projectID uint, activeColor string) (string, error)
}

// ActiveColor 实现 canary.ActiveColorGetter（canary 渲染 conf 时取活跃色）。
// 无记录返回 ""（未启用蓝绿，单域存量模式）。查询错误也返回 ""：
// 渲染侧宁可退回单域名（nginx -t 会拦截不存在的域名）也不能瞎猜颜色。
func (s *Service) ActiveColor(ctx context.Context, projectID uint) string {
	c, err := s.activeColorChecked(ctx, projectID)
	if err != nil {
		logger.Warnf("[release] 查询项目 %d 蓝绿状态失败（按未启用处理）: %v", projectID, err)
		return ""
	}
	return c
}

// activeColorChecked 严格版：DB 错误显式报错（发布链路用，错判"首次"
// 会对正在承载流量的活跃域原地重建 = 停机）。
func (s *Service) activeColorChecked(ctx context.Context, projectID uint) (string, error) {
	var st BGState
	err := s.db.WithContext(ctx).First(&st, "project_id = ?", projectID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil // 未启用蓝绿
	}
	if err != nil {
		return "", fmt.Errorf("查询蓝绿状态失败: %w", err)
	}
	return st.ActiveColor, nil
}

// ActiveDomainFor 实现 cron.DomainResolver：项目基础名 → 当前活跃的隔离域名。
// 启用蓝绿的项目返回 <norm>-prod-<活跃色>（任务与业务同版本同网络）；未启用/
// 项目不存在/查询错误一律原样返回（调用方拿原名执行，失败信息明确可排查）。
// 事后注入而非构造参数（app 层 SetDomainResolver），避免 cron↔release 构造环。
func (s *Service) ActiveDomainFor(ctx context.Context, baseName string) string {
	norm := strx.NormalizeName(baseName)
	var pid uint
	if err := s.db.WithContext(ctx).Table("projects").
		Select("id").Where("name IN ?", []string{baseName, norm}).
		Scan(&pid).Error; err != nil || pid == 0 {
		return baseName
	}
	var st BGState
	if err := s.db.WithContext(ctx).
		First(&st, "project_id = ?", pid).Error; err != nil {
		return baseName
	}
	if st.ActiveColor != ColorBlue && st.ActiveColor != ColorGreen {
		return baseName
	}
	logger.Infof("[release] 定时任务域名解析：%s → %s-prod-%s（跟随蓝绿活跃色）", baseName, norm, st.ActiveColor)
	return fmt.Sprintf("%s-prod-%s", norm, st.ActiveColor)
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

// executeBlueGreen 蓝绿发布主链路。失败语义分两段：
//   - conf 切换（步骤 3）成功前失败：销毁新颜色域，线上零影响；
//   - conf 切换成功后失败（仅 BGState 落库等极端情况）：流量已在新域上，
//     绝不能销毁新域——落 failed 记录注明现场，人工介入。
//
// 全程项目级互斥（与 canary.Publish 共锁）：conf 的写入时序必须串行化，
// 否则双发布/发布与灰度交错会互相销毁对方刚切好的域或写坏 conf。
func (s *Service) executeBlueGreen(ctx context.Context, p *projectRow, target *EnvTargetRow, in ReleaseInput, operator, yamlContent string) (*Release, error) {
	if s.canary == nil {
		return nil, errors.New("蓝绿发布依赖的配置渲染器未装配")
	}
	unlock := projlock.Lock(in.ProjectID)
	defer unlock()

	norm := strx.NormalizeName(p.Name)
	oldColor, err := s.activeColorChecked(ctx, in.ProjectID)
	if err != nil {
		return nil, err
	}
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
	// record 收尾：落库 + 通知（conf 切换后的失败也必须留痕，不能吞）
	record := func(status string) (*Release, error) {
		rel.Status = status
		rel.DurationSecs = int(time.Since(startedAt).Seconds())
		rel.Output = truncate(strings.Join(steps, "\n"), 8000)
		if cerr := s.db.WithContext(ctx).Create(&rel).Error; cerr != nil {
			logger.Errorf("[release] 项目 %d 发布记录落库失败: %v", in.ProjectID, cerr)
			return &rel, cerr
		}
		s.notifyRelease(ctx, p, &rel)
		return &rel, nil
	}
	fail := func(err error) (*Release, error) {
		// 仅在 conf 尚未切换到新域时才可安全销毁新域
		if out, derr := s.res.DestroyCompose(ctx, target.ServerID, newDomain); derr != nil {
			step("销毁新颜色域失败（可手动清理 %s）: %v\n%s", newDomain, derr, out)
		} else {
			step("已销毁未通过门禁的新颜色域 %s", newDomain)
		}
		step("发布失败: %v", err)
		_, _ = record(ReleaseFailed)
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

	// 3. 整份 conf 切换：渲染（upstream 直指新颜色域）→ 写入（备份 + nginx -t
	// + reload 任一失败远端脚本自动还原备份，磁盘回到旧 conf）→ 落库活跃色
	conf, err := s.canary.FullConf(ctx, in.ProjectID, newColor)
	if err != nil {
		return fail(fmt.Errorf("渲染 nginx 配置失败: %w", err))
	}
	step("切换 nginx conf → %s（写入 + nginx -t + reload，失败自动还原备份）", newColor)
	out, err = s.res.DeployNginxConf(ctx, target.ServerID, norm, conf)
	if err != nil {
		return fail(fmt.Errorf("nginx 切换失败（旧配置未受影响）: %w\n%s", err, out))
	}
	if err := s.db.WithContext(ctx).Save(&BGState{ProjectID: in.ProjectID, ActiveColor: newColor}).Error; err != nil {
		// 流量已切到健康的新域：绝不能销毁它。留痕人工修 DB 后即完全一致。
		step("严重：nginx 已切到 %s 且新域健康，但蓝绿状态落库失败: %v", newColor, err)
		step("新颜色域 %s 保留运行，请检查数据库后重试发布或手动修正 blue_green_states", newDomain)
		_, _ = record(ReleaseFailed)
		return &rel, fmt.Errorf("蓝绿状态落库失败（流量已在新域，未销毁）: %w", err)
	}

	// 4. drain 窗口：存量连接在旧颜色上跑完（长连接由客户端重连兜底）
	if oldColor != "" {
		step("drain %s：存量连接跑完（%s）", oldColor, drainWindow)
		time.Sleep(drainWindow)
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

	rel2, rerr := record(ReleaseSuccess)
	if rerr != nil {
		return rel2, rerr
	}
	return rel2, nil
}
