package resources

import (
	"testing"
)

// docker compose ps --format json 的两种输出形态都须解析（WaitComposeHealthy 依赖）。
func TestParseComposePS(t *testing.T) {
	arr := `[{"Name":"demo-web-1","State":"running","Health":"healthy"},{"Name":"demo-db-1","State":"running","Health":""}]`
	if items := parseComposePS(arr); len(items) != 2 || items[1].Health != "" {
		t.Fatalf("数组形态解析异常: %+v", items)
	}
	lines := "{\"Name\":\"a-1\",\"State\":\"running\",\"Health\":\"healthy\"}\n{\"Name\":\"b-1\",\"State\":\"exited\",\"Health\":\"\"}"
	items := parseComposePS(lines)
	if len(items) != 2 || items[1].State != "exited" {
		t.Fatalf("逐行形态解析异常: %+v", items)
	}
	if items := parseComposePS(""); len(items) != 0 {
		t.Fatalf("空输出应返回空, got %+v", items)
	}
	if items := parseComposePS("Error response from daemon"); len(items) != 0 {
		t.Fatalf("非 JSON 输出应返回空, got %+v", items)
	}
}
