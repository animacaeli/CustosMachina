package crypto

import (
	"bytes"
	"os/exec"
	"testing"
)

func TestPassphraseRoundTrip(t *testing.T) {
	plain := []byte(`{"masterKey":"0123456789abcdef..."}`)
	enc, err := PassphraseEncrypt(plain, "口令-test-123")
	if err != nil {
		t.Fatal(err)
	}
	dec, err := PassphraseDecrypt(enc, "口令-test-123")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dec, plain) {
		t.Errorf("往返不一致: %q", dec)
	}
	if _, err := PassphraseDecrypt(enc, "wrong"); err == nil {
		t.Error("错误口令应解密失败")
	}
	// 密文应带 Salted__ 头（openssl 兼容格式）
	if !bytes.HasPrefix(enc, []byte("Salted__")) {
		t.Error("缺 Salted__ 头")
	}
}

// TestPassphraseOpenSSLInterop 与系统 openssl 互通：Go 加密 → openssl 解密；
// openssl 加密 → Go 解密。openssl 不存在时跳过（CI 环境各异）。
func TestPassphraseOpenSSLInterop(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("本机无 openssl")
	}
	pass := "interop-pass-验证"
	plain := []byte("custos-machina keyshare interop 测试")

	enc, err := PassphraseEncrypt(plain, pass)
	if err != nil {
		t.Fatal(err)
	}
	// Go 加密 → openssl 解密
	cmd := exec.Command("openssl", "enc", "-d", "-aes-256-cbc", "-pbkdf2",
		"-pass", "pass:"+pass)
	cmd.Stdin = bytes.NewReader(enc)
	got, err := cmd.Output()
	if err != nil {
		t.Fatalf("openssl 解密失败: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Errorf("openssl 解密结果不符: %q", got)
	}

	// openssl 加密 → Go 解密
	cmd2 := exec.Command("openssl", "enc", "-aes-256-cbc", "-pbkdf2",
		"-pass", "pass:"+pass)
	cmd2.Stdin = bytes.NewReader(plain)
	enc2, err := cmd2.Output()
	if err != nil {
		t.Fatalf("openssl 加密失败: %v", err)
	}
	dec, err := PassphraseDecrypt(enc2, pass)
	if err != nil {
		t.Fatalf("Go 解密 openssl 密文失败: %v", err)
	}
	if !bytes.Equal(dec, plain) {
		t.Errorf("Go 解密结果不符: %q", dec)
	}
}
