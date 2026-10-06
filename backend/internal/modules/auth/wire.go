package auth

import (
	"github.com/google/wire"

	"github.com/custos-machina/backend/internal/config"
	cryptopkg "github.com/custos-machina/backend/internal/pkg/crypto"
	"github.com/custos-machina/backend/internal/pkg/logger"
)

// ProvideCipher 主密钥未配置时返回 nil（本机开发不强制），
// 保存/解密企微凭证时会得到明确报错。
// release 模式缺失主密钥打启动告警（不 fail-fast：不用加密功能的存量部署
// 升级不应被拦——但必须让运维知道 IM/凭据/告警模板等能力已静默降级）。
func ProvideCipher(cfg *config.Config) *cryptopkg.Cipher {
	c, err := cryptopkg.NewCipher(cfg.Secrets.MasterKey)
	if err != nil {
		if cfg.HTTP.Mode != "debug" {
			logger.Warnf("[auth] %v：凭证加密相关能力（IM 凭据/服务器凭据/告警模板变量）不可用", err)
		}
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
