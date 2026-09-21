package crypto

import (
	"encoding/hex"
	"strings"
	"testing"
)

func testKey(t *testing.T) string {
	t.Helper()
	// 固定 32 字节测试密钥（hex 64 字符）
	return hex.EncodeToString(make([]byte, 32))
}

func TestEncryptDecrypt(t *testing.T) {
	c, err := NewCipher(testKey(t))
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	enc, err := c.Encrypt(`{"corpid":"ww123","secret":"s3cret"}`)
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	if strings.Contains(enc, "s3cret") {
		t.Error("密文不应包含明文")
	}
	dec, err := c.Decrypt(enc)
	if err != nil {
		t.Fatalf("解密失败: %v", err)
	}
	if dec != `{"corpid":"ww123","secret":"s3cret"}` {
		t.Errorf("往返结果不符: %s", dec)
	}
}

func TestNewCipher_Errors(t *testing.T) {
	if _, err := NewCipher(""); err != ErrNoMasterKey {
		t.Errorf("空密钥应返回 ErrNoMasterKey，实际 %v", err)
	}
	if _, err := NewCipher("nothex"); err == nil {
		t.Error("非法 hex 应报错")
	}
	if _, err := NewCipher(hex.EncodeToString([]byte("short"))); err == nil {
		t.Error("长度不足 32 字节应报错")
	}
}

func TestDecrypt_WrongKey(t *testing.T) {
	c1, _ := NewCipher(testKey(t))
	enc, _ := c1.Encrypt("secret")
	// 不同密钥的 cipher
	other := hex.EncodeToString([]byte(strings.Repeat("a", 32)))
	c2, _ := NewCipher(other)
	if _, err := c2.Decrypt(enc); err == nil {
		t.Error("密钥不一致应解密失败")
	}
}
