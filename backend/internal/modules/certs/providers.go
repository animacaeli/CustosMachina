package certs

import (
	"fmt"
	"os"

	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/providers/dns/alidns"
	"github.com/go-acme/lego/v4/providers/dns/cloudflare"
	"github.com/go-acme/lego/v4/providers/dns/dnspod"
	"github.com/go-acme/lego/v4/providers/dns/gandi"
	"github.com/go-acme/lego/v4/providers/dns/godaddy"
	"github.com/go-acme/lego/v4/providers/dns/huaweicloud"
	"github.com/go-acme/lego/v4/providers/dns/tencentcloud"
)

// buildDNSProvider 按名构造 lego DNS provider；凭证经环境变量注入
// （各家 provider 从环境读配置——临时 set 后构造，构造完成即恢复，避免污染进程环境）。
func buildDNSProvider(name string, creds map[string]string) (challenge.Provider, error) {
	restore := setEnv(creds)
	defer restore()
	switch name {
	case "alidns":
		return alidns.NewDNSProvider()
	case "cloudflare":
		return cloudflare.NewDNSProvider()
	case "dnspod":
		return dnspod.NewDNSProvider()
	case "huaweicloud":
		return huaweicloud.NewDNSProvider()
	case "tencentcloud": // 腾讯云 API 密钥（SecretId/SecretKey），管理 DNSPod 解析
		return tencentcloud.NewDNSProvider()
	case "gandi":
		return gandi.NewDNSProvider()
	case "godaddy":
		return godaddy.NewDNSProvider()
	default:
		return nil, fmt.Errorf("未知 DNS provider: %s", name)
	}
}

// setEnv 临时注入环境变量，返回恢复函数。
func setEnv(kv map[string]string) func() {
	saved := map[string]string{}
	for k, v := range kv {
		if old, ok := os.LookupEnv(k); ok {
			saved[k] = old
		}
		_ = os.Setenv(k, v)
	}
	return func() {
		for k := range kv {
			if old, ok := saved[k]; ok {
				_ = os.Setenv(k, old)
			} else {
				_ = os.Unsetenv(k)
			}
		}
	}
}
