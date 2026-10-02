package backup

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/custos-machina/backend/internal/pkg/crypto"
)

// storage 存储抽象：上传一个备份产物 + 按保留数清理最旧。
// local 与 s3 双实现（rule of two：恰好两个实现，抽象成立）。
type storage interface {
	Put(ctx context.Context, name string, size int64, r io.Reader) error
	List(ctx context.Context, prefix string) ([]string, error)
	Delete(ctx context.Context, name string) error
	fmt.Stringer
}

func buildStorage(j Job, cipher *crypto.Cipher) (storage, error) {
	switch j.Storage {
	case StorageLocal:
		dir := j.LocalDir
		if dir == "" {
			return nil, fmt.Errorf("本地存储目录未配置")
		}
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, err
		}
		return &localStorage{dir: dir}, nil
	case StorageS3:
		if j.S3Endpoint == "" || j.S3Bucket == "" || j.S3AccessKey == "" || j.S3SecretEnc == "" {
			return nil, fmt.Errorf("S3 存储配置不完整（endpoint/bucket/accessKey/secretKey）")
		}
		secret, err := cipher.Decrypt(j.S3SecretEnc)
		if err != nil {
			return nil, fmt.Errorf("S3 secretKey 解密失败: %w", err)
		}
		client, err := minio.New(j.S3Endpoint, &minio.Options{
			Creds: credentials.NewStaticV4(j.S3AccessKey, secret, ""),
			Secure: !strings.HasPrefix(j.S3Endpoint, "localhost") &&
				!strings.HasPrefix(j.S3Endpoint, "127.0.0.1"),
		})
		if err != nil {
			return nil, err
		}
		return &s3Storage{client: client, bucket: j.S3Bucket}, nil
	default:
		return nil, fmt.Errorf("未知存储类型 %q", j.Storage)
	}
}

type localStorage struct{ dir string }

func (l *localStorage) String() string { return "local:" + l.dir }

func (l *localStorage) Put(_ context.Context, name string, _ int64, r io.Reader) error {
	tmp := filepath.Join(l.dir, name+".part")
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(l.dir, name))
}

func (l *localStorage) List(_ context.Context, prefix string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(l.dir, prefix+"*"))
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if strings.HasSuffix(m, ".part") {
			continue
		}
		out = append(out, filepath.Base(m))
	}
	sort.Strings(out)
	return out, nil
}

func (l *localStorage) Delete(_ context.Context, name string) error {
	return os.Remove(filepath.Join(l.dir, name))
}

type s3Storage struct {
	client *minio.Client
	bucket string
}

func (s *s3Storage) String() string { return "s3:" + s.bucket }

func (s *s3Storage) Put(ctx context.Context, name string, size int64, r io.Reader) error {
	_, err := s.client.PutObject(ctx, s.bucket, name, r, size, minio.PutObjectOptions{
		ContentType: "application/gzip",
	})
	return err
}

func (s *s3Storage) List(ctx context.Context, prefix string) ([]string, error) {
	var out []string
	for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		out = append(out, obj.Key)
	}
	sort.Strings(out)
	return out, nil
}

func (s *s3Storage) Delete(ctx context.Context, name string) error {
	return s.client.RemoveObject(ctx, s.bucket, name, minio.RemoveObjectOptions{})
}

// enforceRetention 保留策略：按名称时序（产物名含时间戳，字典序即时序）
// 保留最新 retentionCount 个，其余删除。
func enforceRetention(ctx context.Context, st storage, prefix string, retention int) (removed int, err error) {
	if retention <= 0 {
		return 0, nil
	}
	names, err := st.List(ctx, prefix)
	if err != nil {
		return 0, err
	}
	// 只清理本任务命名规则的产物（前缀过滤已保证）
	for len(names) > retention {
		oldest := names[0]
		if dErr := st.Delete(ctx, oldest); dErr != nil {
			return removed, dErr
		}
		removed++
		names = names[1:]
	}
	return removed, nil
}

// artifactName 产物命名：prefix + 时间戳（字典序即时间序，保留清理依赖）。
func artifactName(jobPrefix string) string {
	return fmt.Sprintf("%s%s.tar.gz", jobPrefix, time.Now().UTC().Format("20060102T150405Z"))
}
