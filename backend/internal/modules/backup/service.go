package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/config"
	"github.com/custos-machina/backend/internal/modules/notify"
	"github.com/custos-machina/backend/internal/pkg/crypto"
	"github.com/custos-machina/backend/internal/pkg/jobs"
	"github.com/custos-machina/backend/internal/pkg/logger"
)

var ErrNotFound = errors.New("备份任务不存在")

// EventNotifier 统一别名（notify 是唯一生产实现；历史上 6 份逐字相同的声明收敛于此）。
type EventNotifier = notify.EventNotifier

// SSHExecutor resources.Service 的最小投影（避免模块反向依赖具体类型）。
type SSHExecutor interface {
	RunCommandOn(ctx context.Context, serverID uint, cmd, stdin string, timeout time.Duration) (string, error)
	SftpDownload(serverID uint, name string, w io.Writer) (int64, error)
}

// Service 备份中心：任务 CRUD + 调度扫描 + 执行 + 保留清理。
type Service struct {
	db       *gorm.DB
	cipher   *crypto.Cipher
	cfg      *config.Config
	ssh      SSHExecutor // 可空：无远端任务时不注入也可
	notifier EventNotifier
}

// 密文字段绑定（GCM AAD）
const (
	aadS3Secret   = "backup_jobs.s3_secret"
	aadPassphrase = "backup_jobs.passphrase"
)

func NewService(db *gorm.DB, cipher *crypto.Cipher, cfg *config.Config, ssh SSHExecutor) *Service {
	return &Service{db: db, cipher: cipher, cfg: cfg, ssh: ssh}
}

func (s *Service) SetNotifier(n EventNotifier) { s.notifier = n }

// ---- 调度 ----

// NewScheduler 扫描型调度（对齐 cron 模块模式）：30s 扫到点任务；
// 24h 清理过期运行记录。返回 cleanup。
func NewScheduler(svc *Service) (*Scheduler, func(), error) {
	svc.recoverDangling()
	g := jobs.NewGroup(
		jobs.Job{Name: "backup:sched", Interval: 30 * time.Second, Fn: svc.scanDue},
		jobs.Job{Name: "backup:purge", Interval: 24 * time.Hour, Fn: svc.purgeOldRuns},
	)
	g.Start()
	return &Scheduler{}, g.Stop, nil
}

type Scheduler struct{}

const runRetention = 90 * 24 * time.Hour

func (s *Service) purgeOldRuns(ctx context.Context) error {
	return s.db.WithContext(ctx).Where("created_at < ?", time.Now().Add(-runRetention)).Delete(&Run{}).Error
}

// recoverDangling 平台重启恢复：悬空 running 记录标 unknown。
func (s *Service) recoverDangling() {
	s.db.Model(&Run{}).Where("status = ?", RunRunning).
		Updates(map[string]any{"status": RunUnknown, "output": "平台重启，结果未知（调度器恢复时标记）"})
}

func (s *Service) scanDue(ctx context.Context) error {
	now := time.Now()
	var due []Job
	if err := s.db.WithContext(ctx).
		Where("enabled = ? AND next_run_at IS NOT NULL AND next_run_at <= ?", true, now).
		Limit(20).Find(&due).Error; err != nil {
		return err
	}
	for _, j := range due {
		s.scheduleNext(ctx, &j, now)
		if s.concurrentRunning(j.ID) {
			continue // 上一轮还没结束：跳过本触发点（Forbid 语义）
		}
		jobs.GoSafe("backup:run", func() {
			if _, err := s.execute(context.WithoutCancel(ctx), &j, TriggerSchedule); err != nil {
				logger.Warnf("[backup] 调度执行失败 job=%q: %v", j.Name, err)
			}
		})
	}
	return nil
}

// scheduleNext 无论本轮是否真正执行，先把 next_run_at 推进
// （跳过不补跑，对齐 cron 模块 missed 语义——平台宕机错过的触发点不补偿）。
func (s *Service) scheduleNext(ctx context.Context, j *Job, now time.Time) {
	sched, err := cron.ParseStandard(j.Schedule)
	if err != nil {
		return
	}
	next := sched.Next(now)
	j.NextRunAt = &next
	s.db.WithContext(ctx).Model(&Job{}).Where("id = ?", j.ID).Update("next_run_at", next)
}

func (s *Service) concurrentRunning(jobID uint) bool {
	var n int64
	s.db.Model(&Run{}).Where("job_id = ? AND status = ?", jobID, RunRunning).Count(&n)
	return n > 0
}

// ---- CRUD ----

type SaveJobInput struct {
	Name           string `json:"name" binding:"required,max=64"`
	Type           string `json:"type" binding:"required,oneof=platform_self remote_dir"`
	Enabled        bool   `json:"enabled"`
	Schedule       string `json:"schedule" binding:"omitempty,max=32"`
	ServerID       uint   `json:"serverId"`
	RemotePath     string `json:"remotePath" binding:"omitempty,max=512"`
	Storage        string `json:"storage" binding:"required,oneof=local s3"`
	S3Endpoint     string `json:"s3Endpoint" binding:"omitempty,max=255"`
	S3Bucket       string `json:"s3Bucket" binding:"omitempty,max=64"`
	S3Prefix       string `json:"s3Prefix" binding:"omitempty,max=255"`
	S3AccessKey    string `json:"s3AccessKey" binding:"omitempty,max=128"`
	S3SecretKey    string `json:"s3SecretKey" binding:"omitempty,max=128"` // 明文入参，落库前加密；留空保留
	LocalDir       string `json:"localDir" binding:"omitempty,max=255"`
	Passphrase     string `json:"passphrase" binding:"omitempty,max=128"` // 同上
	RetentionCount int    `json:"retentionCount" binding:"min=1,max=365"`
}

// remotePathRe 远端目录白名单：绝对路径 + 安全字符（v0.12.0 审计严重项 4——
// RemotePath 拆分后拼进目标机 shell，空格/分号/引号都是注入面）。
var remotePathRe = regexp.MustCompile(`^/[a-zA-Z0-9][a-zA-Z0-9._/-]*$`)

func (s *Service) validateJob(in SaveJobInput) error {
	if in.Schedule != "" {
		if _, err := cron.ParseStandard(in.Schedule); err != nil {
			return fmt.Errorf("调度表达式不合法（标准 5 段 crontab）: %v", err)
		}
	}
	switch in.Type {
	case TypeRemoteDir:
		if in.ServerID == 0 || in.RemotePath == "" {
			return fmt.Errorf("remote_dir 任务须指定目标主机与目录")
		}
		if !remotePathRe.MatchString(in.RemotePath) {
			return fmt.Errorf("远端目录须为绝对路径，且仅含字母数字与 . _ - /（不支持空格、中文与特殊符号）")
		}
	case TypePlatformSelf:
		if in.Passphrase == "" {
			// 编辑场景允许留空（保留原口令），由调用方区分；新建在 Create 强校验
		}
		if s.cfg.Database.Driver != "sqlite" {
			return fmt.Errorf("platform_self 暂仅支持 sqlite 部署（当前 %s）", s.cfg.Database.Driver)
		}
		if in.Storage == StorageS3 && in.Passphrase == "" {
			return fmt.Errorf("platform_self 备份必须配置口令（密钥份额加密）")
		}
	}
	return nil
}

func (s *Service) encryptField(plain, aad string) (string, error) {
	if plain == "" {
		return "", nil
	}
	if s.cipher == nil {
		return "", crypto.ErrNoMasterKey
	}
	return s.cipher.Encrypt(plain, aad)
}

func (s *Service) CreateJob(ctx context.Context, in SaveJobInput) (*Job, error) {
	if err := s.validateJob(in); err != nil {
		return nil, err
	}
	if in.Type == TypePlatformSelf && in.Passphrase == "" {
		return nil, fmt.Errorf("platform_self 备份必须配置口令")
	}
	j := Job{
		Name: in.Name, Type: in.Type, Enabled: in.Enabled, Schedule: in.Schedule,
		ServerID: in.ServerID, RemotePath: in.RemotePath,
		Storage: in.Storage, S3Endpoint: in.S3Endpoint, S3Bucket: in.S3Bucket,
		S3AccessKey: in.S3AccessKey, LocalDir: in.LocalDir,
		RetentionCount: in.RetentionCount,
	}
	if in.RetentionCount == 0 {
		j.RetentionCount = 7
	}
	var err error
	if j.S3SecretEnc, err = s.encryptField(in.S3SecretKey, aadS3Secret); err != nil {
		return nil, err
	}
	if j.PassphraseEnc, err = s.encryptField(in.Passphrase, aadPassphrase); err != nil {
		return nil, err
	}
	if j.S3Prefix == "" {
		j.S3Prefix = sanitizePrefix(j.Name)
	} else {
		j.S3Prefix = sanitizePrefix(in.S3Prefix)
	}
	if in.Schedule != "" {
		s.scheduleNext(ctx, &j, time.Now()) // 填 NextRunAt
	}
	if err := s.db.WithContext(ctx).Create(&j).Error; err != nil {
		return nil, err
	}
	return &j, nil
}

func (s *Service) UpdateJob(ctx context.Context, id uint, in SaveJobInput) (*Job, error) {
	var j Job
	if err := s.db.WithContext(ctx).First(&j, id).Error; err != nil {
		return nil, ErrNotFound
	}
	if err := s.validateJob(in); err != nil {
		return nil, err
	}
	j.Name, j.Type, j.Enabled, j.Schedule = in.Name, in.Type, in.Enabled, in.Schedule
	j.ServerID, j.RemotePath = in.ServerID, in.RemotePath
	j.Storage, j.S3Endpoint, j.S3Bucket, j.S3AccessKey = in.Storage, in.S3Endpoint, in.S3Bucket, in.S3AccessKey
	j.LocalDir = in.LocalDir
	j.RetentionCount = in.RetentionCount
	if in.S3Prefix == "" {
		j.S3Prefix = sanitizePrefix(in.Name)
	} else {
		j.S3Prefix = sanitizePrefix(in.S3Prefix)
	}
	// 密文类字段留空保留
	if in.S3SecretKey != "" {
		enc, err := s.encryptField(in.S3SecretKey, aadS3Secret)
		if err != nil {
			return nil, err
		}
		j.S3SecretEnc = enc
	}
	if in.Passphrase != "" {
		enc, err := s.encryptField(in.Passphrase, aadPassphrase)
		if err != nil {
			return nil, err
		}
		j.PassphraseEnc = enc
	}
	j.NextRunAt = nil
	if in.Schedule != "" {
		s.scheduleNext(ctx, &j, time.Now())
	}
	if err := s.db.WithContext(ctx).Save(&j).Error; err != nil {
		return nil, err
	}
	return &j, nil
}

func (s *Service) DeleteJob(ctx context.Context, id uint) error {
	res := s.db.WithContext(ctx).Delete(&Job{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// JobOut 对外视图：密文不入参。
type JobOut struct {
	Job
	HasPassphrase bool `json:"hasPassphrase"`
	HasS3Secret   bool `json:"hasS3Secret"`
}

func toJobOut(j Job) JobOut {
	return JobOut{Job: j, HasPassphrase: j.PassphraseEnc != "", HasS3Secret: j.S3SecretEnc != ""}
}

func (s *Service) ListJobs(ctx context.Context) ([]JobOut, error) {
	var js []Job
	if err := s.db.WithContext(ctx).Order("id").Find(&js).Error; err != nil {
		return nil, err
	}
	out := make([]JobOut, len(js))
	for i, j := range js {
		out[i] = toJobOut(j)
	}
	return out, nil
}

func (s *Service) ListRuns(ctx context.Context, jobID uint, limit int) ([]Run, error) {
	q := s.db.WithContext(ctx).Model(&Run{}).Order("id DESC")
	if jobID > 0 {
		q = q.Where("job_id = ?", jobID)
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var rs []Run
	if err := q.Limit(limit).Find(&rs).Error; err != nil {
		return nil, err
	}
	return rs, nil
}

// TriggerRun 手动触发（并发保护同调度）。
func (s *Service) TriggerRun(ctx context.Context, id uint) (*Run, error) {
	var j Job
	if err := s.db.WithContext(ctx).First(&j, id).Error; err != nil {
		return nil, ErrNotFound
	}
	if s.concurrentRunning(j.ID) {
		return nil, fmt.Errorf("上一轮备份仍在执行，请稍候")
	}
	return s.execute(ctx, &j, TriggerManual)
}

// ---- 执行 ----

// dataDir 从 sqlite DSN 推导平台数据目录。
func (s *Service) dataDir() string {
	dsn := s.cfg.Database.DSN
	if dsn == "" {
		return ""
	}
	dsn = strings.TrimPrefix(strings.TrimPrefix(dsn, "file:"), "sqlite:")
	if i := strings.IndexByte(dsn, '?'); i >= 0 {
		dsn = dsn[:i]
	}
	return filepath.Dir(dsn)
}

func (s *Service) execute(ctx context.Context, j *Job, trigger string) (*Run, error) {
	// 备份全程脱离请求 ctx：手动触发时客户端断开（页面关闭/网关超时）
	// 不得中断大文件打包与 S3 上传——结果落 run 历史，成败均可查询
	ctx = context.WithoutCancel(ctx)
	run := &Run{JobID: j.ID, JobName: j.Name, Trigger: trigger, Status: RunRunning, StartedAt: time.Now()}
	if err := s.db.WithContext(ctx).Create(run).Error; err != nil {
		return nil, err
	}
	fail := func(err error) (*Run, error) {
		run.Status = RunFailed
		run.Output = truncate(err.Error(), 4000)
		s.db.Model(run).Updates(map[string]any{"status": run.Status, "output": run.Output})
		s.notifyFailure(ctx, j, err)
		return run, err
	}

	if j.Type == TypeRemoteDir && s.ssh == nil {
		return fail(fmt.Errorf("SSH 执行器未注入（remote_dir 任务不可用）"))
	}
	st, err := buildStorage(*j, s.cipher)
	if err != nil {
		return fail(err)
	}
	r := &runner{
		job:       *j,
		cipher:    s.cipher,
		masterKey: s.cfg.Secrets.MasterKey,
		dataDir:   s.dataDir(),
		sshRun: func(ctx context.Context, serverID uint, cmd string, timeout time.Duration) (string, error) {
			return s.ssh.RunCommandOn(ctx, serverID, cmd, "", timeout)
		},
		sftpPull: func(ctx context.Context, serverID uint, remote string, w io.Writer) (int64, error) {
			return s.ssh.SftpDownload(serverID, remote, w)
		},
	}
	prefix := j.S3Prefix + "/"
	if j.Storage == StorageLocal {
		prefix = j.S3Prefix + "-" // 本地无目录分层：前缀-时间戳 命名
	}
	name := artifactName(prefix)

	// 构建到临时文件再上传（需要 size；本地存储则直接流式落盘）
	tmp, tmpErr := os.CreateTemp("", "custos-bk-*.tar.gz")
	if tmpErr != nil {
		return fail(tmpErr)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	mf, buildErr := r.buildArtifact(ctx, tmp)
	closeErr := tmp.Close()
	if buildErr != nil {
		return fail(buildErr)
	}
	if closeErr != nil {
		return fail(closeErr)
	}
	fi, statErr := os.Stat(tmpName)
	if statErr != nil {
		return fail(statErr)
	}

	f, openErr := os.Open(tmpName)
	if openErr != nil {
		return fail(openErr)
	}
	putCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	if err := st.Put(putCtx, name, fi.Size(), f); err != nil {
		_ = f.Close()
		return fail(fmt.Errorf("上传/落盘失败: %w", err))
	}
	_ = f.Close()

	// 保留清理（失败不阻断本次成功）
	removed, _ := enforceRetention(ctx, st, prefix, j.RetentionCount)

	run.Status = RunSuccess
	run.Artifact = name
	run.SizeBytes = fi.Size()
	run.Output = fmt.Sprintf("产物 %s（%s，%d 项%s）；保留清理 %d 个",
		name, humanBytes(fi.Size()), mf.Entries, retentionNote(removed), removed)
	s.db.Model(run).Updates(map[string]any{
		"status": run.Status, "artifact": run.Artifact, "size_bytes": run.SizeBytes, "output": run.Output,
	})
	return run, nil
}

func (s *Service) notifyFailure(ctx context.Context, j *Job, err error) {
	if s.notifier == nil {
		return
	}
	detail := fmt.Sprintf("任务：%s（%s）\n错误：%v", j.Name, j.Type, err)
	jobs.GoSafe("backup:notify-fail", func() {
		s.notifier.NotifyEvent(context.WithoutCancel(ctx),
			notify.SourceBackupFailed, notify.LevelWarn,
			fmt.Sprintf("backup-job-%d", j.ID), "备份失败："+j.Name, detail)
	})
}

func retentionNote(removed int) string {
	if removed > 0 {
		return fmt.Sprintf("，清理 %d 个超限备份", removed)
	}
	return ""
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// sanitizePrefix 对象前缀：小写字母数字-下划线（S3 key 友好）。
func sanitizePrefix(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "backup"
	}
	return out
}
