// kube.go kubeconfig → clientset 的惰性构建缓存 + 连通测试。
package k3s

import (
	"context"
	"fmt"
	"sync"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/custos-machina/backend/internal/pkg/logger"
)

// kubeCache clientset 惰性构建缓存：集群凭证变更（保存/删除）时失效。
type kubeCache struct {
	mu   sync.Mutex
	byID map[uint]*kubernetes.Clientset
}

type kubeEntry struct {
	cs      *kubernetes.Clientset
	builtAt time.Time
}

func newKubeCache() *kubeCache {
	return &kubeCache{byID: map[uint]*kubernetes.Clientset{}}
}

// Clientset 解密 kubeconfig 并构建（或取缓存）clientset。
func (s *Service) Clientset(ctx context.Context, clusterID uint) (*kubernetes.Clientset, error) {
	s.kube.mu.Lock()
	if cs, ok := s.kube.byID[clusterID]; ok {
		s.kube.mu.Unlock()
		return cs, nil
	}
	s.kube.mu.Unlock()

	var c Cluster
	if err := s.db.WithContext(ctx).First(&c, clusterID).Error; err != nil {
		return nil, fmt.Errorf("k3s 集群不存在: %w", err)
	}
	if c.Kubeenc == "" || s.cipher == nil {
		return nil, fmt.Errorf("集群 %q 未配置 kubeconfig（或平台主密钥未设置）", c.Name)
	}
	kubeconfig, err := s.cipher.Decrypt(c.Kubeenc, aadKubeconfig)
	if err != nil {
		return nil, fmt.Errorf("kubeconfig 解密失败: %w", err)
	}
	restCfg, err := clientcmd.RESTConfigFromKubeConfig([]byte(kubeconfig))
	if err != nil {
		return nil, fmt.Errorf("kubeconfig 解析失败: %w", err)
	}
	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("构建 clientset 失败: %w", err)
	}
	s.kube.mu.Lock()
	s.kube.byID[clusterID] = cs
	s.kube.mu.Unlock()
	return cs, nil
}

// Invalidate 集群凭证变更时清缓存（保存/删除调用）。
func (s *Service) Invalidate(clusterID uint) {
	s.kube.mu.Lock()
	delete(s.kube.byID, clusterID)
	s.kube.mu.Unlock()
}

// Test 连通测试：ServerVersion 查询。
func (s *Service) Test(ctx context.Context, clusterID uint) (string, error) {
	cs, err := s.Clientset(ctx, clusterID)
	if err != nil {
		return "", err
	}
	v, err := cs.ServerVersion()
	if err != nil {
		return "", fmt.Errorf("连接集群失败: %w", err)
	}
	logger.Infof("[k3s] 集群连通 OK: %s", v.GitVersion)
	return v.GitVersion, nil
}
