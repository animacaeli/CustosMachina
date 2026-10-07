package server

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// v0.12.14 独立审核 N3 回归：慢速 body 必须在期限内断开。
// http.Server 只设 ReadHeaderTimeout（SSE/WS 不能全局 ReadTimeout），
// bodyDeadline 用 ResponseController 按请求分层设期限。
func TestBodyDeadlineCutsSlowBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	readErr := make(chan error, 1)
	e.POST("/api/demo", bodyDeadlineWith(100*time.Millisecond, time.Minute), func(c *gin.Context) {
		_, err := io.ReadAll(c.Request.Body)
		readErr <- err
		c.Status(200)
	})

	srv := httptest.NewServer(e)
	defer srv.Close()

	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(srv.URL, "http://"), 2*time.Second)
	if err != nil {
		t.Fatalf("拨号失败: %v", err)
	}
	defer conn.Close()
	// 声明 8 字节 body 只写 3 字节，然后挂住——慢速 body 占连接
	if _, err := io.WriteString(conn, "POST /api/demo HTTP/1.1\r\nHost: t\r\nContent-Length: 8\r\n\r\nabc"); err != nil {
		t.Fatalf("写请求头失败: %v", err)
	}

	start := time.Now()
	select {
	case err := <-readErr:
		if err == nil {
			t.Fatal("慢速 body 应在期限内读到超时错误，实际读成功")
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("断开过慢: %v", elapsed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("慢速 body 未被期限切断（3s 内 handler 仍阻塞在读）")
	}
}

// 无 body 的请求（GET）不应设期限——SSE/终端 WebSocket 升级请求依赖长连接。
func TestBodyDeadlineSkipsBodylessRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.GET("/api/sse", bodyDeadlineWith(50*time.Millisecond, time.Minute), func(c *gin.Context) {
		// 无 body 请求不应触发读期限：睡过 def 期限，连接仍应活着写出响应
		time.Sleep(150 * time.Millisecond)
		c.Status(200)
	})

	srv := httptest.NewServer(e)
	defer srv.Close()
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(srv.URL + "/api/sse")
	if err != nil {
		t.Fatalf("GET 请求不应受 body 期限影响: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("状态码 %d != 200", resp.StatusCode)
	}
}

// 上传前缀路由用更长的期限档位。
func TestBodyDeadlineUploadPrefixUsesLongDeadline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	served := make(chan time.Duration, 1)
	e.POST("/api/server-compose/deploy", bodyDeadlineWith(100*time.Millisecond, time.Minute), func(c *gin.Context) {
		start := time.Now()
		_, err := io.ReadAll(c.Request.Body)
		served <- time.Since(start)
		if err != nil {
			t.Errorf("上传前缀路由不应被短期限切断: %v", err)
		}
		c.Status(200)
	})

	srv := httptest.NewServer(e)
	defer srv.Close()

	// 总耗时超过 def（100ms）的慢速但完整的 body——上传档位（1min）内应读完
	body := strings.Repeat("x", 10)
	go func() {
		conn, err := net.DialTimeout("tcp", strings.TrimPrefix(srv.URL, "http://"), 2*time.Second)
		if err != nil {
			return
		}
		defer conn.Close()
		io.WriteString(conn, "POST /api/server-compose/deploy HTTP/1.1\r\nHost: t\r\nContent-Length: 10\r\n\r\n")
		for i := 0; i < len(body); i++ {
			io.WriteString(conn, body[i:i+1])
			time.Sleep(40 * time.Millisecond) // 总 ~400ms > def 100ms
		}
		time.Sleep(200 * time.Millisecond) // 等响应
	}()

	select {
	case took := <-served:
		if took < 350*time.Millisecond {
			t.Logf("上传 body 读取完成，耗时 %v（长档位生效）", took)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("上传前缀路由在长档位期限内未完成读取")
	}
}
