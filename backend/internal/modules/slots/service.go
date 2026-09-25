package slots

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/modules/ci"
	"github.com/custos-machina/backend/internal/modules/notify"
	"github.com/custos-machina/backend/internal/modules/resources"
	"github.com/custos-machina/backend/internal/pkg/jobs"
	"github.com/custos-machina/backend/internal/pkg/logger"
)

var ErrNotFound = errors.New("槽位不存在")
var ErrOccupied = errors.New("槽位已被占用")

type Service struct {
	db     *gorm.DB
	ci     *ci.Service
	res    *resources.Service
	notify *notify.Service
}

func NewService(db *gorm.DB, ciSvc *ci.Service, resSvc *resources.Service, ntfy *notify.Service) *Service {
	return &Service{db: db, ci: ciSvc, res: resSvc, notify: ntfy}
}

// projectRow 只读所需列。
type projectRow struct {
	ID                uint
	Name              string
	RepoPath          string
	ComposePath       string
	TestSlotCount     int
	SlotGraceDays     int
	NotifyTestGroupID *uint
}

func (s *Service) project(ctx context.Context, id uint) (*projectRow, error) {
	var p projectRow
	if err := s.db.WithContext(ctx).Table("projects").
		Select("id, name, repo_path, compose_path, test_slot_count, slot_grace_days, notify_test_group_id").
		Where("id = ?", id).First(&p).Error; err != nil {
		return nil, errors.New("项目不存在")
	}
	return &p, nil
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
	// 立即拉起（失败不回滚占用：用户可修好后等推送重建，或手动释放）
	go s.rebuild(context.WithoutCancel(ctx), p, &slot, "occupy")
	return &slot, nil
}

// Release 释放槽位：销毁隔离域容器，覆盖配置保留（slot_overrides 不动），删除占用行。
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
	}
	if err := s.db.WithContext(ctx).Delete(&slot).Error; err != nil {
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

// OnBranchPush 项目仓库某分支收到 push：匹配占用该分支的槽位并重建。
func (s *Service) OnBranchPush(ctx context.Context, repoPath, branch, pusher string) {
	var proj projectRow
	if err := s.db.WithContext(ctx).Table("projects").
		Select("id, name, repo_path, compose_path, test_slot_count, slot_grace_days, notify_test_group_id").
		Where("lower(repo_path) = ?", strings.ToLower(repoPath)).First(&proj).Error; err != nil {
		return // 未登记项目
	}
	var hits []Slot
	s.db.WithContext(ctx).
		Where("project_id = ? AND branch = ? AND status IN ?", proj.ID, branch, []string{StatusOccupied, StatusExpired}).
		Find(&hits)
	for i := range hits {
		slot := hits[i]
		go s.rebuild(context.WithoutCancel(ctx), &proj, &slot, "push:"+pusher)
	}
}

// rebuild 拉起槽位隔离域：按分支取 compose 描述 → 部署 <proj>-test-<slot>；落构建记录 + 通知。
func (s *Service) rebuild(ctx context.Context, p *projectRow, slot *Slot, source string) {
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
		var out string
		out, _, err = s.res.DeployComposeTo(ctx, serverID, deployName(p.Name, slot.SlotName), yamlContent)
		output = out
	}
	if err != nil {
		status = ci.BuildFailed
		output = err.Error()
	}
	// 用 struct 落库：map 方式不会触发 GORM 的 CreatedAt 自动填充（曾出零值时间）
	b := ci.Build{
		ProjectID: p.ID, EnvType: "test", Tag: tag, Builder: slot.OccupiedBy,
		Source: "auto", Status: status,
	}
	if res := s.db.WithContext(ctx).Create(&b); res.Error == nil {
		s.db.WithContext(ctx).Model(slot).Update("last_build_id", b.ID)
	}
	verb := "部署成功"
	if status == ci.BuildFailed {
		verb = "部署失败"
	}
	s.notifySlot(ctx, p, slot, fmt.Sprintf("%s：槽位 %s（分支 %s，触发 %s）\n%s", verb, slot.SlotName, slot.Branch, source, truncate(output, 400)))
}

func (s *Service) fetchCompose(ctx context.Context, p *projectRow, branch string) (string, error) {
	if p.ComposePath == "" {
		return "", errors.New("项目未配置部署描述文件路径")
	}
	raw, base, err := s.ci.RawClient(ctx, p.RepoPath)
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/api/v1/repos/%s/raw/%s?ref=%s", base, p.RepoPath, strings.TrimPrefix(p.ComposePath, "/"), branch)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := raw.HTTPDo(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("取部署描述失败：gitea %d（分支 %s）", resp.StatusCode, branch)
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
		s.db.WithContext(ctx).Model(&slot).Update("status", StatusExpired)
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
	go func() {
		_ = s.notify.Send(context.WithoutCancel(ctx), g, "测试槽位："+p.Name, text)
	}()
}

func deployName(projectName, slotName string) string {
	n := strings.ToLower(strings.TrimSpace(projectName))
	var b strings.Builder
	for _, ch := range n {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9', ch == '_', ch == '.', ch == '-':
			b.WriteRune(ch)
		default:
			b.WriteRune('-')
		}
	}
	out := b.String()
	if out == "" {
		out = "project"
	}
	return out + "-test-" + slotName
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
	return s[:n]
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
