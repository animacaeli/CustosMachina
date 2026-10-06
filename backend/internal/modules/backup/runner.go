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
	"time"

	"github.com/custos-machina/backend/internal/pkg/crypto"
	"github.com/custos-machina/backend/internal/pkg/shellx"
	"github.com/custos-machina/backend/internal/pkg/strx"
)

// appVersion 产物 manifest 记录的平台版本（恢复时可判断兼容性）。
var appVersion = "dev"

// SetAppVersion 由 main 启动时注入（ldflags 或常量）。
func SetAppVersion(v string) { appVersion = v }

func timeNowUTC() string { return time.Now().UTC().Format(time.RFC3339) }
func timeNowUnix() int64 { return time.Now().Unix() }

// runner 产物构建器：把任务定义变成一个 tar.gz 流（含 manifest）。
type runner struct {
	job       Job
	cipher    *crypto.Cipher
	masterKey string // 平台主密钥（仅 platform_self 的密钥份额用）
	dataDir   string // 平台数据目录（sqlite DSN 推导）
	sshRun    func(ctx context.Context, serverID uint, cmd string, timeout time.Duration) (string, error)
	sftpPull  func(ctx context.Context, serverID uint, remote string, w io.Writer) (int64, error)
}

// manifest 产物内清单（restore 脚本读取）。
type manifest struct {
	Type      string `json:"type"`
	CreatedAt string `json:"createdAt"`
	AppVer    string `json:"appVersion"`
	KeyShare  string `json:"keyShare,omitempty"` // platform_self：口令加密的密钥份额文件名
	HasDB     bool   `json:"hasDb"`
	Entries   int    `json:"entries"`
}

// SSHRunner / SFTPPuller 由 resources.Service 提供（app 层注入，避免模块反向依赖）。
type SSHRunner interface {
	RunCommandOn(ctx context.Context, serverID uint, cmd, stdin string, timeout time.Duration) (string, error)
	SftpDownload(serverID uint, name string, w io.Writer) (int64, error)
}

// buildArtifact 构建产物写入 w。返回 manifest（含统计）。
func (r *runner) buildArtifact(ctx context.Context, w io.Writer) (*manifest, error) {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	mf := &manifest{Type: r.job.Type, AppVer: appVersion}

	switch r.job.Type {
	case TypePlatformSelf:
		if err := r.writePlatformSelf(ctx, tw, mf); err != nil {
			return nil, err
		}
	case TypeRemoteDir:
		if err := r.writeRemoteDir(ctx, tw, mf); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("未知任务类型 %q", r.job.Type)
	}

	// manifest 收尾
	mf.CreatedAt = timeNowUTC()
	if err := writeTarJSON(tw, "manifest.json", mf); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return mf, nil
}

// writePlatformSelf 平台自身备份：
// custos.db（sqlite 文件）+ 数据目录（排除 backups/ 与 logs/）+ keyshare.enc。
func (r *runner) writePlatformSelf(ctx context.Context, tw *tar.Writer, mf *manifest) error {
	if r.masterKey == "" {
		return fmt.Errorf("平台主密钥不可用（配置缺失）")
	}
	// 密钥份额：口令加密 master key（restore 时 openssl 解开）
	passEnc, err := r.cipher.Decrypt(r.job.PassphraseEnc, aadPassphrase)
	if err != nil || passEnc == "" {
		return fmt.Errorf("备份口令未配置或解密失败（platform_self 必须配置口令）")
	}
	shareJSON, _ := json.Marshal(map[string]string{"masterKey": r.masterKey})
	share, err := crypto.PassphraseEncrypt(shareJSON, passEnc)
	if err != nil {
		return fmt.Errorf("密钥份额加密失败: %w", err)
	}
	if err := writeTarBytes(tw, "keyshare.enc", share); err != nil {
		return err
	}
	mf.KeyShare = "keyshare.enc"

	// 数据目录（含 DB 文件——sqlite 即数据目录里的一个文件）
	root := r.dataDir
	if root == "" {
		return fmt.Errorf("无法推导平台数据目录（仅支持 sqlite 部署的自备份）")
	}
	mf.HasDB = true
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		// 排除：备份产物自身目录与可再生日志
		if d.IsDir() && (rel == "backups" || rel == "logs") {
			return filepath.SkipDir
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil {
			return statErr
		}
		hdr := &tar.Header{Name: filepath.ToSlash(filepath.Join("data", rel)), Size: info.Size(), Mode: int64(info.Mode()), ModTime: info.ModTime()}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, openErr := os.Open(path)
		if openErr != nil {
			return openErr
		}
		defer func() { _ = f.Close() }()
		if _, err := io.Copy(tw, f); err != nil {
			return err
		}
		mf.Entries++
		return nil
	})
}

// writeRemoteDir 远端目录备份：SSH tar 打包 → SFTP 拉回流式并入产物。
// 产物内路径 remote/<basename>.tar.gz（嵌套 tar，恢复侧整目录还原）。
func (r *runner) writeRemoteDir(ctx context.Context, tw *tar.Writer, mf *manifest) error {
	if r.job.ServerID == 0 || r.job.RemotePath == "" {
		return fmt.Errorf("remote_dir 任务缺少目标主机或目录")
	}
	remotePath := strings.TrimRight(r.job.RemotePath, "/")
	parent, base := filepath.Split(remotePath)
	if base == "" || base == "." {
		return fmt.Errorf("远端目录路径不合法: %q", r.job.RemotePath)
	}
	tmpRemote := fmt.Sprintf("/tmp/custos-bk-%d.tar.gz", timeNowUnix())
	// 打包（在远端执行；失败时输出带回）。parent/base 源自用户填写的
	// RemotePath，必须 shellQuote——校验白名单在 validateJob，这里是第二道防线
	cmd := fmt.Sprintf("tar -czf %s -C %s %s && du -sh %s | cut -f1",
		tmpRemote, shellx.Quote(strings.TrimRight(parent, "/")), shellx.Quote(base), tmpRemote)
	out, err := r.sshRun(ctx, r.job.ServerID, cmd, 10*time.Minute)
	if err != nil {
		return fmt.Errorf("远端打包失败: %v（%s）", err, truncate(out, 300))
	}
	defer func() {
		// best-effort 清理远端临时文件
		_, _ = r.sshRun(context.WithoutCancel(ctx), r.job.ServerID, "rm -f "+tmpRemote, 30*time.Second)
	}()

	// 拉回流式写入产物。读端任何失败路径都要 CloseWithError 解锁写端——
	// io.Pipe 无 finalizer，读端被 GC 不会唤醒阻塞在 Write 的写端，
	// goroutine 与 SFTP 连接永久泄漏（v0.12.1 复核 N5）
	pr, pw := io.Pipe()
	defer func() { _ = pr.CloseWithError(io.EOF) }()
	go func() {
		_, pullErr := r.sftpPull(ctx, r.job.ServerID, tmpRemote, pw)
		_ = pw.CloseWithError(pullErr)
	}()
	gz, gzErr := gzip.NewReader(pr)
	if gzErr != nil {
		return fmt.Errorf("远端产物读取失败: %w", gzErr)
	}
	defer func() { _ = gz.Close() }()
	inner := tar.NewReader(gz)
	count := 0
	for {
		hdr, hdrErr := inner.Next()
		if hdrErr == io.EOF {
			break
		}
		if hdrErr != nil {
			return fmt.Errorf("远端产物解析失败: %w", hdrErr)
		}
		// 条目名逐条校验后显式拼前缀——不能用 filepath.Join：它的 Clean 会
		// 吃掉 ../，恶意条目名将逃出 remote/ 前缀写坏产物结构
		name := strings.TrimPrefix(filepath.ToSlash(hdr.Name), "./")
		if name == "" || strings.HasPrefix(name, "/") || name == ".." || strings.Contains(name, "../") {
			return fmt.Errorf("远端产物条目名不合法: %q", hdr.Name)
		}
		hdr.Name = "remote/" + name
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := io.Copy(tw, inner); err != nil {
			return err
		}
		count++
	}
	if count == 0 {
		return fmt.Errorf("远端目录为空或打包失败")
	}
	mf.Entries = count
	return nil
}

func writeTarBytes(tw *tar.Writer, name string, body []byte) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(body)), Mode: 0o600}); err != nil {
		return err
	}
	_, err := tw.Write(body)
	return err
}

func writeTarJSON(tw *tar.Writer, name string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeTarBytes(tw, name, b)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strx.Truncate(s, n) + "..." // rune 安全（防切碎中文，v0.12.0 审计）
}
