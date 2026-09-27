// sftp.go SFTP 文件管理（第四阶段 M4，docs/plan-phase4-runtime.md 第三节第 4 项）：
// github.com/pkg/sftp 架在既有 SSH 拨号与凭据体系上（零新增认证面）。
// 每次操作独立连接（与容器/部署通道同一取舍：操作由人触发、频率低）。
// 权限：admin/ops 可写，dev 只读浏览（casbin + handler 双层拦截）。
// 大文件走 scp/直传（>100MB 前端提示，后端不设硬限）。
package resources

import (
	"context"
	"fmt"
	"io"
	"path"
	"sort"
	"time"

	"github.com/pkg/sftp"
)

const (
	sftpIODialTimeout  = 20 * time.Second
	sftpMaxEditBytes   = 1 << 20   // 1MB：在线编辑上限（超出提示下载改完再传）
	sftpMaxUploadBytes = 100 << 20 // 100MB：上传上限（再大走 scp/直传）
	sftpMaxListEntries = 2000
)

func (s *Service) sftpClient(serverID uint) (*sftp.Client, func(), error) {
	srv, cred, err := s.serverWithCredential(context.Background(), serverID)
	if err != nil {
		return nil, nil, err
	}
	sshClient, err := DialSSH(srv.Host, srv.Port, cred, srv.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("SSH 连接失败: %w", err)
	}
	client, err := sftp.NewClient(sshClient)
	if err != nil {
		sshClient.Close()
		return nil, nil, fmt.Errorf("SFTP 协商失败: %w", err)
	}
	return client, func() { client.Close(); sshClient.Close() }, nil
}

// FileEntry 目录条目。
type FileEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"isDir"`
	Size  int64  `json:"size"`
	Mtime int64  `json:"mtime"` // Unix 秒；目录为 0
}

// normalizeRemotePath 归一化远端路径（拒绝空与相对路径），防误操作当前目录。
func normalizeRemotePath(p string) (string, error) {
	p = path.Clean(p)
	if !path.IsAbs(p) {
		return "", fmt.Errorf("路径须为绝对路径")
	}
	return p, nil
}

// SftpList 列目录（目录在前、按名排序，上限 2000 条）。
func (s *Service) SftpList(serverID uint, dir string) ([]FileEntry, error) {
	dir, err := normalizeRemotePath(dir)
	if err != nil {
		return nil, err
	}
	client, closeFn, err := s.sftpClient(serverID)
	if err != nil {
		return nil, err
	}
	defer closeFn()
	infos, err := client.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("读取目录失败（不存在或无权限）: %w", err)
	}
	entries := make([]FileEntry, 0, len(infos))
	for _, fi := range infos {
		e := FileEntry{Name: fi.Name(), IsDir: fi.IsDir()}
		if !e.IsDir {
			e.Size = fi.Size()
			e.Mtime = fi.ModTime().Unix()
		}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return entries[i].Name < entries[j].Name
	})
	if len(entries) > sftpMaxListEntries {
		entries = entries[:sftpMaxListEntries]
	}
	return entries, nil
}

// SftpRead 读小文件（在线编辑用；>1MB 拒绝）。
func (s *Service) SftpRead(serverID uint, name string) (string, error) {
	name, err := normalizeRemotePath(name)
	if err != nil {
		return "", err
	}
	client, closeFn, err := s.sftpClient(serverID)
	if err != nil {
		return "", err
	}
	defer closeFn()
	fi, err := client.Stat(name)
	if err != nil {
		return "", fmt.Errorf("文件不存在或无权限: %w", err)
	}
	if fi.Size() > sftpMaxEditBytes {
		return "", fmt.Errorf("文件 %dKB 超过在线编辑上限 1MB（下载修改后重新上传）", fi.Size()>>10)
	}
	f, err := client.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf, err := io.ReadAll(io.LimitReader(f, sftpMaxEditBytes+1))
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

// backupAndSwap 覆盖写的安全序列：写 tmp → 时间戳备份原文件 → tmp 换到原名。
// tmp 先落盘保证"写失败原文件不动"（直接 rename 原文件后 Create 失败会丢原文件）。
func backupAndSwap(client *sftp.Client, name string, write func(*sftp.File) error) error {
	tmp := name + ".custos-tmp"
	tf, err := client.Create(tmp)
	if err != nil {
		return fmt.Errorf("创建临时文件失败（目录无权限?）: %w", err)
	}
	if err := write(tf); err != nil {
		tf.Close()
		client.Remove(tmp) // 清理半写的 tmp，原文件未动
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err := tf.Close(); err != nil {
		return err
	}
	if _, err := client.Stat(name); err == nil {
		bak := fmt.Sprintf("%s.bak.%d", name, time.Now().Unix())
		if err := client.Rename(name, bak); err != nil {
			client.Remove(tmp)
			return fmt.Errorf("备份原文件失败: %w", err)
		}
	}
	if err := client.Rename(tmp, name); err != nil {
		return fmt.Errorf("落盘失败（原文件已备份）: %w", err)
	}
	return nil
}

// SftpWrite 覆盖写小文件（在线编辑保存；>1MB 拒绝，大文件走 SftpUpload）。
func (s *Service) SftpWrite(serverID uint, name string, content []byte) error {
	name, err := normalizeRemotePath(name)
	if err != nil {
		return err
	}
	if int64(len(content)) > sftpMaxEditBytes {
		return fmt.Errorf("内容超过 1MB 上限（大文件请走上传）")
	}
	client, closeFn, err := s.sftpClient(serverID)
	if err != nil {
		return err
	}
	defer closeFn()
	werr := backupAndSwap(client, name, func(f *sftp.File) error {
		_, err := f.Write(content)
		return err
	})
	s.auditSftp(serverID, "write", name, werr)
	return werr
}

// SftpUpload 流式上传（multipart 大文件；上限 100MB，边读边写不整读内存）。
func (s *Service) SftpUpload(serverID uint, name string, r io.Reader, size int64) error {
	nm, err := normalizeRemotePath(name)
	if err != nil {
		return err
	}
	if size > sftpMaxUploadBytes {
		return fmt.Errorf("文件超过 100MB 上限，请改用 scp/直传")
	}
	client, closeFn, err := s.sftpClient(serverID)
	if err != nil {
		return err
	}
	defer closeFn()
	werr := backupAndSwap(client, nm, func(f *sftp.File) error {
		_, err := io.Copy(f, io.LimitReader(r, sftpMaxUploadBytes+1))
		return err
	})
	s.auditSftp(serverID, "upload", nm, werr)
	return werr
}

// auditSftp 写操作落审计事件。
func (s *Service) auditSftp(serverID uint, action, name string, err error) {
	s.recordSimpleEvent(context.Background(), serverID, "sftp_"+action,
		fmt.Sprintf("SFTP %s %s：%s", action, name, map[bool]string{true: "成功", false: "失败"}[err == nil]))
}

// SftpMkdir 建目录。
func (s *Service) SftpMkdir(serverID uint, name string) error {
	name, err := normalizeRemotePath(name)
	if err != nil {
		return err
	}
	client, closeFn, err := s.sftpClient(serverID)
	if err != nil {
		return err
	}
	defer closeFn()
	err = client.Mkdir(name)
	s.auditSftp(serverID, "mkdir", name, err)
	return err
}

// SftpRename 重命名/移动（同文件系统）。
func (s *Service) SftpRename(serverID uint, oldName, newName string) error {
	oldName, err := normalizeRemotePath(oldName)
	if err != nil {
		return err
	}
	newName, err = normalizeRemotePath(newName)
	if err != nil {
		return err
	}
	client, closeFn, err := s.sftpClient(serverID)
	if err != nil {
		return err
	}
	defer closeFn()
	rerr := client.Rename(oldName, newName)
	s.auditSftp(serverID, "rename", oldName+" -> "+newName, rerr)
	return rerr
}

// SftpRemove 删除文件或空目录。
func (s *Service) SftpRemove(serverID uint, name string, isDir bool) error {
	name, err := normalizeRemotePath(name)
	if err != nil {
		return err
	}
	client, closeFn, err := s.sftpClient(serverID)
	if err != nil {
		return err
	}
	defer closeFn()
	if isDir {
		err = client.RemoveDirectory(name) // 只删空目录：防误删整棵树
	} else {
		err = client.Remove(name)
	}
	s.auditSftp(serverID, "remove", name, err)
	return err
}

// SftpDownload 流式下载（handler 直接拷给响应）。
func (s *Service) SftpDownload(serverID uint, name string, w io.Writer) (int64, error) {
	name, err := normalizeRemotePath(name)
	if err != nil {
		return 0, err
	}
	client, closeFn, err := s.sftpClient(serverID)
	if err != nil {
		return 0, err
	}
	defer closeFn()
	f, err := client.Open(name)
	if err != nil {
		return 0, fmt.Errorf("打开文件失败: %w", err)
	}
	defer f.Close()
	return io.Copy(w, f)
}
