package notify

import "testing"

// 渠道判定按 Host 而非子串（v0.12.0 审计中等项：查询参数伪造）。
func TestDetectProviderByHost(t *testing.T) {
	if detectProvider("https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=x") != provWecom {
		t.Error("企微主机应识别为 wecom")
	}
	if detectProvider("https://evil.com/?x=qyapi.weixin.qq.com") != provUnknown {
		t.Error("查询参数伪造的域名不应被识别为 wecom")
	}
	if detectProvider("https://oapi.dingtalk.com/robot/send?access_token=t") != provDingtalk {
		t.Error("钉钉主机应识别")
	}
	if detectProvider("https://open.feishu.cn/open-apis/bot/v2/hook/x") != provFeishu {
		t.Error("飞书主机应识别")
	}
	if detectProvider("https://evil.com/?q=oapi.dingtalk.com") != provUnknown {
		t.Error("伪造钉钉应识别为 unknown")
	}
}
