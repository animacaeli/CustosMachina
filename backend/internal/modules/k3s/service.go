// service.go k3s Service：集群 CRUD + 部署/回滚/状态（M3.2 最小承载）。
package k3s

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"gorm.io/gorm"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/custos-machina/backend/internal/pkg/crypto"
)

var ErrNotFound = errors.New("k3s 集群不存在")

type Service struct {
	db        *gorm.DB
	cipher    *crypto.Cipher
	kube      *kubeCache
	observURL ObservURLFunc // P8-M3.3：观测栈 O2 地址供给（app 注入）
}

func NewService(db *gorm.DB, cipher *crypto.Cipher) *Service {
	return &Service{db: db, cipher: cipher, kube: newKubeCache()}
}

// ---- 集群 CRUD ----

type SaveClusterInput struct {
	Name       string `json:"name" binding:"required,max=64"`
	Kubeconfig string `json:"kubeconfig" binding:"max=65536"` // 保存时必填一次；留空保留（AES 加密落库）
	Domain     string `json:"domain" binding:"omitempty,max=255"`
	Remark     string `json:"remark" binding:"max=255"`
}

func (s *Service) List(ctx context.Context) ([]Cluster, error) {
	var out []Cluster
	if err := s.db.WithContext(ctx).Order("id").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) Save(ctx context.Context, id uint, in SaveClusterInput) (*Cluster, error) {
	if s.cipher == nil {
		return nil, fmt.Errorf("平台主密钥未配置，无法保存 kubeconfig")
	}
	var c Cluster
	err := s.db.WithContext(ctx).First(&c, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// 新建：kubeconfig 必填
		if in.Kubeconfig == "" {
			return nil, fmt.Errorf("新建集群必须提供 kubeconfig")
		}
		c = Cluster{Name: in.Name, Domain: in.Domain, Remark: in.Remark}
	} else if err != nil {
		return nil, err
	} else {
		c.Name, c.Domain, c.Remark = in.Name, in.Domain, in.Remark
	}
	if in.Kubeconfig != "" {
		enc, err := s.cipher.Encrypt(in.Kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("kubeconfig 加密失败: %w", err)
		}
		c.Kubeenc = enc
	}
	if err := s.db.WithContext(ctx).Save(&c).Error; err != nil {
		return nil, err
	}
	s.Invalidate(c.ID)
	return &c, nil
}

func (s *Service) Delete(ctx context.Context, id uint) error {
	res := s.db.WithContext(ctx).Delete(&Cluster{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	s.Invalidate(id)
	return nil
}

// ---- 部署（release 的 k3s 分流调用）----

// DeployInput 发布入参（release.Execute 的 k3s 路径组装）。
type DeployInput struct {
	ClusterID   uint
	Namespace   string // 项目-环境（custos-<project>-<env>）
	ComposeYAML string
	Tag         string // 发布 tag（镜像替换）
	ProjectName string // Host 组装用（<project>-<env>.<cluster.Domain>）
	EnvType     string
	Host        string // 显式 host（非空优先于域名策略）
}

// Deploy 翻译 + apply（create-or-update）+ 等 rollout ready。
// 返回耗时与部署名（Release 记录的载体信息）。
func (s *Service) Deploy(ctx context.Context, in DeployInput) (*DeployResult, error) {
	spec, err := ParseCompose(in.ComposeYAML)
	if err != nil {
		return nil, err
	}
	// Host 域名策略（M3.3）：显式 Host 优先；否则集群 Domain 配置时
	// 组装 <project>-<env>.<domain>；都空不建 Ingress（仅集群内 Service）
	host := in.Host
	if host == "" && in.ProjectName != "" {
		var c Cluster
		if err := s.db.WithContext(ctx).Select("domain").First(&c, in.ClusterID).Error; err == nil && c.Domain != "" {
			host = fmt.Sprintf("%s-%s.%s", in.ProjectName, in.EnvType, c.Domain)
		}
	}
	t, err := Translate(spec, TranslateInput{Namespace: in.Namespace, Tag: in.Tag, Host: host})
	if err != nil {
		return nil, err
	}
	cs, err := s.Clientset(ctx, in.ClusterID)
	if err != nil {
		return nil, err
	}
	// namespace 确保
	if _, err := cs.CoreV1().Namespaces().Get(ctx, in.Namespace, metav1.GetOptions{}); err != nil {
		if _, cerr := cs.CoreV1().Namespaces().Create(ctx, nsOf(in.Namespace), metav1.CreateOptions{}); cerr != nil {
			return nil, fmt.Errorf("建 namespace 失败: %w", cerr)
		}
	}
	// Service create-or-update（先建，Deployment 引用其就绪）
	if _, err := upsertService(ctx, cs, t.Service); err != nil {
		return nil, err
	}
	dep, err := upsertDeployment(ctx, cs, t.Deployment)
	if err != nil {
		return nil, err
	}
	if t.Ingress != nil {
		if _, err := upsertIngress(ctx, cs, t.Ingress); err != nil {
			return nil, err
		}
	}
	// rollout 等待（由 release 层控制总超时；此处 5min 硬顶）
	started := time.Now()
	if err := waitReady(ctx, cs, in.Namespace, dep.Name, 5*time.Minute); err != nil {
		return nil, err
	}
	return &DeployResult{
		Name: dep.Name, Namespace: in.Namespace, Image: dep.Spec.Template.Spec.Containers[0].Image,
		RolloutSecs: int(time.Since(started).Seconds()),
	}, nil
}

type DeployResult struct {
	Name        string `json:"name"`
	Namespace   string `json:"namespace"`
	Image       string `json:"image"`
	RolloutSecs int    `json:"rolloutSecs"`
}

// Rollback 回滚到上一 revision（client-go 无现成 undo——等价实现：
// 取倒数第二个 ReplicaSet 的 pod template 回写 Deployment，即 kubectl rollout undo）。
func (s *Service) Rollback(ctx context.Context, clusterID uint, namespace, name string) error {
	cs, err := s.Clientset(ctx, clusterID)
	if err != nil {
		return err
	}
	rsList, err := cs.AppsV1().ReplicaSets(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", name),
	})
	if err != nil {
		return fmt.Errorf("查询 ReplicaSet 失败: %w", err)
	}
	if len(rsList.Items) < 2 {
		return fmt.Errorf("无历史 revision 可回滚（首个版本）")
	}
	// revision 注解排序取上一代
	sort.Slice(rsList.Items, func(i, j int) bool {
		return revisionOf(rsList.Items[i]) > revisionOf(rsList.Items[j])
	})
	prev := rsList.Items[1]
	cur, err := cs.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	cur.Spec.Template = prev.Spec.Template
	if _, err := cs.AppsV1().Deployments(namespace).Update(ctx, cur, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("回写上一版本失败: %w", err)
	}
	return waitReady(ctx, cs, namespace, name, 3*time.Minute)
}

func revisionOf(rs appsv1.ReplicaSet) int64 {
	v, _ := strconv.ParseInt(rs.Annotations["deployment.kubernetes.io/revision"], 10, 64)
	return v
}

// waitReady rollout 完成三条件（spike 验证口径）。
func waitReady(ctx context.Context, cs *kubernetes.Clientset, namespace, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		d, err := cs.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil && d.Generation == d.Status.ObservedGeneration &&
			d.Status.UpdatedReplicas == *d.Spec.Replicas && d.Status.AvailableReplicas == *d.Spec.Replicas {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("rollout 超时（%s）: %s/%s", timeout, namespace, name)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
