package slots

import (
	"context"
	"errors"
	"fmt"
	"github.com/custos-machina/backend/internal/modules/configs"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/modules/ci"
	"github.com/custos-machina/backend/internal/modules/notify"
	"github.com/custos-machina/backend/internal/modules/projects"
	releasemod "github.com/custos-machina/backend/internal/modules/release"
	"github.com/custos-machina/backend/internal/modules/resources"
	"github.com/custos-machina/backend/internal/pkg/jobs"
	"github.com/custos-machina/backend/internal/pkg/logger"
	"github.com/custos-machina/backend/internal/pkg/strx"
)

var ErrNotFound = errors.New("槽位不存在")
var ErrOccupied = errors.New("槽位已被占用")

type Service struct {
	db     *gorm.DB
	ci     *ci.Service
	res    *resources.Service
	notify *notify.Service
	proj   projects.Reader // 只读投影，替代 Table("projects") 直读
	tokens TokenVerifier   // CI 回调鉴权（可空=回调未装配）

	// push 重建风暴防护：同槽位串行执行，正在跑时只记 pending、
	// 跑完补跑一次（部署幂等——按分支最新提交，连续 push 合并为最后一次）
	rebuildMu      sync.Mutex
	rebuildRunning map[uint]bool
	rebuildPending map[uint]*rebuildReq
}

type rebuildReq struct {
	p        *projectRow
	slotID   uint
	slot     Slot
	source   string
	imageTag string // CI 回调携带的测试镜像 tag（空=compose 原文，兼容存量）
}

func NewService(db *gorm.DB, ciSvc *ci.Service, resSvc *resources.Service, ntfy *notify.Service,
	proj projects.Reader, tokens TokenVerifier) *Service {
	// 存量库迁移兜底：清理历史软删的占用行，避免 (project_id, slot_name)
	// 唯一索引创建失败（占用行本就是临时状态，审计在事件表）
	db.Unscoped().Where("deleted_at IS NOT NULL").Delete(&Slot{})
	return &Service{
		db: db, ci: ciSvc, res: resSvc, notify: ntfy, proj: proj, tokens: tokens,
		rebuildRunning: map[uint]bool{}, rebuildPending: map[uint]*rebuildReq{},
	}
}

// TokenVerifier 应用级令牌校验（configs.Service 实现，wire 注入）——
// CI 回调鉴权复用配置拉取令牌（限定 app×env=test，可即时吊销）。
type TokenVerifier interface {
	VerifyPullToken(ctx context.Context, plaintext, app, env string) (*configs.PullToken, error)
}

// projectRow 只读所需列。
type projectRow struct {
	ID                uint
	Name              string
	RepoPath          string
	Provider          string
	ComposePath       string
	TestSlotCount     int
	SlotGraceDays     int
	NotifyTestGroupID *uint
}

func (s *Service) project(ctx context.Context, id uint) (*projectRow, error) {
	v, err := s.proj.ViewByID(ctx, id)
	if err != nil {
		return nil, errors.New("项目不存在")
	}
	return viewToRow(v), nil
}

// viewToRow projects.View → 本模块投影。
func viewToRow(v *projects.View) *projectRow {
	return &projectRow{
		ID: v.ID, Name: v.Name, RepoPath: v.RepoPath, Provider: v.Provider, ComposePath: v.ComposePath,
		TestSlotCount: v.TestSlotCount, SlotGraceDays: v.SlotGraceDays,
		NotifyTestGroupID: v.NotifyTestGroupID,
	}
}

func (s *Service) testTarget(ctx context.Context, projectID uint) (uint, error) {
	var t struct{ ServerID uint }
	if err := s.db.WithContext(ctx).Table("project_env_targets").
		Select("server_id").
		Where("project_id = ? AND env_type = ?", projectID, "test").
		First(&t).Error; err != nil {
		return 0, errors.New("项目未配置测试环境的部署目标")
	}
	return t.ServerID, nil
}

// ---- 占用 / 释放 / 续期 ----

type OccupyInput struct {
	SlotName      string `json:"slotName" binding:"required,max=32"`
	Branch        string `json:"branch" binding:"required,max=128"`
	DurationValue int    `json:"durationValue" binding:"required,min=1"`
	DurationUnit  string `json:"durationUnit" binding:"required,oneof=hours days weeks"`
}

type durationLike interface{ duration() time.Duration }

func parseDuration(value int, unit string) time.Duration {
	base := time.Duration(value)
	switch unit {
	case "days":
		return base * 24 * time.Hour
	case "weeks":
		return base * 7 * 24 * time.Hour
	default:
		return base * time.Hour
	}
}

func (d OccupyInput) duration() time.Duration { return parseDuration(d.DurationValue, d.DurationUnit) }
func (d DurationInput) duration() time.Duration {
	return parseDuration(d.DurationValue, d.DurationUnit)
}

var _ = []durationLike{OccupyInput{}, DurationInput{}}

// Occupy 占用槽位并立即拉起该分支的测试环境。
func (s *Service) Occupy(ctx context.Context, projectID uint, in OccupyInput, uid uint, display string) (*Slot, error) {
	p, err := s.project(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if slotIndex(in.SlotName) > p.TestSlotCount {
		return nil, fmt.Errorf("槽位名须为 devN 且不超过项目配置的 %d 个", p.TestSlotCount)
	}
	// 槽位独占：已被占用则拒绝
	var cnt int64
	s.db.WithContext(ctx).Model(&Slot{}).
		Where("project_id = ? AND slot_name = ? AND status IN ?", projectID, in.SlotName, []string{StatusOccupied, StatusExpired}).
		Count(&cnt)
	if cnt > 0 {
		return nil, ErrOccupied
	}

	now := time.Now()
	slot := Slot{
		ProjectID: projectID, SlotName: in.SlotName, Branch: in.Branch,
		OccupiedBy: display, OccupiedByUID: uid,
		OccupiedAt: now, Duration: in.duration(),
		ExpireAt: now.Add(in.duration()), Status: StatusOccupied,
	}
	if err := s.db.WithContext(ctx).Create(&slot).Error; err != nil {
		return nil, err
	}
	// 2026-11-08 命名统一：槽位部署由 CI 回调触发（质检+镜像就绪才部署），
	// 占用不再立即拉起——推一次分支触发 CI 即自动部署首个环境
	s.notifySlot(ctx, p, &slot, fmt.Sprintf("槽位 %s 已占用（分支 %s）：等待首次 CI 构建回调后自动部署", slot.SlotName, slot.Branch))
	return &slot, nil
}

// Release 释放槽位：销毁隔离域容器，（槽位配置由外部配置中心管理，平台不再持有），删除占用行。
func (s *Service) Release(ctx context.Context, projectID uint, slotName string, uid uint, isAdmin bool) error {
	var slot Slot
	if err := s.db.WithContext(ctx).
		Where("project_id = ? AND slot_name = ?", projectID, slotName).
		First(&slot).Error; err != nil {
		return ErrNotFound
	}
	if slot.OccupiedByUID != uid && !isAdmin {
		return fmt.Errorf("仅占用人或管理员可释放")
	}
	p, err := s.project(ctx, projectID)
	if err != nil {
		return err
	}
	serverID, err := s.testTarget(ctx, projectID)
	if err == nil {
		if out, derr := s.res.DestroyCompose(ctx, serverID, deployName(p.Name, slotName)); derr != nil {
			return fmt.Errorf("销毁失败（可重试）：%s\n%v", out, derr)
		}
		// 旧命名域（<proj>-test-<slot>）兼容清理：幂等，不存在时无害
		if out, derr := s.res.DestroyCompose(ctx, serverID, legacyDeployName(p.Name, slotName)); derr != nil {
			logger.Warnf("[slots] 旧域名清理失败 %s: %v\n%s", legacyDeployName(p.Name, slotName), derr, out)
		}
	}
	// 硬删：占用行是临时状态（审计已在 server_events/notify），且软删行会占用
	// (project_id, slot_name) 唯一索引导致无法重新占用
	if err := s.db.WithContext(ctx).Unscoped().Delete(&slot).Error; err != nil {
		return err
	}
	s.notifySlot(ctx, p, &slot, fmt.Sprintf("槽位 %s 已由释放（分支 %s）", slotName, slot.Branch))
	return nil
}

// DurationInput 仅时长（续期用；不能复用 OccupyInput——slot/branch 的
// required 校验会让只传时长的续期请求 400）。
type DurationInput struct {
	DurationValue int    `json:"durationValue" binding:"required,min=1"`
	DurationUnit  string `json:"durationUnit" binding:"required,oneof=hours days weeks"`
}

// Renew 续期：从当前时间起重新计时一个时长。
func (s *Service) Renew(ctx context.Context, projectID uint, slotName string, in DurationInput, uid uint, isAdmin bool) (*Slot, error) {
	var slot Slot
	if err := s.db.WithContext(ctx).
		Where("project_id = ? AND slot_name = ?", projectID, slotName).
		First(&slot).Error; err != nil {
		return nil, ErrNotFound
	}
	if slot.OccupiedByUID != uid && !isAdmin {
		return nil, fmt.Errorf("仅占用人或管理员可续期")
	}
	slot.Duration = in.duration()
	slot.ExpireAt = time.Now().Add(in.duration())
	slot.Status = StatusOccupied
	if err := s.db.WithContext(ctx).Save(&slot).Error; err != nil {
		return nil, err
	}
	return &slot, nil
}

// List 槽位视图：项目全部槽位名（dev1..N），附占用信息。
func (s *Service) List(ctx context.Context, projectID uint) ([]map[string]any, error) {
	p, err := s.project(ctx, projectID)
	if err != nil {
		return nil, err
	}
	var occupied []Slot
	if err := s.db.WithContext(ctx).
		Where("project_id = ? AND status IN ?", projectID, []string{StatusOccupied, StatusExpired}).
		Find(&occupied).Error; err != nil {
		return nil, err
	}
	byName := map[string]Slot{}
	for _, s := range occupied {
		byName[s.SlotName] = s
	}
	out := make([]map[string]any, 0, p.TestSlotCount)
	for i := 1; i <= p.TestSlotCount; i++ {
		name := fmt.Sprintf("dev%d", i)
		if s, ok := byName[name]; ok {
			out = append(out, map[string]any{
				"slotName": name, "occupied": true, "slot": s,
			})
		} else {
			out = append(out, map[string]any{"slotName": name, "occupied": false})
		}
	}
	return out, nil
}

// ---- 分支推送自动链路（ci 模块的 BranchPushHook 调入） ----

// validImageTag 镜像 tag 白名单（拼进 compose 镜像名的值，收敛注入面）。
func validImageTag(tag string) bool {
	if len(tag) == 0 || len(tag) > 64 {
		return false
	}
	for _, r := range tag {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// SlotCallback CI 构建完成回调（2026-11-08 命名统一，替代 push 即部署）：
// Jenkins 质检+构建测试镜像后携带 tag 调用（应用级拉取令牌鉴权，env=test），
// 平台找到占用该分支的槽位并以该镜像重建。质检失败不会回调 → 测试环境停留旧版。
type SlotCallbackInput struct {
	App    string `json:"app" binding:"required"`    // 项目名（须与令牌绑定一致）
	Branch string `json:"branch" binding:"required"` // 触发构建的分支
	Tag    string `json:"tag" binding:"required"`    // 测试镜像 tag（yyyymmddHHMMSS）
}

// SlotCallback 返回 skipped=true 表示构建正常但无槽位可部署（不视为错误）。
func (s *Service) SlotCallback(ctx context.Context, in SlotCallbackInput, token string) (map[string]any, error) {
	if s.tokens == nil {
		return nil, fmt.Errorf("回调鉴权未装配（平台配置）")
	}
	if _, err := s.tokens.VerifyPullToken(ctx, token, in.App, "test"); err != nil {
		return nil, err
	}
	// tag 白名单：回调值会拼进 compose 镜像名（受信 CI 也校验，防令牌泄漏后的注入面）
	if !validImageTag(in.Tag) {
		return nil, fmt.Errorf("非法镜像 tag %q（仅允许字母数字._-）", in.Tag)
	}
	var proj projectRow
	if err := s.db.WithContext(ctx).Table("projects").
		Where("name = ? AND deleted_at IS NULL", in.App).First(&proj).Error; err != nil {
		return nil, fmt.Errorf("项目 %s 不存在（令牌 app 须与项目名一致）", in.App)
	}
	var hits []Slot
	s.db.WithContext(ctx).
		Where("project_id = ? AND branch = ? AND status IN ?", proj.ID, in.Branch, []string{StatusOccupied, StatusExpired}).
		Find(&hits)
	if len(hits) == 0 {
		return map[string]any{"skipped": true, "reason": fmt.Sprintf("分支 %s 无占用槽位", in.Branch)}, nil
	}
	for i := range hits {
		s.dispatchRebuild(&proj, &hits[i], "ci:"+in.Tag, in.Tag)
	}
	return map[string]any{"skipped": false, "slots": len(hits), "tag": in.Tag}, nil
}

// dispatchRebuild 投递重建请求：目标槽位空闲则立即执行（异步），
// 正在跑则记 pending（覆盖旧 pending），跑完补跑一次——部署幂等（按分支
// 最新提交），连续 push 合并为最后一次的结果。
func (s *Service) dispatchRebuild(p *projectRow, slot *Slot, source, imageTag string) {
	req := &rebuildReq{p: p, slotID: slot.ID, slot: *slot, source: source, imageTag: imageTag}
	s.rebuildMu.Lock()
	defer s.rebuildMu.Unlock()
	if s.rebuildRunning[slot.ID] {
		s.rebuildPending[slot.ID] = req
		return
	}
	s.rebuildRunning[slot.ID] = true
	go s.rebuildLoop(req)
}

// rebuildLoop 串行消费同槽位的重建请求，直到无新 pending。
func (s *Service) rebuildLoop(first *rebuildReq) {
	req := first
	for {
		s.rebuild(context.Background(), req.p, &req.slot, req.source, req.imageTag)
		s.rebuildMu.Lock()
		next := s.rebuildPending[req.slotID]
		delete(s.rebuildPending, req.slotID)
		if next == nil {
			delete(s.rebuildRunning, req.slotID)
			s.rebuildMu.Unlock()
			return
		}
		s.rebuildMu.Unlock()
		req = next
	}
}

func (s *Service) rebuild(ctx context.Context, p *projectRow, slot *Slot, source, imageTag string) {
	start := time.Now()
	serverID, err := s.testTarget(ctx, p.ID)
	if err != nil {
		logger.Warnf("[slots] 项目 %d %v", p.ID, err)
		return
	}
	yamlContent, err := s.fetchCompose(ctx, p, slot.Branch)
	tag := fmt.Sprintf("%s@%s", slot.Branch, slot.SlotName)
	status := ci.BuildSuccess
	output := ""
	if err == nil {
		// 旧命名域兼容清理（一次性迁移）：新城部署前销毁存量 <proj>-test-<slot>
		if out, derr := s.res.DestroyCompose(ctx, serverID, legacyDeployName(p.Name, slot.SlotName)); derr != nil {
			logger.Warnf("[slots] 旧域名清理失败 %s: %v\n%s", legacyDeployName(p.Name, slot.SlotName), derr, out)
		}
		var out string
		out, _, _, err = s.res.DeployComposeToImage(ctx, serverID, deployName(p.Name, slot.SlotName), yamlContent,
			s.testImage(ctx, p.ID, imageTag))
		output = out
	}
	if err != nil {
		status = ci.BuildFailed
		output = err.Error()
	}
	// 用 struct 落库：map 方式不会触发 GORM 的 CreatedAt 自动填充（曾出零值时间）
	// Provider 必须显式带项目值：Build 默认 gitea，漏设则 gitee 项目显示错来源；
	// DurationSecs 同理须实测赋值，否则构建/发布抽屉耗时列恒为空/0
	secs := int(time.Since(start).Seconds())
	b := ci.Build{
		ProjectID: p.ID, EnvType: "test", Tag: tag, Builder: slot.OccupiedBy,
		Provider: p.Provider, Source: "auto", Status: status,
		DurationSecs: secs,
	}
	if res := s.db.WithContext(ctx).Create(&b); res.Error == nil {
		s.db.WithContext(ctx).Model(slot).Update("last_build_id", b.ID)
	}
	// 同步落一条部署记录（测试环境发布列表可见、可重新部署/看 CD 日志）
	rel := releasemod.Release{
		ProjectID: p.ID, EnvType: "test", Tag: tag,
		ServerID: serverID, Runtime: "compose",
		ReleaseBy: slot.OccupiedBy, Status: releasemod.ReleaseSuccess,
		Output: truncate(output, 8000), DurationSecs: secs,
	}
	if status == ci.BuildFailed {
		rel.Status = releasemod.ReleaseFailed
	}
	if err := s.db.WithContext(ctx).Create(&rel).Error; err != nil {
		logger.Warnf("[slots] 落部署记录失败: %v", err)
	}
	verb := "部署成功"
	if status == ci.BuildFailed {
		verb = "部署失败"
	}
	s.notifySlot(ctx, p, slot, fmt.Sprintf("%s：槽位 %s（分支 %s，触发 %s）\n%s", verb, slot.SlotName, slot.Branch, source, truncate(output, 400)))
}

// fetchCompose 按分支取部署描述（gitea/gitee 按 provider 路由）。
func (s *Service) fetchCompose(ctx context.Context, p *projectRow, branch string) (string, error) {
	if p.ComposePath == "" {
		return "", errors.New("项目未配置部署描述文件路径")
	}
	body, err := s.ci.RawFile(ctx, p.Provider, p.RepoPath, p.ComposePath, branch)
	if err != nil {
		return "", fmt.Errorf("取部署描述失败（分支 %s）: %w", branch, err)
	}
	return string(body), nil
}

// ---- 到期与宽限回收（jobs 常驻） ----

// SweepExpire 到期标记（通知）+ 宽限期满自动回收。
func (s *Service) SweepExpire(ctx context.Context) error {
	now := time.Now()
	var due []Slot
	if err := s.db.WithContext(ctx).
		Where("status = ? AND expire_at < ?", StatusOccupied, now).
		Find(&due).Error; err != nil {
		return err
	}
	for i := range due {
		slot := due[i]
		p, err := s.project(ctx, slot.ProjectID)
		if err != nil {
			continue
		}
		if err := s.db.WithContext(ctx).Model(&slot).Update("status", StatusExpired).Error; err != nil {
			logger.Warnf("[slots] 标记过期失败 slot=%s: %v", slot.SlotName, err)
			continue
		}
		s.notifySlot(ctx, p, &slot, fmt.Sprintf("槽位 %s 已到期限（分支 %s），%d 天宽限后自动回收，请及时续期或释放",
			slot.SlotName, slot.Branch, p.SlotGraceDays))
	}
	// 宽限期满：自动回收
	var overdue []Slot
	if err := s.db.WithContext(ctx).
		Where("status = ?", StatusExpired).
		Find(&overdue).Error; err != nil {
		return err
	}
	for i := range overdue {
		slot := overdue[i]
		p, err := s.project(ctx, slot.ProjectID)
		if err != nil {
			continue
		}
		if now.Before(slot.ExpireAt.Add(time.Duration(p.SlotGraceDays) * 24 * time.Hour)) {
			continue
		}
		if err := s.Release(ctx, slot.ProjectID, slot.SlotName, 0, true); err != nil {
			logger.Warnf("[slots] 槽位 %s/%s 自动回收失败: %v", p.Name, slot.SlotName, err)
			continue
		}
		s.notifySlot(ctx, p, &slot, fmt.Sprintf("槽位 %s 宽限期满已自动回收（容器销毁，配置保留）", slot.SlotName))
	}
	return nil
}

func (s *Service) notifySlot(ctx context.Context, p *projectRow, slot *Slot, text string) {
	if p.NotifyTestGroupID == nil || s.notify == nil {
		return
	}
	g, err := s.notify.Get(ctx, *p.NotifyTestGroupID)
	if err != nil {
		return
	}
	jobs.GoSafe("slots:test-notify", func() {
		_ = s.notify.Send(context.WithoutCancel(ctx), g, "测试槽位："+p.Name, text)
	})
}

// testImage CI 回调镜像全名：registry/<proj>-test:<tag>。
// tag 为空返回空（compose 原文部署，兼容存量无占位符项目）。
func (s *Service) testImage(ctx context.Context, projectID uint, imageTag string) string {
	if imageTag == "" {
		return ""
	}
	var addr string
	s.db.WithContext(ctx).Table("projects").
		Select("registries.address").
		Joins("LEFT JOIN registries ON registries.id = projects.registry_id").
		Where("projects.id = ?", projectID).Scan(&addr)
	if addr == "" {
		return "" // 未绑 registry：交由 RenderCompose 报「占位符未提供镜像」
	}
	var name string
	s.db.WithContext(ctx).Table("projects").Select("name").Where("id = ?", projectID).Scan(&name)
	// 与主仓库同 repo、纯时间戳 tag 区分环境（TCR 个人版自动新建的 -test 仓库
	// 存在 compose pull 被拒异常，复用已验证仓库；tag 白名单保证纯数字/字母）
	// address 允许 host/namespace 形态（与 release.imageFor 一致；TrimRight 防双斜杠）
	return strings.TrimRight(addr, "/") + "/" + strx.NormalizeName(name) + ":" + imageTag
}

// deployName 槽位隔离域：<proj>-<slot>（命名统一去 -test 段，2026-11-08；
// 槽位名 dev1..N 天然不与 prod/canary 域冲突）。
func deployName(projectName, slotName string) string {
	return strx.NormalizeName(projectName) + "-" + slotName
}

// legacyDeployName 2026-11-08 之前的 <proj>-test-<slot> 旧域——兼容清理用。
func legacyDeployName(projectName, slotName string) string {
	return strx.NormalizeName(projectName) + "-test-" + slotName
}

func slotIndex(name string) int {
	var n int
	if _, err := fmt.Sscanf(name, "dev%d", &n); err != nil {
		return 1 << 30 // 不合法
	}
	return n
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strx.Truncate(s, n) // rune 安全（防切碎中文，v0.12.0 审计）
}

// Sweeper 到期扫描任务（占位主类型，便于 wire 聚合 cleanup）。
type Sweeper struct{}

// NewSweeper 到期扫描任务（每 5 分钟）；cleanup 供 wire 聚合。
func NewSweeper(svc *Service) (*Sweeper, func(), error) {
	g := jobs.NewGroup(jobs.Job{
		Name: "slots:sweep", Interval: 5 * time.Minute, Fn: svc.SweepExpire,
	})
	g.Start()
	return &Sweeper{}, g.Stop, nil
}
