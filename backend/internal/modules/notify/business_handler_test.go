// business_handler_test.go P7-M5：公开入口限速/鉴权/契约的 handler 级集成测试。
package notify

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func bizRequest(r *gin.Engine, token, body string) int {
	req := httptest.NewRequest(http.MethodPost, "/api/notify/business", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestBusinessAlert_Handler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := newRuleTestEnv(t)
	out, err := env.svc.IssueBusinessToken(context.Background(), "t", "app-x")
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(env.svc)
	r := gin.New()
	r.POST("/api/notify/business", h.businessAlert)

	body := `{"app":"app-x","level":"warn","title":"t1"}`
	if got := bizRequest(r, "", body); got != 401 {
		t.Errorf("无 token 应 401，got %d", got)
	}
	if got := bizRequest(r, "bad", body); got != 401 {
		t.Errorf("错 token 应 401，got %d", got)
	}
	if got := bizRequest(r, out.Plaintext, body); got != 200 {
		t.Errorf("正确 token 应 200，got %d", got)
	}
	if got := bizRequest(r, out.Plaintext, `{"app":"app-x","level":"error","title":"x"}`); got != 400 {
		t.Errorf("非法 level 应 400，got %d", got)
	}

	// 限速：同 handler 实例连打（httptest RemoteAddr 固定 192.0.2.1）
	hit429 := 0
	for i := 0; i < 62; i++ {
		if code := bizRequest(r, out.Plaintext, body); code == 429 {
			hit429 = i + 1
			break
		}
	}
	if hit429 == 0 {
		t.Error("62 次同 IP 请求应触发 429")
	} else {
		t.Logf("第 %d 次触发 429", hit429)
	}
}
