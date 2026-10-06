// Package crypto 组件凭证与 IM 提供商凭证的 AES-256-GCM 加解密（FR6.6）。
// 主密钥来自环境变量 CUSTOS_SECRETS_MASTER_KEY（32 字节 hex），仅存于环境。
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

var ErrNoMasterKey = errors.New("未配置主密钥 CUSTOS_SECRETS_MASTER_KEY（32 字节 hex）")

type Cipher struct {
	aead cipher.AEAD
}

// NewCipher 用 hex 编码的 32 字节主密钥构造；密钥缺失时返回 ErrNoMasterKey。
func NewCipher(masterKeyHex string) (*Cipher, error) {
	if masterKeyHex == "" {
		return nil, ErrNoMasterKey
	}
	key, err := hex.DecodeString(masterKeyHex)
	if err != nil {
		return nil, fmt.Errorf("主密钥不是合法 hex: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("主密钥须为 32 字节（hex 后 %d 字符），实际 %d 字节", hex.EncodedLen(32), len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt 返回 nonce+ciphertext 的 hex 字符串。
// 可选 aad：绑定字段上下文（GCM additionalData）——不同字段的密文不可互换
// （v0.12.0 审计中等项：此前 AAD 为 nil，A 表密文贴到 B 字段即生效）。
func (c *Cipher) Encrypt(plaintext string, aad ...string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	var ad []byte
	if len(aad) > 0 {
		ad = []byte(aad[0])
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plaintext), ad)
	return hex.EncodeToString(sealed), nil
}

// Decrypt 解密 Encrypt 的产物。
// 传入 aad 时先按绑定字段解；失败回退无 AAD 旧格式（存量数据兼容升级）。
func (c *Cipher) Decrypt(encoded string, aad ...string) (string, error) {
	data, err := hex.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("密文不是合法 hex: %w", err)
	}
	ns := c.aead.NonceSize()
	if len(data) < ns {
		return "", errors.New("密文长度不合法")
	}
	var ad []byte
	if len(aad) > 0 {
		ad = []byte(aad[0])
		if plain, err := c.aead.Open(nil, data[:ns], data[ns:], ad); err == nil {
			return string(plain), nil
		}
	}
	plain, err := c.aead.Open(nil, data[:ns], data[ns:], nil)
	if err != nil {
		return "", fmt.Errorf("解密失败（主密钥不一致？）: %w", err)
	}
	return string(plain), nil
}
