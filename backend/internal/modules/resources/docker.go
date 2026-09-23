package resources

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	dc "github.com/moby/moby/client"

	"golang.org/x/crypto/ssh"
)

// Docker API over SSH 隧道（M4，agentless）：每个请求/流建一条 SSH 连接，
// 在目标机上 unix dial /var/run/docker.sock。操作由人触发，频率低，不复用连接
// 换取实现简单（计划文档性能一节的取舍）。

const dockerSockPath = "/var/run/docker.sock"

// dockerClientFor 为一台服务器建立 Docker API 客户端；返回的 ssh 连接由调用方关闭。
// 注意：WithHost 会经 sockets.ConfigureTransport 重写 transport 的 DialContext，
// 因此自定义拨号必须在 WithHost 之后用 WithDialContext 重新注入（否则会对
// docker.local 做真实 DNS 解析，表现为容器接口超时/报 no such host）。
func dockerClientFor(srv *Server, cred *credential) (*dc.Client, *ssh.Client, error) {
	sshClient, err := DialSSH(srv.Host, srv.Port, cred)
	if err != nil {
		return nil, nil, fmt.Errorf("SSH 连接失败: %w", err)
	}
	dialUnixOverSSH := func(ctx context.Context, _, _ string) (net.Conn, error) {
		// 每条 HTTP 连接对应一条到远端 docker.sock 的 SSH channel
		conn, err := sshClient.DialContext(ctx, "unix", dockerSockPath)
		if err != nil {
			return nil, fmt.Errorf("连接受限（SSH 用户需在 docker 组）: %w", err)
		}
		return conn, nil
	}
	cli, err := dc.NewClientWithOpts(
		dc.WithHTTPClient(&http.Client{Transport: &http.Transport{}}),
		dc.WithHost("tcp://127.0.0.1:2375"), // 占位 host，实际走 SSH 隧道
		dc.WithScheme("http"),               // 走隧道的是明文 unix socket，别按端口猜 TLS
		dc.WithDialContext(dialUnixOverSSH),
	)
	if err != nil {
		sshClient.Close()
		return nil, nil, err
	}
	return cli, sshClient, nil
}

// sshRunOutput 在目标机执行命令并返回合并输出（compose 部署/环境探测用）。
func sshRunOutput(srv *Server, cred *credential, cmd string, timeout time.Duration) (string, error) {
	return sshRunOutputWithStdin(srv, cred, cmd, "", timeout)
}

// sshRunOutputWithStdin 执行命令并先把 stdin 内容写入（部署时上传 compose 文件）。
func sshRunOutputWithStdin(srv *Server, cred *credential, cmd, stdin string, timeout time.Duration) (string, error) {
	client, err := DialSSH(srv.Host, srv.Port, cred)
	if err != nil {
		return "", fmt.Errorf("SSH 连接失败: %w", err)
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	out := &outBuf{}
	session.Stdout = out
	session.Stderr = out
	if stdin != "" {
		inPipe, err := session.StdinPipe()
		if err != nil {
			return "", err
		}
		go func() {
			_, _ = inPipe.Write([]byte(stdin))
			inPipe.Close()
		}()
	}
	done := make(chan error, 1)
	if err := session.Start(cmd); err != nil {
		return "", err
	}
	go func() { done <- session.Wait() }()
	select {
	case err := <-done:
		return out.String(), err
	case <-time.After(timeout):
		_ = session.Signal(ssh.SIGKILL)
		return out.String(), fmt.Errorf("命令超时（%s）", timeout)
	}
}

type outBuf struct{ b []byte }

func (o *outBuf) Write(p []byte) (int, error) {
	o.b = append(o.b, p...)
	return len(p), nil
}

func (o *outBuf) String() string { return string(o.b) }
