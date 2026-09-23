package canary

import (
	"fmt"
	"strings"
)

// renderNginx 渲染 compose 期的 nginx 分流配置（CanaryRenderer 的 compose 实现）。
//
// 约定（需与项目部署拓扑匹配，实测点）：
//   - 正式实例：compose 项目 <proj>-prod 的服务容器（upstream 名 <proj>-prod）
//   - 灰度实例：compose 项目 <proj>-canary 的服务容器（upstream 名 <proj>-canary）
//   - 命中任一请求头策略 → <proj>-canary；未命中 → split_clients 按 traffic 比例
//   - 多条 traffic 策略按比例加权（10 与 20 → 命中流量池的请求 1:2 分流）
//
// 配置写入目标机 /opt/custos-machina/canary/<proj>.conf（server 块内 include 用）。
func renderNginx(proj string, policies []Policy) (string, error) {
	var headers, traffic []Policy
	for _, p := range policies {
		switch p.Type {
		case TypeHeader:
			headers = append(headers, p)
		case TypeTraffic:
			traffic = append(traffic, p)
		}
	}
	if len(headers) == 0 && len(traffic) == 0 {
		return "", fmt.Errorf("没有启用的策略")
	}

	var b strings.Builder
	b.WriteString("# 由 CustosMachina 生成，请勿手工编辑（聚合发布会整体覆盖）\n")

	// 请求头映射：任一命中 → canary
	if len(headers) > 0 {
		fmt.Fprintf(&b, "map $http_x_canary_mux %s_canary_hit {\n", proj)
		b.WriteString("\tdefault 0;\n")
		for _, h := range headers {
			fmt.Fprintf(&b, "\t%q 1;\n", h.HeaderValue)
		}
		b.WriteString("}\n")
	}

	// 流量比例：部署模型只有一个 <proj>-canary 实例，多条 traffic 策略按总和切灰度
	if len(traffic) > 0 {
		fmt.Fprintf(&b, "split_clients \"${remote_addr}${http_user_agent}\" %s_traffic_split {\n", proj)
		fmt.Fprintf(&b, "\t%.2f%% canary;\n", float64(sumPercents(traffic)))
		b.WriteString("\t* stable;\n}\n")
	}
	return b.String(), nil
}

func sumPercents(ps []Policy) int {
	s := 0
	for _, p := range ps {
		s += p.TrafficPercent
	}
	return s
}
