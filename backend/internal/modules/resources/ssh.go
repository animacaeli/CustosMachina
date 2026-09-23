package resources

import (
	"fmt"
	"net"
	"time"

	"golang.org/x/crypto/ssh"
)

const sshDialTimeout = 5 * time.Second

// sshAuthMethod 按凭据类型构造认证方式。
func sshAuthMethod(cred *credential) (ssh.AuthMethod, error) {
	switch {
	case cred.Password != "":
		return ssh.Password(cred.Password), nil
	case cred.PrivateKey != "":
		var signer ssh.Signer
		var err error
		if cred.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(cred.PrivateKey), []byte(cred.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(cred.PrivateKey))
		}
		if err != nil {
			return nil, fmt.Errorf("私钥解析失败: %w", err)
		}
		return ssh.PublicKeys(signer), nil
	default:
		return nil, fmt.Errorf("凭据中无密码也无私钥")
	}
}

// DialSSH 用给定凭据建立 SSH 连接（不含凭据解密，调用方负责）。
// 调用方用完必须 Close。
func DialSSH(host string, port int, cred *credential) (*ssh.Client, error) {
	auth, err := sshAuthMethod(cred)
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User:            cred.Username,
		Auth:            []ssh.AuthMethod{auth},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // 资源看护场景：目标机为用户自管资产，暂不落 known_hosts（硬化项）
		Timeout:         sshDialTimeout,
	}
	return ssh.Dial("tcp", net.JoinHostPort(host, fmt.Sprintf("%d", port)), cfg)
}

// TestConnectivity 建连并跑一次 echo，验证认证与会话通道都可用。
func TestConnectivity(host string, port int, cred *credential) error {
	client, err := DialSSH(host, port, cred)
	if err != nil {
		return err
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("打开会话失败: %w", err)
	}
	defer session.Close()
	out, err := session.Output("echo custos-ok")
	if err != nil {
		return fmt.Errorf("执行测试命令失败: %w", err)
	}
	if string(out) != "custos-ok\n" && string(out) != "custos-ok" {
		return fmt.Errorf("测试命令输出异常: %q", string(out))
	}
	return nil
}

// TryConnectivity 同 TestConnectivity，但返回详细信息（连接耗时）供表单即时反馈。
func TryConnectivity(host string, port int, cred *credential) (time.Duration, error) {
	start := time.Now()
	err := TestConnectivity(host, port, cred)
	return time.Since(start).Round(time.Millisecond), err
}
