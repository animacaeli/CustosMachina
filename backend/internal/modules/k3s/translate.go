// translate.go compose → K8s 对象翻译（P8-M3.2 部署翻译表实现，对齐 spike §三）。
// 只取平台 compose 语义子集：services.<name> 的 image/ports/environment/
// deploy.resources（limits）/healthcheck（HTTP test）——其余字段忽略不报错
// （存量 compose 不迁移是硬约束，翻译器宽容降级）。
package k3s

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// ComposeSpec 平台 compose 语义子集（宽容解析：未知字段忽略）。
type ComposeSpec struct {
	Services map[string]ComposeService `yaml:"services"`
}

type ComposeService struct {
	Image       string            `yaml:"image"`
	Ports       []string          `yaml:"ports"` // "8080:80" / "80"
	Environment map[string]string `yaml:"environment"`
	Deploy      *struct {
		Resources *struct {
			Limits *struct {
				CPUs   string `yaml:"cpus"`
				Memory string `yaml:"memory"`
			} `yaml:"limits"`
		} `yaml:"resources"`
	} `yaml:"deploy"`
	Healthcheck *struct {
		Test []string `yaml:"test"` // ["CMD", "curl", "-f", "http://localhost/"]
	} `yaml:"healthcheck"`
}

// TranslateInput 翻译入参：namespace/标签（发布 tag）/Ingress host 与端口路由。
type TranslateInput struct {
	Namespace string
	Tag       string // 发布 tag：services[*].image 替换为 :tag（compose 里通常已带 :latest 由平台注入）
	Host      string // Ingress host（空 = 不建 Ingress）
}

// TranslateOutput 翻译产物。
type TranslateOutput struct {
	Deployment *appsv1.Deployment
	Service    *corev1.Service
	Ingress    *networkingv1.Ingress // Host 为空时 nil
}

// ParseCompose 解析 compose YAML（宽容）。
func ParseCompose(content string) (*ComposeSpec, error) {
	var spec ComposeSpec
	if err := yaml.Unmarshal([]byte(content), &spec); err != nil {
		return nil, fmt.Errorf("compose 解析失败: %w", err)
	}
	if len(spec.Services) == 0 {
		return nil, fmt.Errorf("compose 无 services 定义")
	}
	return &spec, nil
}

// Translate 翻译入口：主 service（web/app/api/main 关键词选取）为发布单元，
// 其余 service 作为同 Pod 的 sidecar 容器（compose 网络命名空间语义 =
// K8s 同 Pod 共享网络；M3.3 多容器翻译落地）。Ingress 路由主服务容器端口。
func Translate(spec *ComposeSpec, in TranslateInput) (*TranslateOutput, error) {
	name := pickMainService(spec.Services)
	svc := spec.Services[name]
	if svc.Image == "" {
		return nil, fmt.Errorf("service %q 缺 image", name)
	}
	if len(spec.Services) > 6 {
		return nil, fmt.Errorf("service 数 %d 超出单 Pod 翻译上限（6）——请拆分 compose", len(spec.Services))
	}
	image := svc.Image
	if in.Tag != "" {
		image = stripTag(image) + ":" + in.Tag
	}

	containerPort := int32(80)
	if p := firstPort(svc.Ports); p > 0 {
		containerPort = p
	}

	// env
	env := make([]corev1.EnvVar, 0, len(svc.Environment))
	for k, v := range svc.Environment {
		env = append(env, corev1.EnvVar{Name: k, Value: v})
	}

	// 资源限额（compose deploy.resources.limits）
	res := corev1.ResourceRequirements{Requests: corev1.ResourceList{}, Limits: corev1.ResourceList{}}
	if svc.Deploy != nil && svc.Deploy.Resources != nil && svc.Deploy.Resources.Limits != nil {
		l := svc.Deploy.Resources.Limits
		if l.CPUs != "" {
			if q, err := resource.ParseQuantity(l.CPUs); err == nil {
				res.Limits[corev1.ResourceCPU] = q
			}
		}
		if l.Memory != "" {
			if q, err := resource.ParseQuantity(l.Memory); err == nil {
				res.Limits[corev1.ResourceMemory] = q
			}
		}
	}
	if len(res.Limits) == 0 {
		// 未配置时给保守默认（防 BestEffort 被驱逐）
		res.Limits = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m")}
	}

	// healthcheck CMD curl → readinessProbe（仅识别 HTTP 形态；其余降级为 TCP 探针）
	probe := probeFrom(svc.Healthcheck, containerPort)

	// sidecar 容器：其余 service 并入同 Pod（不含探针——整 Pod 就绪由主容器
	// 探针代表；资源未配置时给保守限额防 BestEffort）
	sidecars := make([]corev1.Container, 0, len(spec.Services)-1)
	for svcName, sc := range spec.Services {
		if svcName == name || sc.Image == "" {
			continue
		}
		scRes := corev1.ResourceRequirements{Limits: corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("200m"),
		}}
		if sc.Deploy != nil && sc.Deploy.Resources != nil && sc.Deploy.Resources.Limits != nil {
			l := sc.Deploy.Resources.Limits
			scRes.Limits = corev1.ResourceList{}
			if l.CPUs != "" {
				if q, err := resource.ParseQuantity(l.CPUs); err == nil {
					scRes.Limits[corev1.ResourceCPU] = q
				}
			}
			if l.Memory != "" {
				if q, err := resource.ParseQuantity(l.Memory); err == nil {
					scRes.Limits[corev1.ResourceMemory] = q
				}
			}
		}
		sideEnv := make([]corev1.EnvVar, 0, len(sc.Environment))
		for k, v := range sc.Environment {
			sideEnv = append(sideEnv, corev1.EnvVar{Name: k, Value: v})
		}
		sidecars = append(sidecars, corev1.Container{
			Name: svcName, Image: tagOf(sc.Image, in.Tag), Env: sideEnv, Resources: scRes,
		})
	}

	replicas := int32(1)
	labels := map[string]string{"app": name, "custos-machina/managed": "true"}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: in.Namespace, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{Containers: append([]corev1.Container{{
					Name:           name,
					Image:          image,
					Ports:          []corev1.ContainerPort{{ContainerPort: containerPort}},
					Env:            env,
					Resources:      res,
					ReadinessProbe: probe,
				}}, sidecars...)},
			},
		},
	}

	ksvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: in.Namespace, Labels: labels},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": name},
			Ports:    []corev1.ServicePort{{Port: containerPort, TargetPort: intstr.FromInt32(containerPort)}},
		},
	}

	var ing *networkingv1.Ingress
	if in.Host != "" {
		pm := networkingv1.PathTypePrefix
		ing = &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: in.Namespace, Labels: labels},
			Spec: networkingv1.IngressSpec{
				IngressClassName: strPtr("nginx"),
				Rules: []networkingv1.IngressRule{{
					Host: in.Host,
					IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path: "/", PathType: &pm,
							Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
								Name: name, Port: networkingv1.ServiceBackendPort{Number: containerPort},
							}},
						}},
					}},
				}},
			},
		}
	}
	return &TranslateOutput{Deployment: dep, Service: ksvc, Ingress: ing}, nil
}

// pickMainService 主服务：优先名含 web/app/api/main 的，否则字典序首个。
func pickMainService(services map[string]ComposeService) string {
	best := ""
	for _, kw := range []string{"web", "app", "api", "main"} {
		for name := range services {
			if strings.Contains(strings.ToLower(name), kw) && (best == "" || name < best) {
				best = name
			}
		}
		if best != "" {
			return best
		}
	}
	for name := range services {
		if best == "" || name < best {
			best = name
		}
	}
	return best
}

func firstPort(ports []string) int32 {
	if len(ports) == 0 {
		return 0
	}
	p := ports[0]
	// "8080:80" 取容器侧（右），裸 "80" 即容器端口
	if i := strings.LastIndex(p, ":"); i >= 0 {
		p = p[i+1:]
	}
	var port int32
	if _, err := fmt.Sscanf(p, "%d", &port); err != nil {
		return 0
	}
	return port
}

// tagOf sidecar 镜像的 tag 替换（主容器同款规则）。
func tagOf(image, tag string) string {
	if tag == "" {
		return image
	}
	return stripTag(image) + ":" + tag
}

func stripTag(image string) string {
	// 保留 registry/host（含 port），仅去最后的 :tag（不含 digest @）
	if i := strings.LastIndex(image, "@"); i > 0 {
		return image[:i]
	}
	slash := strings.LastIndex(image, "/")
	if c := strings.LastIndex(image, ":"); c > slash && c > 0 {
		return image[:c]
	}
	return image
}

// probeFrom healthcheck → readinessProbe：CMD 形态含 http(s) URL 的转 HTTPGet
// （取 URL 路径与端口）；其余给 TCP 探针（宽容降级，spike §三对照）。
func probeFrom(hc *struct {
	Test []string `yaml:"test"`
}, port int32) *corev1.Probe {
	tcp := &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
		TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(port)},
	}, InitialDelaySeconds: 3, PeriodSeconds: 5}
	if hc == nil {
		return tcp
	}
	joined := strings.Join(hc.Test, " ")
	low := strings.ToLower(joined)
	for _, prefix := range []string{"http://", "https://"} {
		if i := strings.Index(low, prefix); i >= 0 {
			rest := joined[i+len(prefix):]
			if slash := strings.Index(rest, "/"); slash >= 0 {
				path := rest[slash:]
				if sp := strings.IndexAny(path, " \t\"'"); sp > 0 {
					path = path[:sp]
				}
				return &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{Path: path, Port: intstr.FromInt32(port)},
				}, InitialDelaySeconds: 3, PeriodSeconds: 5}
			}
		}
	}
	return tcp
}

func strPtr(s string) *string { return &s }
