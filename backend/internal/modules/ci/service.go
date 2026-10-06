package ci

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/modules/notify"
	"github.com/custos-machina/backend/internal/modules/projects"
	"github.com/custos-machina/backend/internal/pkg/crypto"
	"github.com/custos-machina/backend/internal/pkg/logger"
	"github.com/custos-machina/backend/internal/pkg/strx"
)

var ErrNotFound = errors.New("CI 配置不存在")

// RegistryOut / GlobalConfigOut 对外视图。
type RegistryOut struct {
	Registry
	HasCredential bool `json:"hasCredential"`
}

func toOut(r Registry) RegistryOut {
	out := RegistryOut{Registry: r, HasCredential: r.Credential != ""}
	out.Credential = ""
	return out
}

type GlobalConfigOut struct {
	GiteaBaseURL     string `json:"giteaBaseUrl"`
	HasGiteaToken    bool   `json:"hasGiteaToken"`
	WebhookSet       bool   `json:"webhookSet"`
	WebhookHint      string `json:"webhookHint"` // 给前端展示的回调地址模板
	GiteeBaseURL     string `json:"giteeBaseUrl"`
	HasGiteeToken    bool   `json:"hasGiteeToken"`
	GiteeWebhookSet  bool   `json:"giteeWebhookSet"`
	GiteeWebhookHint string `json:"giteeWebhookHint"`
	JenkinsURL       string `json:"jenkinsUrl"`
	JenkinsUser      string `json:"jenkinsUser"`
	HasJenkinsToken  bool   `json:"hasJenkinsToken"`
	JenkinsJobHint   string `json:"jenkinsJobHint"`
}

type Service struct {
	db     *gorm.DB
	cipher *crypto.Cipher
	notify *notify.Service
	proj   projects.Reader // 只读投影，替代 Table("projects") 直读

	// BranchPushHook 分支推送钩子（slots 模块经 app 注入：push → 匹配槽位自动重建）。
	BranchPushHook func(ctx context.Context, repoPath, branch, pusher string)
}

func NewService(db *gorm.DB, cipher *crypto.Cipher, ntfy *notify.Service, proj projects.Reader) *Service {
	return &Service{db: db, cipher: cipher, notify: ntfy, proj: proj}
}

// ---- 全局配置 ----

type SaveGlobalInput struct {
	GiteaBaseURL     string `json:"giteaBaseUrl" binding:"omitempty,url,max=255"`
	GiteaToken       string `json:"giteaToken" binding:"omitempty,max=512"`    // 留空保留
	WebhookSecret    string `json:"webhookSecret" binding:"omitempty,max=128"` // 留空保留
	GiteeBaseURL     string `json:"giteeBaseUrl" binding:"omitempty,url,max=255"`
	GiteeToken       string `json:"giteeToken" binding:"omitempty,max=512"`
	GiteeWebhookPass string `json:"giteeWebhookPass" binding:"omitempty,max=128"`
	JenkinsURL       string `json:"jenkinsUrl" binding:"omitempty,url,max=255"`
	JenkinsUser      string `json:"jenkinsUser" binding:"omitempty,max=128"`
	JenkinsToken     string `json:"jenkinsToken" binding:"omitempty,max=512"`
}

func (s *Service) GetGlobal(ctx context.Context) (*GlobalConfigOut, error) {
	var g GlobalConfig
	if err := s.db.WithContext(ctx).First(&g, 1).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &GlobalConfigOut{
				WebhookHint:      "配置 base url 后生成",
				GiteeWebhookHint: "配置 base url 后生成",
			}, nil
		}
		return nil, err
	}
	return &GlobalConfigOut{
		GiteaBaseURL:     g.GiteaBaseURL,
		HasGiteaToken:    g.GiteaToken != "",
		WebhookSet:       g.WebhookSecret != "",
		WebhookHint:      "在 gitea 仓库 Settings → Webhooks 添加：POST <平台地址>/api/ci/webhook/gitea（X-Gitea-Signature）",
		GiteeBaseURL:     g.GiteeBaseURL,
		HasGiteeToken:    g.GiteeToken != "",
		GiteeWebhookSet:  g.GiteeWebhook != "",
		GiteeWebhookHint: "在 gitee 仓库 管理 → WebHooks 添加：POST <平台地址>/api/ci/webhook/gitee（密码 = X-Gitee-Token）",
		JenkinsURL:       g.JenkinsURL,
		JenkinsUser:      g.JenkinsUser,
		HasJenkinsToken:  g.JenkinsToken != "",
		JenkinsJobHint:   "Jenkins job 需参数化构建（参数名 TAG=git 标签），job 名在项目管理里逐项目配置",
	}, nil
}

func (s *Service) SaveGlobal(ctx context.Context, in SaveGlobalInput) (*GlobalConfigOut, error) {
	var g GlobalConfig
	if err := s.db.WithContext(ctx).First(&g, 1).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("读取 CI 全局配置失败: %w", err)
	}
	g.ID = 1
	if in.GiteaBaseURL != "" {
		g.GiteaBaseURL = strings.TrimRight(in.GiteaBaseURL, "/")
	}
	if in.GiteeBaseURL != "" {
		g.GiteeBaseURL = strings.TrimRight(in.GiteeBaseURL, "/")
	}
	if in.JenkinsURL != "" {
		g.JenkinsURL = strings.TrimRight(in.JenkinsURL, "/")
	}
	g.JenkinsUser = in.JenkinsUser
	encIfSet := func(v, aad string, dst *string) error {
		if v == "" {
			return nil
		}
		enc, err := s.encrypt(v, aad)
		if err != nil {
			return err
		}
		*dst = enc
		return nil
	}
	for _, e := range []struct {
		v   string
		aad string
		dst *string
	}{
		{in.GiteaToken, aadGiteaToken, &g.GiteaToken},
		{in.GiteeToken, aadGiteeToken, &g.GiteeToken},
		{in.JenkinsToken, aadJenkinsToken, &g.JenkinsToken},
	} {
		if err := encIfSet(e.v, e.aad, e.dst); err != nil {
			return nil, err
		}
	}
	if in.WebhookSecret != "" {
		g.WebhookSecret = in.WebhookSecret
	}
	if in.GiteeWebhookPass != "" {
		g.GiteeWebhook = HashWebhookPass(in.GiteeWebhookPass) // 哈希存储：比对不需要原文
	}
	if err := s.db.WithContext(ctx).Save(&g).Error; err != nil {
		return nil, err
	}
	return s.GetGlobal(ctx)
}

func (s *Service) loadGlobal(ctx context.Context) (*GlobalConfig, error) {
	var g GlobalConfig
	if err := s.db.WithContext(ctx).First(&g, 1).Error; err != nil {
		return nil, errors.New("CI 全局配置未初始化")
	}
	return &g, nil
}

// projectToken 项目级 token（空 = 全局）。projects 表直读（同库，避免模块循环依赖）。
func (s *Service) projectToken(repoPath string) (string, error) {
	enc := s.proj.EncryptedCIToken(context.Background(), repoPath)
	if enc == "" || s.cipher == nil {
		return "", nil
	}
	row := struct{ CIToken string }{CIToken: enc}
	dec, err := s.cipher.Decrypt(row.CIToken, aadProjectToken)
	if err != nil {
		// 项目级 token 解密失败不能静默回落全局 token（权限语义漂移），显式告警
		logger.Warnf("[ci] 项目 %s 的 token 解密失败，回落全局 token: %v", repoPath, err)
		return "", nil
	}
	return dec, nil
}

// giteaFor 构造 gitea 客户端（*giteaClient 同时实现 GitProvider 与 CIProvider，
// 供两个工厂复用——接口到接口无法隐式转换，须保留具体类型）。
func (s *Service) giteaFor(ctx context.Context, repoPath string) (*giteaClient, error) {
	g, err := s.loadGlobal(ctx)
	if err != nil {
		return nil, err
	}
	token := ""
	if g.GiteaToken != "" && s.cipher != nil {
		if dec, err := s.cipher.Decrypt(g.GiteaToken, aadGiteaToken); err == nil {
			token = dec
		}
	}
	if pt, _ := s.projectToken(repoPath); pt != "" {
		token = pt
	}
	return newGiteaClient(g.GiteaBaseURL, token, g.WebhookSecret), nil
}

// gitFor 按 provider 构造 git 托管适配器（项目级 token 优先、全局兜底）。
func (s *Service) gitFor(ctx context.Context, provider, repoPath string) (GitProvider, error) {
	if provider == ProviderGitee {
		g, err := s.loadGlobal(ctx)
		if err != nil {
			return nil, err
		}
		token := ""
		if g.GiteeToken != "" && s.cipher != nil {
			if dec, err := s.cipher.Decrypt(g.GiteeToken, aadGiteeToken); err == nil {
				token = dec
			}
		}
		if pt, _ := s.projectToken(repoPath); pt != "" {
			token = pt
		}
		return newGiteeClient(g.GiteeBaseURL, token, g.GiteeWebhook), nil
	}
	return s.giteaFor(ctx, repoPath)
}

// ciFor 按 git provider 映射的 CI 引擎构造适配器（映射见 provider.go ciEngineFor）。
func (s *Service) ciFor(ctx context.Context, provider, repoPath string) (CIProvider, error) {
	if ciEngineFor(provider) == "jenkins" {
		g, err := s.loadGlobal(ctx)
		if err != nil {
			return nil, err
		}
		token := ""
		if g.JenkinsToken != "" && s.cipher != nil {
			if dec, err := s.cipher.Decrypt(g.JenkinsToken, aadJenkinsToken); err == nil {
				token = dec
			}
		}
		return newJenkinsClient(g.JenkinsURL, g.JenkinsUser, token), nil
	}
	// gitea Actions 与 gitea git 托管同一客户端实现（双接口）
	return s.giteaFor(ctx, repoPath)
}

// ---- Registry CRUD ----

type SaveRegistryInput struct {
	Name       string `json:"name" binding:"required,max=64"`
	Type       string `json:"type" binding:"required,oneof=aliyun tencent gitea harbor"`
	Address    string `json:"address" binding:"required,max=255"`
	Credential string `json:"credential" binding:"omitempty,max=512"` // "username:password"；留空保留
	Remark     string `json:"remark" binding:"max=255"`
}

func (s *Service) ListRegistries(ctx context.Context) ([]RegistryOut, error) {
	var rs []Registry
	if err := s.db.WithContext(ctx).Order("id").Find(&rs).Error; err != nil {
		return nil, err
	}
	out := make([]RegistryOut, len(rs))
	for i, r := range rs {
		out[i] = toOut(r)
	}
	return out, nil
}

func (s *Service) SaveRegistry(ctx context.Context, id uint, in SaveRegistryInput) (*RegistryOut, error) {
	var r Registry
	if id > 0 {
		if err := s.db.WithContext(ctx).First(&r, id).Error; err != nil {
			return nil, ErrNotFound
		}
	}
	r.Name, r.Type, r.Address, r.Remark = in.Name, in.Type, in.Address, in.Remark
	if in.Credential != "" {
		enc, err := s.encrypt(in.Credential, crypto.AADRegistryCredential)
		if err != nil {
			return nil, err
		}
		r.Credential = enc
	}
	if err := s.db.WithContext(ctx).Save(&r).Error; err != nil {
		return nil, err
	}
	out := toOut(r)
	return &out, nil
}

func (s *Service) DeleteRegistry(ctx context.Context, id uint) error {
	// 硬删：软删行占住 name 唯一索引
	res := s.db.WithContext(ctx).Unscoped().Delete(&Registry{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- webhook（端点定 provider，签名校验后归一到 handlePush）----

// VerifyGiteaWebhook 仅校验 X-Gitea-Signature（HMAC-SHA256）——鉴权失败 401，
// 与业务错误（400）分层由 handler 体现。
func (s *Service) VerifyGiteaWebhook(ctx context.Context, body []byte, sigHex string) error {
	g, err := s.loadGlobal(ctx)
	if err != nil {
		return errors.New("CI 全局配置未初始化，拒绝 webhook")
	}
	return newGiteaClient(g.GiteaBaseURL, "", g.WebhookSecret).VerifyWebhook(body, sigHex)
}

// HandleGiteaPush 解析并处理 gitea 推送（签名校验由 VerifyGiteaWebhook 先行）。
func (s *Service) HandleGiteaPush(ctx context.Context, body []byte) (*Build, error) {
	g, err := s.loadGlobal(ctx)
	if err != nil {
		return nil, err
	}
	ev, err := newGiteaClient(g.GiteaBaseURL, "", g.WebhookSecret).ParsePush(body)
	if err != nil {
		return nil, err
	}
	return s.handlePush(ctx, ev)
}

// VerifyGiteeWebhook 仅校验 X-Gitee-Token（密码常量时间比较）。
func (s *Service) VerifyGiteeWebhook(ctx context.Context, token string) error {
	g, err := s.loadGlobal(ctx)
	if err != nil {
		return errors.New("CI 全局配置未初始化，拒绝 webhook")
	}
	return newGiteeClient(g.GiteeBaseURL, "", g.GiteeWebhook).VerifyWebhook(nil, token)
}

// HandleGiteePush 解析并处理 gitee 推送（签名校验由 VerifyGiteeWebhook 先行）。
func (s *Service) HandleGiteePush(ctx context.Context, body []byte) (*Build, error) {
	g, err := s.loadGlobal(ctx)
	if err != nil {
		return nil, err
	}
	ev, err := newGiteeClient(g.GiteeBaseURL, "", g.GiteeWebhook).ParsePush(body)
	if err != nil {
		return nil, err
	}
	return s.handlePush(ctx, ev)
}

// handlePush 推送事件公共处理：分支推送交给槽位钩子，标签推送匹配项目落构建记录。
func (s *Service) handlePush(ctx context.Context, ev *PushEvent) (*Build, error) {
	if !strings.HasPrefix(ev.Ref, "refs/tags/") {
		// 分支推送：交给槽位自动链路（未注入 hook 则忽略）
		if s.BranchPushHook != nil && strings.HasPrefix(ev.Ref, "refs/heads/") {
			s.BranchPushHook(context.WithoutCancel(ctx), ev.RepoPath,
				strings.TrimPrefix(ev.Ref, "refs/heads/"), ev.Pusher)
		}
		return nil, nil
	}
	tag := strings.TrimPrefix(ev.Ref, "refs/tags/")

	// 项目匹配（精确优先/大小写兜底由 Reader 内聚）
	projView, err := s.proj.ViewByRepoPath(ctx, ev.RepoPath)
	if err != nil {
		return nil, nil // 非平台登记的项目，忽略
	}
	proj := notifyProjRow{
		ID:                  projView.ID,
		NotifyOnSuccess:     projView.NotifyOnSuccess,
		NotifyProdGroupID:   projView.NotifyProdGroupID,
		NotifyCanaryGroupID: projView.NotifyCanaryGroupID,
		NotifyTestGroupID:   projView.NotifyTestGroupID,
	}

	// 标签规则 → 环境：v* = 正式；canary-* = 灰度（格式校验 canary-yyyymmdd-缩写）
	env := ""
	switch {
	case strings.HasPrefix(tag, "v"):
		env = "prod"
	case strings.HasPrefix(tag, "canary-"):
		if !validCanaryTag(tag) {
			return nil, fmt.Errorf("灰度标签 %q 不符合 canary-yyyymmdd-姓名缩写 规则", tag)
		}
		env = "canary"
	default:
		return nil, nil // 不识别的标签不落记录
	}

	b := Build{
		ProjectID: proj.ID, EnvType: env, Tag: tag, SHA: ev.SHA,
		Provider: ev.Provider, Builder: s.mapBuilder(ev.Pusher), Source: SourceTag, Status: BuildPending,
	}
	if err := s.db.WithContext(ctx).Create(&b).Error; err != nil {
		return nil, err
	}
	return &b, nil
}

// validCanaryTag canary-yyyymmdd-[姓名首字母小写]。
func validCanaryTag(tag string) bool {
	parts := strings.SplitN(tag, "-", 3)
	if len(parts) != 3 || parts[0] != "canary" {
		return false
	}
	if len(parts[1]) != 8 {
		return false
	}
	for _, ch := range parts[1] {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return parts[2] != ""
}

// ---- 构建 ----

type BuildQuery struct {
	ProjectID uint
	EnvType   string
	// ScopeIDs P7-M1 项目范围（nil = 不限）：scoped 用户只见授权项目
	ScopeIDs   []uint
	Page, Size int
}

func (s *Service) ListBuilds(ctx context.Context, q BuildQuery) ([]Build, int64, error) {
	tx := s.db.WithContext(ctx).Model(&Build{})
	if q.ScopeIDs != nil {
		tx = tx.Where("project_id IN ?", q.ScopeIDs)
	}
	if q.ProjectID > 0 {
		tx = tx.Where("project_id = ?", q.ProjectID)
	}
	if q.EnvType != "" {
		tx = tx.Where("env_type = ?", q.EnvType)
	}
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if q.Page <= 0 {
		q.Page = 1
	}
	if q.Size <= 0 || q.Size > 100 {
		q.Size = 20
	}
	var list []Build
	if err := tx.Order("id DESC").Offset((q.Page - 1) * q.Size).Limit(q.Size).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ---- 状态轮询（jobs 常驻任务） ----

// PollPending 更新所有未到终态的构建状态；终态时按项目配置推通知。
func (s *Service) PollPending(ctx context.Context) error {
	var pendings []Build
	if err := s.db.WithContext(ctx).
		Where("status IN ?", []string{BuildPending, BuildRunning}).
		Find(&pendings).Error; err != nil {
		return err
	}
	// 长时间无终态（runner 挂了 / commit status 不可得）标失败并通知——
	// 观测链路失效时不能永远显示"排队中"
	pendingTimeout := 45 * time.Minute
	for _, b := range pendings {
		if time.Since(b.CreatedAt) > pendingTimeout {
			s.db.WithContext(ctx).Model(&Build{}).Where("id = ?", b.ID).
				Update("status", BuildFailed)
			if pv, err := s.proj.ViewByID(ctx, b.ProjectID); err == nil {
				b.Status = BuildFailed
				s.notifyBuild(ctx, b, notifyProjRow{
					ID: pv.ID, RepoPath: pv.RepoPath, NotifyOnSuccess: pv.NotifyOnSuccess,
					NotifyProdGroupID: pv.NotifyProdGroupID, NotifyCanaryGroupID: pv.NotifyCanaryGroupID,
					NotifyTestGroupID: pv.NotifyTestGroupID,
				}, BuildFailed)
			}
			logger.Warnf("[ci] 构建 %d（%s）超过 %v 无终态，标记失败", b.ID, b.Tag, pendingTimeout)
		}
	}
	for _, b := range pendings {
		// SHA 无效（手动重放/异常 webhook）——不可能观察到流水线，直接失败
		if b.SHA == "" || b.SHA == strings.Repeat("0", 40) {
			s.db.WithContext(ctx).Model(&Build{}).Where("id = ?", b.ID).
				Update("status", BuildFailed)
			continue
		}
		pv, verr := s.proj.ViewByID(ctx, b.ProjectID)
		if verr != nil {
			continue
		}
		proj := notifyProjRow{
			ID: pv.ID, RepoPath: pv.RepoPath, NotifyOnSuccess: pv.NotifyOnSuccess,
			NotifyProdGroupID: pv.NotifyProdGroupID, NotifyCanaryGroupID: pv.NotifyCanaryGroupID,
			NotifyTestGroupID: pv.NotifyTestGroupID,
		}
		cip, err := s.ciFor(ctx, b.Provider, pv.RepoPath)
		if err != nil {
			logger.Warnf("[ci] 构建 %d 无法建 CI 客户端（provider=%s）: %v", b.ID, b.Provider, err)
			continue
		}
		st, err := cip.Status(ctx, BuildRef{RepoPath: pv.RepoPath, SHA: b.SHA, Tag: b.Tag, Job: pv.CIJob})
		if err != nil {
			// 提交/job 在对端不存在（假 SHA/仓库改写/job 删了）——永久错误，终止轮询标失败；
			// 其余（Jenkins 不可达等瞬时错误）保留 pending 下轮再试
			if strings.Contains(err.Error(), "404") {
				s.db.WithContext(ctx).Model(&Build{}).Where("id = ?", b.ID).
					Update("status", BuildFailed)
				logger.Warnf("[ci] 构建 %d 的目标在 CI 侧不存在，标记失败", b.ID)
				continue
			}
			logger.Warnf("[ci] 构建 %d 状态轮询失败: %v", b.ID, err)
			continue
		}
		newStatus := st
		if newStatus == b.Status {
			continue
		}
		// 防抖：commit status 在 job 切换间隙可能短暂回落为非 success，
		// 连续 2 次非 success 才标 failed（防误杀 running 中的构建）
		if newStatus == BuildFailed {
			if b.FailCount < 1 {
				s.db.WithContext(ctx).Model(&Build{}).Where("id = ?", b.ID).
					Update("fail_count", b.FailCount+1)
				continue
			}
		} else if newStatus == BuildSuccess {
			// 恢复为 success 时重置计数
			if b.FailCount > 0 {
				s.db.WithContext(ctx).Model(&Build{}).Where("id = ?", b.ID).
					Update("fail_count", 0)
			}
		}
		updates := map[string]any{"status": newStatus}
		if b.LogURL == "" {
			if gp, err := s.gitFor(ctx, b.Provider, pv.RepoPath); err == nil {
				updates["log_url"] = gp.ActionsURL(pv.RepoPath)
			}
		}
		// 耗时：pending→running 记开始时间；终态按 started_at 差值计算
		if b.Status == BuildPending && newStatus == BuildRunning {
			updates["started_at"] = time.Now()
		}
		if newStatus == BuildSuccess || newStatus == BuildFailed {
			start := b.StartedAt
			if start.IsZero() {
				start = b.CreatedAt
			}
			updates["duration_secs"] = int(time.Since(start).Seconds())
			// P7-M2：终态顺手拉 consoleText 尾部存库（log_tail）——AI 分析
			// 优先读它（快、CI 不可达时可用）；拉取失败不阻塞终态落库
			if logTxt, lerr := cip.Log(ctx, BuildRef{RepoPath: pv.RepoPath, SHA: b.SHA, Tag: b.Tag, Job: pv.CIJob}); lerr == nil {
				updates["log_tail"] = tailLines(logTxt, 200)
			} else {
				logger.Warnf("[ci] 构建 %d log_tail 拉取失败（AI 分析将回退实时拉取）: %v", b.ID, lerr)
			}
		}
		if err := s.db.WithContext(ctx).Model(&Build{}).Where("id = ?", b.ID).Updates(updates).Error; err != nil {
			continue
		}
		if newStatus == BuildSuccess || newStatus == BuildFailed {
			s.notifyBuild(ctx, b, proj, newStatus)
		}
	}
	return nil
}

// notifyProjRow 项目通知相关列的投影。
type notifyProjRow struct {
	ID                  uint
	RepoPath            string
	NotifyOnSuccess     bool
	NotifyProdGroupID   *uint
	NotifyCanaryGroupID *uint
	NotifyTestGroupID   *uint
}

func (s *Service) notifyBuild(ctx context.Context, b Build, proj notifyProjRow, status string) {
	if status == BuildSuccess && !proj.NotifyOnSuccess {
		return
	}
	var groupID *uint
	switch b.EnvType {
	case "prod":
		groupID = proj.NotifyProdGroupID
	case "canary":
		groupID = proj.NotifyCanaryGroupID
	case "test":
		groupID = proj.NotifyTestGroupID
	}
	if groupID == nil {
		return
	}
	g, err := s.notify.Get(ctx, *groupID)
	if err != nil {
		return
	}
	verb := "成功"
	if status == BuildFailed {
		verb = "失败"
	}
	title := fmt.Sprintf("构建%s：%s %s", verb, proj.RepoPath, b.Tag)
	content := fmt.Sprintf("环境：%s\n构建人：%s\n[查看日志](%s)", envLabel(b.EnvType), b.Builder, b.LogURL)
	go func() {
		if err := s.notify.Send(context.WithoutCancel(ctx), g, title, content); err != nil {
			logger.Warnf("[ci] 构建通知发送失败: %v", err)
		}
	}()
}

// mapBuilder gitea pusher 同名映射平台用户（找不到保留 gitea 名）——
// webhook 自动触发时平台无法感知操作者，同名约定是唯一可行关联。
func (s *Service) mapBuilder(giteaLogin string) string {
	if giteaLogin == "" {
		return "unknown"
	}
	var display string
	if err := s.db.Table("users").Select("display_name").
		Where("username = ?", giteaLogin).First(&display).Error; err != nil {
		return giteaLogin
	}
	if display != "" {
		return display
	}
	return giteaLogin
}

func envLabel(env string) string {
	switch env {
	case "prod":
		return "正式"
	case "canary":
		return "灰度"
	case "test":
		return "测试"
	}
	return env
}

// RawFile 按标签/分支取仓库文件（release 取部署描述用；provider 决定 gitea/gitee 端点与鉴权）。
func (s *Service) RawFile(ctx context.Context, provider, repoPath, path, ref string) ([]byte, error) {
	gp, err := s.gitFor(ctx, provider, repoPath)
	if err != nil {
		return nil, err
	}
	return gp.RawFile(ctx, repoPath, path, ref)
}

// BuildLog 展示某次构建的流水线日志（gitea 按 SHA 找 run；Jenkins 按 job+TAG 参数定位）。
func (s *Service) BuildLog(ctx context.Context, buildID uint) (string, error) {
	var b Build
	if err := s.db.WithContext(ctx).First(&b, buildID).Error; err != nil {
		return "", errors.New("构建记录不存在")
	}
	// 测试环境槽位构建没有外部流水线（部署由平台直接执行），
	// 其"日志"= 对应的部署输出（releases 按 tag 取最新）
	if b.EnvType == "test" {
		var out struct{ Output string }
		if err := s.db.WithContext(ctx).Table("releases").
			Select("output").Where("project_id = ? AND tag = ?", b.ProjectID, b.Tag).
			Order("id DESC").First(&out).Error; err != nil {
			return "", errors.New("该构建没有对应的部署输出记录")
		}
		return "（槽位部署输出，无独立流水线）\n" + out.Output, nil
	}
	if b.SHA == "" || b.SHA == strings.Repeat("0", 40) {
		return "", errors.New("该记录无关联提交（手动重放的测试数据）")
	}
	pv, err := s.proj.ViewByID(ctx, b.ProjectID)
	if err != nil {
		return "", errors.New("项目不存在")
	}
	cip, err := s.ciFor(ctx, b.Provider, pv.RepoPath)
	if err != nil {
		return "", err
	}
	return cip.Log(ctx, BuildRef{RepoPath: pv.RepoPath, SHA: b.SHA, Tag: b.Tag, Job: pv.CIJob})
}

// Branches 供前端表单（M5 槽位占用选分支）。
func (s *Service) Branches(ctx context.Context, projectID uint) ([]string, error) {
	pv, err := s.proj.ViewByID(ctx, projectID)
	if err != nil {
		return nil, ErrNotFound
	}
	gp, err := s.gitFor(ctx, pv.Provider, pv.RepoPath)
	if err != nil {
		// 未配置全局 CI 不阻断槽位等功能：返回空列表，前端降级为手输分支
		logger.Warnf("[ci] 拉分支降级为空列表: %v", err)
		return []string{}, nil
	}
	return gp.Branches(ctx, pv.RepoPath)
}

func (s *Service) encrypt(v, aad string) (string, error) {
	if s.cipher == nil {
		return "", errors.New("平台主密钥未配置，无法加密")
	}
	return s.cipher.Encrypt(v, aad)
}

// 密文字段绑定（GCM AAD）
const (
	aadGiteaToken   = "ci.gitea_token"
	aadGiteeToken   = "ci.gitee_token"
	aadJenkinsToken = "ci.jenkins_token"
	aadProjectToken = crypto.AADProjectCIToken
)

// tailLines 取文本最后 n 行（log_tail 截尾存储用）。
func tailLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

// RecentBuildLogsText P7-M2（AI 工具 get_build_logs 数据源）：最近构建的日志
// 摘要行 + 失败构建的日志正文。log_tail 优先（终态留存、CI 不可达时可用）；
// 失败且无 log_tail 时实时调 provider 拉取（回退路径），仍失败则降级提示。
func (s *Service) RecentBuildLogsText(ctx context.Context, projectID uint, limit int) (string, error) {
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	q := s.db.WithContext(ctx).Model(&Build{}).
		Order("id DESC").Limit(limit)
	if projectID > 0 {
		q = q.Where("project_id = ?", projectID)
	}
	var builds []Build
	if err := q.Find(&builds).Error; err != nil {
		return "", err
	}
	if len(builds) == 0 {
		return "（暂无构建记录）", nil
	}
	var sb strings.Builder
	for _, b := range builds {
		fmt.Fprintf(&sb, "== #%d %s %s %s %s dur=%ds\n", b.ID, b.Tag, b.EnvType, b.Status, b.Provider, b.DurationSecs)
		if b.Status != BuildFailed {
			continue
		}
		if b.LogTail != "" {
			sb.WriteString(tailLines(b.LogTail, 40))
			sb.WriteString("\n")
			continue
		}
		// 回退：实时拉 consoleText（test 环境走部署输出）
		if logTxt, err := s.BuildLog(ctx, b.ID); err == nil {
			sb.WriteString(tailLines(logTxt, 40))
			sb.WriteString("\n")
		} else {
			fmt.Fprintf(&sb, "（日志暂不可用：%v）\n", err)
		}
	}
	return truncateLines(sb.String(), 8000), nil
}

// GitCommitsText P7-M2（AI 工具 get_git_commits 数据源）：按 repo_path 定位
// 项目 provider（找不到项目时默认 gitea）后查提交列表。
func (s *Service) GitCommitsText(ctx context.Context, repoPath string, sinceHours int, limit int) (string, error) {
	if sinceHours <= 0 {
		sinceHours = 24
	}
	provider := ""
	var proj struct{ Provider string }
	if err := s.db.WithContext(ctx).Table("projects").
		Select("provider").Where("repo_path = ?", repoPath).First(&proj).Error; err == nil {
		provider = proj.Provider
	}
	gp, err := s.gitFor(ctx, provider, repoPath)
	if err != nil {
		return "", err
	}
	since := time.Now().Add(-time.Duration(sinceHours) * time.Hour)
	return gp.Commits(ctx, repoPath, "", since, limit)
}

// truncateLines 按行截断（工具输出统一 8000 字符纪律）。
func truncateLines(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// rune 安全截断（v0.12.2 复核：8000 字节硬切可能把中文尾行切出非法 UTF-8）
	return strx.Truncate(s, n) + "\n…（结果过长截断）"
}
