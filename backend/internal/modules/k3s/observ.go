// observ.go P8-M3.3 观测栈 DaemonSet 化：k3s 集群一键部署 node-exporter
// （指标）+ fluent-bit（容器日志→O2，O2 地址内嵌 basic auth 拆分注入）。
// 与 compose 期观测组件同一套镜像与输出语义；DaemonSet 每节点一份——
// compose 期"逐台部署"的手工模拟至此收敛为原生形态。
package k3s

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/custos-machina/backend/internal/pkg/logger"
)

const (
	observNamespace   = "custos-observ"
	nodeExporterImage = "prom/node-exporter:v1.8.2"
	fluentBitImage    = "fluent/fluent-bit:3.1.4"
)

// ObservURLFunc O2 地址供给（observ.Service.O2URL 实现，app 层注入）。
type ObservURLFunc func(ctx context.Context) string

// SetObservURL 注入 O2 地址供给（nil = 观测栈部署时报未配置）。
func (s *Service) SetObservURL(f ObservURLFunc) { s.observURL = f }

// DeployObservStack 在集群上部署观测 DaemonSet（幂等 upsert）。
// 返回部署摘要（UI toast 展示）。
func (s *Service) DeployObservStack(ctx context.Context, clusterID uint) (string, error) {
	if s.observURL == nil {
		return "", fmt.Errorf("观测栈未装配（O2 地址供给缺失）")
	}
	o2URL := s.observURL(ctx)
	if o2URL == "" {
		return "", fmt.Errorf("O2 地址未配置（管理后台 → 观测组件 → O2 地址）")
	}
	cs, err := s.Clientset(ctx, clusterID)
	if err != nil {
		return "", err
	}
	// namespace 确保
	if _, err := cs.CoreV1().Namespaces().Get(ctx, observNamespace, metav1.GetOptions{}); err != nil {
		if _, cerr := cs.CoreV1().Namespaces().Create(ctx, nsOf(observNamespace), metav1.CreateOptions{}); cerr != nil {
			return "", fmt.Errorf("建 namespace 失败: %w", cerr)
		}
	}
	// fluent-bit ConfigMap（O2 地址带 userinfo 时拆 http_User/http_Passwd）
	if err := upsertConfigMap(ctx, cs, fluentBitCM(o2URL)); err != nil {
		return "", err
	}
	for _, ds := range []*appsv1.DaemonSet{nodeExporterDS(), fluentBitDS()} {
		if err := upsertDaemonSet(ctx, cs, ds); err != nil {
			return "", err
		}
	}
	logger.Infof("[k3s] 观测栈已部署：cluster=%d ns=%s", clusterID, observNamespace)
	return fmt.Sprintf("已部署 node-exporter + fluent-bit DaemonSet（%s，每节点一份）", observNamespace), nil
}

func observLabels(app string) map[string]string {
	return map[string]string{"app": app, "custos-machina/managed": "true"}
}

func nodeExporterDS() *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "node-exporter", Namespace: observNamespace, Labels: observLabels("node-exporter")},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "node-exporter"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: observLabels("node-exporter")},
				Spec: corev1.PodSpec{
					HostNetwork: true, // 端口 9100 绑宿主（compose 期同款语义）
					Containers: []corev1.Container{{
						Name:  "node-exporter",
						Image: nodeExporterImage,
						Args:  []string{"--path.procfs=/host/proc", "--path.sysfs=/host/sys", "--path.rootfs=/rootfs"},
						Ports: []corev1.ContainerPort{{ContainerPort: 9100, HostPort: 9100}},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "proc", MountPath: "/host/proc", ReadOnly: true},
							{Name: "sys", MountPath: "/host/sys", ReadOnly: true},
							{Name: "rootfs", MountPath: "/rootfs", ReadOnly: true},
						},
						Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("64Mi"),
						}},
					}},
					Volumes: []corev1.Volume{
						{Name: "proc", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/proc"}}},
						{Name: "sys", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/sys"}}},
						{Name: "rootfs", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/"}}},
					},
				},
			},
		},
	}
}

func fluentBitDS() *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "fluent-bit", Namespace: observNamespace, Labels: observLabels("fluent-bit")},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "fluent-bit"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: observLabels("fluent-bit")},
				Spec: corev1.PodSpec{
					// 容器运行时日志：k3s containerd 落 /var/log/pods（docker.sock 输入
					// 不再适用——fluent-bit tail 插件读容器日志文件+tag 映射）
					Containers: []corev1.Container{{
						Name:  "fluent-bit",
						Image: fluentBitImage,
						VolumeMounts: []corev1.VolumeMount{
							{Name: "conf", MountPath: "/fluent-bit/etc", ReadOnly: true},
							{Name: "podslog", MountPath: "/var/log/pods", ReadOnly: true},
							{Name: "run", MountPath: "/run", ReadOnly: true},
						},
						Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("200m"), corev1.ResourceMemory: resource.MustParse("128Mi"),
						}},
					}},
					Volumes: []corev1.Volume{
						{Name: "conf", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: "fluent-bit"},
						}}},
						{Name: "podslog", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/var/log/pods"}}},
						{Name: "run", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/run"}}},
					},
				},
			},
		},
	}
}

// fluentBitCM fluent-bit 配置（tail /var/log/pods → O2 http json_lines；
// O2 地址内嵌 userinfo 时拆 http_User/http_Passwd——与 compose 期
// renderFluentBitConf 同款语义，k3s 版仅输入源从 docker 换 tail）。
func fluentBitCM(o2URL string) *corev1.ConfigMap {
	user, pass := "", ""
	if at := strings.LastIndex(o2URL, "@"); at > len("https://") && strings.Contains(o2URL[:at], ":") {
		schemeEnd := strings.Index(o2URL, "://") + 3
		creds := o2URL[schemeEnd:at]
		if sep := strings.Index(creds, ":"); sep > 0 {
			user, pass = creds[:sep], creds[sep+1:]
		}
	}
	conf := fmt.Sprintf(`[SERVICE]
    Flush 5
    Log_Level warn

[INPUT]
    Name tail
    Path /var/log/pods/*/*/*.log
    Tag pods.*
    Read_from_Head false
    Refresh_Interval 10

[OUTPUT]
    Name http
    Match *
    URI %s
    Format json_lines
    json_date_key date
    json_date_format iso8601
`, o2OutputURI(o2URL))
	if user != "" {
		conf += fmt.Sprintf("    http_User %s\n    http_Passwd %s\n", user, pass)
	}
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "fluent-bit", Namespace: observNamespace, Labels: observLabels("fluent-bit")},
		Data:       map[string]string{"fluent-bit.conf": conf},
	}
}

// o2OutputURI O2 地址原样作输出 URI（http 插件直接可达，含 userinfo 时
// 由 http_User/http_Passwd 承担认证，URI 去掉 userinfo 部分）。
func o2OutputURI(o2URL string) string {
	if at := strings.LastIndex(o2URL, "@"); at > len("https://") && strings.Contains(o2URL[:at], ":") {
		schemeEnd := strings.Index(o2URL, "://") + 3
		if strings.Contains(o2URL[schemeEnd:at], ":") {
			return o2URL[:schemeEnd] + o2URL[at+1:]
		}
	}
	return o2URL
}

func upsertConfigMap(ctx context.Context, cs *kubernetes.Clientset, cm *corev1.ConfigMap) error {
	cur, err := cs.CoreV1().ConfigMaps(cm.Namespace).Get(ctx, cm.Name, metav1.GetOptions{})
	if isNotFound(err) {
		_, err = cs.CoreV1().ConfigMaps(cm.Namespace).Create(ctx, cm, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return fmt.Errorf("查询 ConfigMap 失败: %w", err)
	}
	cm.ResourceVersion = cur.ResourceVersion
	_, err = cs.CoreV1().ConfigMaps(cm.Namespace).Update(ctx, cm, metav1.UpdateOptions{})
	return err
}

func isNotFound(err error) bool {
	return k8serrors.IsNotFound(err)
}
