package auth

import (
	"github.com/google/wire"

	"github.com/custos-machina/backend/internal/config"
	cryptopkg "github.com/custos-machina/backend/internal/pkg/crypto"
)

// ProvideCipher 主密钥未配置时返回 nil（本机开发不强制），
// 保存/解密企微凭证时会得到明确报错。
func ProvideCipher(cfg *config.Config) *cryptopkg.Cipher {
	c, err := cryptopkg.NewCipher(cfg.Secrets.MasterKey)
	if err != nil {
		return nil
	}
	return c
}

var Set = wire.NewSet(
	NewAuthService,
	NewHandler,
	ProvideAuthMiddleware,
	ProvideCipher,
)
