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

// AAD 字段绑定：不同 aad 的密文不可互换；旧无 AAD 密文回退兼容。
func TestCipherAADBinding(t *testing.T) {
	c, err := NewCipher("3d1e9a4b6f8c2d5e7a0b4c6d8e0f2a4b6c8d0e2f4a6b8c0d2e4f6a8b0c2d4e6f")
	if err != nil {
		t.Fatal(err)
	}
	encA, err := c.Encrypt("secret", "fieldA")
	if err != nil {
		t.Fatal(err)
	}
	encPlain, err := c.Encrypt("secret")
	if err != nil {
		t.Fatal(err)
	}
	// 同 aad 解密正常
	if v, err := c.Decrypt(encA, "fieldA"); err != nil || v != "secret" {
		t.Errorf("同 aad 应解密成功: %v %q", err, v)
	}
	// 换 aad 解密：回退无 AAD 也失败（AAD 密文无 AAD 解不开）→ 报错
	if _, err := c.Decrypt(encA, "fieldB"); err == nil {
		t.Error("不同 aad 应解密失败")
	}
	// 旧无 AAD 密文 + 指定 aad：回退路径成功
	if v, err := c.Decrypt(encPlain, "fieldA"); err != nil || v != "secret" {
		t.Errorf("旧无 AAD 密文应回退兼容: %v", err)
	}
	// AAD 密文不传 aad 解不开（防跨字段互换）
	if _, err := c.Decrypt(encA); err == nil {
		t.Error("AAD 密文不带 aad 应解密失败")
	}
}
