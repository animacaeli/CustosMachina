package resources

import (
	"strings"
	"testing"
)

// RenderCompose 命名统一批 2/审核加固回归：行级占位符匹配（注释字样不误判）、
// 缺镜像报错、无占位符原文返回。
func TestRenderCompose(t *testing.T) {
	// 1) 标准占位符：image + container_name 均替换，返回容器名
	yaml := `services:
  server:
    image: PLATFORM_IMAGE
    container_name: PLATFORM_CONTAINER
`
	out, ct, err := RenderCompose(yaml, "demo-prod-blue", "reg.example.com/demo:v1.0")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ct != "demo-prod-blue-1" {
		t.Fatalf("container = %q", ct)
	}
	if !strings.Contains(out, "image: reg.example.com/demo:v1.0") ||
		!strings.Contains(out, "container_name: demo-prod-blue-1") {
		t.Fatalf("渲染结果:\n%s", out)
	}
	if strings.Contains(out, "PLATFORM_") {
		t.Fatalf("占位符残留:\n%s", out)
	}

	// 2) 注释里出现占位符字样：不误判（image 未用占位符时不应报缺镜像）
	yamlComment := `# image: PLATFORM_IMAGE 说明见文档
services:
  server:
    image: real:v1
`
	out2, _, err2 := RenderCompose(yamlComment, "d", "")
	if err2 != nil {
		t.Fatalf("注释误判: %v", err2)
	}
	if !strings.Contains(out2, "# image: PLATFORM_IMAGE 说明见文档") {
		t.Fatalf("注释被改写:\n%s", out2)
	}

	// 3) image 占位符但未提供镜像 → 明确报错（fail-fast 防字面量部署）
	if _, _, err3 := RenderCompose("    image: PLATFORM_IMAGE\n", "d", ""); err3 == nil {
		t.Fatal("缺镜像未报错")
	}

	// 4) 引号形态与缩进容忍
	yamlQ := `services:
  server:
    image:   "PLATFORM_IMAGE"
`
	out4, _, err4 := RenderCompose(yamlQ, "d", "x:1")
	if err4 != nil || !strings.Contains(out4, `"x:1"`) {
		t.Fatalf("引号形态: out=%s err=%v", out4, err4)
	}

	// 5) 无占位符：原文零改动（存量项目兼容）
	plain := "services:\n  server:\n    image: old:v1\n"
	out5, ct5, err5 := RenderCompose(plain, "d", "ignored")
	if err5 != nil || ct5 != "" || out5 != plain {
		t.Fatalf("原文兼容破坏: ct=%q err=%v", ct5, err5)
	}
}
