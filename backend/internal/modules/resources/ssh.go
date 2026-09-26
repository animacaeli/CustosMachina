package resources

import (
	"encoding/base64"
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

// hostKeyStore 主机公钥的持久化存取（Service 初始化时注入；模块内 wire 单例，
// 避免给 17 处调用点改签名传依赖）。
type hostKeyStore interface {
	// pinned 返回已记录的公钥（base64），空串 = 未记录
	pinned(serverID uint) (string, error)
	pin(serverID uint, keyB64 string) error
}

var hostKeys hostKeyStore

// hostKeyCallback TOFU（首次信任并记录）+ 之后强校验：不一致即拒绝连接——
// SSH 是凭据与部署命令的双通道，MITM 面必须关闭。serverID==0 为保存前
// 的即席连通测试（无落库行），跳过校验。
func hostKeyCallback(serverID uint) ssh.HostKeyCallback {
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		if serverID == 0 || hostKeys == nil {
			return nil
		}
		got := base64.StdEncoding.EncodeToString(key.Marshal())
		want, err := hostKeys.pinned(serverID)
		if err != nil {
			return fmt.Errorf("读取主机公钥记录失败: %w", err)
		}
		if want == "" {
			return hostKeys.pin(serverID, got)
		}
		if want != got {
			return fmt.Errorf("主机公钥与首次记录不一致（可能被劫持或主机重装）；确认变更请联系管理员清空该主机的密钥记录")
		}
		return nil
	}
}

// DialSSH 用给定凭据建立 SSH 连接（不含凭据解密，调用方负责）。
// 主机公钥按 TOFU 记录并强校验（见 hostKeyCallback）。
// 调用方用完必须 Close。
func DialSSH(host string, port int, cred *credential, serverID uint) (*ssh.Client, error) {
	auth, err := sshAuthMethod(cred)
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User:            cred.Username,
		Auth:            []ssh.AuthMethod{auth},
		HostKeyCallback: hostKeyCallback(serverID),
		Timeout:         sshDialTimeout,
	}
	return ssh.Dial("tcp", net.JoinHostPort(host, fmt.Sprintf("%d", port)), cfg)
}

// TestConnectivity 建连并跑一次 echo，验证认证与会话通道都可用。
func TestConnectivity(host string, port int, cred *credential) error {
	client, err := DialSSH(host, port, cred, 0)
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
