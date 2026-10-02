package crypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"

	"golang.org/x/crypto/pbkdf2"
)

// 备份口令加密（P5 M2）：与 `openssl enc -aes-256-cbc -pbkdf2` 输出格式
// 互相兼容（"Salted__" 头 + 8 字节盐 + PKCS7 填充的 AES-256-CBC 密文），
// 使恢复侧空机用 openssl 即可解开密钥份额，无需平台二进制。
// KDF 参数与 openssl 默认对齐：PBKDF2-HMAC-SHA256，10000 轮，key 32 + iv 16。

const (
	pbkdf2Iters  = 10000
	saltHeader   = "Salted__"
	saltLen      = 8
	keyLenAES256 = 32
	ivLenCBC     = 16
)

// PassphraseEncrypt 用口令加密任意字节（输出 openssl enc -pbkdf2 兼容格式）。
func PassphraseEncrypt(plaintext []byte, passphrase string) ([]byte, error) {
	if passphrase == "" {
		return nil, errors.New("口令为空")
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	key, iv := pbkdf2KeyIV([]byte(passphrase), salt)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	padded := pkcs7Pad(plaintext, aes.BlockSize)
	out := bytes.NewBuffer(nil)
	out.WriteString(saltHeader)
	out.Write(salt)
	enc := cipher.NewCBCEncrypter(block, iv)
	buf := make([]byte, len(padded))
	enc.CryptBlocks(buf, padded)
	out.Write(buf)
	return out.Bytes(), nil
}

// PassphraseDecrypt 解开 PassphraseEncrypt 的产物（错误口令返回错误）。
func PassphraseDecrypt(data []byte, passphrase string) ([]byte, error) {
	if len(data) < len(saltHeader)+saltLen+aes.BlockSize {
		return nil, errors.New("密文长度不足")
	}
	if string(data[:len(saltHeader)]) != saltHeader {
		return nil, errors.New("非 Salted__ 格式密文")
	}
	salt := data[len(saltHeader) : len(saltHeader)+saltLen]
	body := data[len(saltHeader)+saltLen:]
	if len(body)%aes.BlockSize != 0 {
		return nil, errors.New("密文长度非块对齐")
	}
	key, iv := pbkdf2KeyIV([]byte(passphrase), salt)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, len(body))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(buf, body)
	return pkcs7Unpad(buf)
}

// pbkdf2KeyIV openssl enc -pbkdf2 派生：单次 PBKDF2 输出 key+iv 连续拼接。
func pbkdf2KeyIV(pass, salt []byte) (key, iv []byte) {
	dk := pbkdf2.Key(pass, salt, pbkdf2Iters, keyLenAES256+ivLenCBC, sha256.New)
	return dk[:keyLenAES256], dk[keyLenAES256:]
}

func pkcs7Pad(b []byte, blockSize int) []byte {
	n := blockSize - len(b)%blockSize
	pad := bytes.Repeat([]byte{byte(n)}, n)
	return append(b, pad...)
}

func pkcs7Unpad(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, errors.New("空明文")
	}
	n := int(b[len(b)-1])
	if n == 0 || n > len(b) || n > aes.BlockSize {
		return nil, fmt.Errorf("PKCS7 填充非法（口令错误或密文损坏）")
	}
	for _, v := range b[len(b)-n:] {
		if int(v) != n {
			return nil, fmt.Errorf("PKCS7 填充非法（口令错误或密文损坏）")
		}
	}
	return b[:len(b)-n], nil
}
