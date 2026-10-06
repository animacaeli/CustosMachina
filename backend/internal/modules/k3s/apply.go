// apply.go create-or-update 辅助（最小承载的 upsert 语义：整对象替换，
// Deployment 保留 revision 历史由 K8s 自动管理）。
package k3s

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func nsOf(name string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

func upsertService(ctx context.Context, cs *kubernetes.Clientset, svc *corev1.Service) (*corev1.Service, error) {
	cur, err := cs.CoreV1().Services(svc.Namespace).Get(ctx, svc.Name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		return cs.CoreV1().Services(svc.Namespace).Create(ctx, svc, metav1.CreateOptions{})
	}
	if err != nil {
		return nil, fmt.Errorf("查询 Service 失败: %w", err)
	}
	svc.ResourceVersion = cur.ResourceVersion
	svc.Spec.ClusterIP = cur.Spec.ClusterIP // 不可变字段沿用
	return cs.CoreV1().Services(svc.Namespace).Update(ctx, svc, metav1.UpdateOptions{})
}

func upsertDeployment(ctx context.Context, cs *kubernetes.Clientset, dep *appsv1.Deployment) (*appsv1.Deployment, error) {
	cur, err := cs.AppsV1().Deployments(dep.Namespace).Get(ctx, dep.Name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		return cs.AppsV1().Deployments(dep.Namespace).Create(ctx, dep, metav1.CreateOptions{})
	}
	if err != nil {
		return nil, fmt.Errorf("查询 Deployment 失败: %w", err)
	}
	dep.ResourceVersion = cur.ResourceVersion
	return cs.AppsV1().Deployments(dep.Namespace).Update(ctx, dep, metav1.UpdateOptions{})
}

func upsertIngress(ctx context.Context, cs *kubernetes.Clientset, ing *networkingv1.Ingress) (*networkingv1.Ingress, error) {
	cur, err := cs.NetworkingV1().Ingresses(ing.Namespace).Get(ctx, ing.Name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		return cs.NetworkingV1().Ingresses(ing.Namespace).Create(ctx, ing, metav1.CreateOptions{})
	}
	if err != nil {
		return nil, fmt.Errorf("查询 Ingress 失败: %w", err)
	}
	ing.ResourceVersion = cur.ResourceVersion
	return cs.NetworkingV1().Ingresses(ing.Namespace).Update(ctx, ing, metav1.UpdateOptions{})
}
