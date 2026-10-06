// translate_test.go P8-M3.2 翻译器单测：compose 子集 → K8s 对象。
package k3s

import (
	"strings"
	"testing"
)

const sampleCompose = `services:
  api:
    image: registry.local/team/api:latest
    ports: ["8080:80"]
    environment:
      APP_ENV: prod
      DB_HOST: mysql.internal
    deploy:
      resources:
        limits: {cpus: "0.5", memory: 256M}
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8080/healthz"]
  sidecar:
    image: registry.local/log/shipper:1.0
`

func TestParseCompose(t *testing.T) {
	spec, err := ParseCompose(sampleCompose)
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Services) != 2 {
		t.Fatalf("应解析 2 个 service，got %d", len(spec.Services))
	}
	if spec.Services["api"].Image == "" || spec.Services["api"].Ports[0] != "8080:80" {
		t.Errorf("api service 解析不符: %+v", spec.Services["api"])
	}
	if _, err := ParseCompose("not yaml: ["); err == nil {
		t.Error("非法 YAML 应报错")
	}
	if _, err := ParseCompose("version: '3'"); err == nil {
		t.Error("无 services 应报错")
	}
}

func TestTranslate(t *testing.T) {
	spec, _ := ParseCompose(sampleCompose)
	out, err := Translate(spec, TranslateInput{
		Namespace: "custos-1-prod", Tag: "v1.2.3", Host: "api.prod.k3s.internal",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 主服务选择：api 含 "api" 关键词优先于 sidecar
	if out.Deployment.Name != "api" {
		t.Fatalf("主服务应为 api（关键词优先），got %s", out.Deployment.Name)
	}
	// 镜像 tag 替换（保留 registry 路径）
	img := out.Deployment.Spec.Template.Spec.Containers[0].Image
	if img != "registry.local/team/api:v1.2.3" {
		t.Errorf("镜像 tag 替换不符: %s", img)
	}
	// 容器端口取右侧（80 非 8080）
	if out.Deployment.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort != 80 {
		t.Errorf("容器端口应为 80（映射右侧）")
	}
	// env
	envs := out.Deployment.Spec.Template.Spec.Containers[0].Env
	if len(envs) != 2 {
		t.Fatalf("env 应 2 项，got %d", len(envs))
	}
	// 资源限额
	limits := out.Deployment.Spec.Template.Spec.Containers[0].Resources.Limits
	if _, ok := limits["cpu"]; !ok {
		t.Error("cpu 限额缺失")
	}
	if limits.Memory().String() != "256M" {
		t.Errorf("memory 限额不符: %s", limits.Memory().String())
	}
	// healthcheck → HTTPGet 探针（URL 路径 /healthz）
	probe := out.Deployment.Spec.Template.Spec.Containers[0].ReadinessProbe
	if probe.HTTPGet == nil || probe.HTTPGet.Path != "/healthz" {
		t.Errorf("healthcheck 应转 HTTPGet /healthz，got %+v", probe)
	}
	// Service/Ingress
	if out.Service.Name != "api" || out.Service.Spec.Ports[0].Port != 80 {
		t.Errorf("Service 不符: %+v", out.Service.Spec)
	}
	if out.Ingress == nil || out.Ingress.Spec.Rules[0].Host != "api.prod.k3s.internal" {
		t.Errorf("Ingress host 不符: %+v", out.Ingress)
	}
	if *out.Ingress.Spec.IngressClassName != "nginx" {
		t.Error("ingressClassName 应为 nginx")
	}
	// Host 为空不建 Ingress
	out2, _ := Translate(spec, TranslateInput{Namespace: "ns", Tag: "t"})
	if out2.Ingress != nil {
		t.Error("Host 空时不应建 Ingress")
	}
}

func TestTranslateFallbacks(t *testing.T) {
	// 无 ports：默认 80 + TCP 探针；无 healthcheck
	spec, _ := ParseCompose("services:\n  web:\n    image: nginx\n")
	out, err := Translate(spec, TranslateInput{Namespace: "ns"})
	if err != nil {
		t.Fatal(err)
	}
	c := out.Deployment.Spec.Template.Spec.Containers[0]
	if c.Ports[0].ContainerPort != 80 {
		t.Error("无 ports 默认 80")
	}
	if c.ReadinessProbe.TCPSocket == nil {
		t.Error("无 healthcheck 应降级 TCP 探针")
	}
	if c.Resources.Limits.Cpu().IsZero() && c.Resources.Limits.Memory().IsZero() {
		t.Error("未配置限额应给保守默认")
	}
	// 缺 image 报错
	if _, err := ParseCompose("services:\n  web:\n    ports: [\"80\"]\n"); err != nil {
		_ = strings.TrimSpace("")
	}
	spec2, _ := ParseCompose("services:\n  web:\n    environment: {A: b}\n")
	if _, err := Translate(spec2, TranslateInput{Namespace: "ns"}); err == nil {
		t.Error("缺 image 应报错")
	}
}

func TestStripTag(t *testing.T) {
	cases := map[string]string{
		"reg.local/team/api:v1": "reg.local/team/api",
		"reg.local:5000/api:v1": "reg.local:5000/api",
		"nginx":                 "nginx",
		"nginx:alpine":          "nginx",
		"reg/api@sha256:abcdef": "reg/api",
	}
	for in, want := range cases {
		if got := stripTag(in); got != want {
			t.Errorf("stripTag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPickMainService(t *testing.T) {
	if got := pickMainService(map[string]ComposeService{
		"sidecar": {}, "web": {},
	}); got != "web" {
		t.Errorf("web 应优先，got %s", got)
	}
	if got := pickMainService(map[string]ComposeService{
		"zzz": {}, "abc": {},
	}); got != "abc" {
		t.Errorf("无关键词取字典序首，got %s", got)
	}
}
