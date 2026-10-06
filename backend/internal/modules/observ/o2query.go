// o2query.go P7-M2：O2 查询导出（告警 AI 分析工具的数据源）。
// 复用 alerts.go 的 O2Config/o2Request（Basic 认证）；端点为真机验证过的
// O2 原生 _search 与 Prometheus 兼容 query_range（remote write 同族路径）。
// 未配置 O2 时返回明确降级文案——模型据此走平台数据分析，不因外部挂了不分析。
package observ

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// O2SearchLogs 全文/SQL 日志查询（AI 工具 search_o2_logs 数据源）。
// query 为 SELECT ... WHERE 片段（空 = 全量扫描最近窗口）；返回紧凑文本。
func (s *Service) O2SearchLogs(ctx context.Context, query, stream string, minutes, limit int) (string, error) {
	cfg, ok := s.o2Config(ctx)
	if !ok || cfg.BaseURL == "" {
		return "", fmt.Errorf("O2 未配置（管理后台 → 观测组件）")
	}
	if minutes <= 0 || minutes > 24*60 {
		minutes = 60
	}
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	sql := "SELECT * FROM \"" + cfg.Org + "\""
	if stream != "" {
		sql += ".\"" + stream + "\""
	}
	if query != "" {
		sql += " WHERE " + query
	}
	sql += " ORDER BY _timestamp DESC"
	now := time.Now().UnixMicro()
	body := map[string]any{
		"query": map[string]any{
			"sql":        sql,
			"start_time": now - int64(minutes)*60*1_000_000,
			"end_time":   now,
			"from":       0,
			"size":       limit,
		},
	}
	out, err := o2Request(ctx, cfg, "POST", "/api/"+cfg.Org+"/_search", body)
	if err != nil {
		return "", err
	}
	return renderO2Hits(out, limit)
}

// O2QueryMetrics PromQL 范围查询（AI 工具 query_o2_metrics 数据源）。
func (s *Service) O2QueryMetrics(ctx context.Context, promql string, minutes int) (string, error) {
	cfg, ok := s.o2Config(ctx)
	if !ok || cfg.BaseURL == "" {
		return "", fmt.Errorf("O2 未配置（管理后台 → 观测组件）")
	}
	if promql == "" {
		return "", fmt.Errorf("promql 必填")
	}
	if minutes <= 0 || minutes > 24*60 {
		minutes = 30
	}
	now := time.Now()
	step := pickStep(minutes)
	q := url.Values{}
	q.Set("query", promql)
	q.Set("start", strconv.FormatInt(now.Add(-time.Duration(minutes)*time.Minute).Unix(), 10))
	q.Set("end", strconv.FormatInt(now.Unix(), 10))
	q.Set("step", step)
	out, err := o2Request(ctx, cfg, "GET", "/api/"+cfg.Org+"/prometheus/api/v1/query_range?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	return renderPromResult(out)
}

// pickStep 按窗口选采样步长（点数控制在 ~60 内）。
func pickStep(minutes int) string {
	switch {
	case minutes <= 10:
		return "10s"
	case minutes <= 60:
		return "60s"
	case minutes <= 360:
		return "6m"
	default:
		return "30m"
	}
}

// renderO2Hits 把 _search 响应压成紧凑文本（时间 | level | 消息节选）。
func renderO2Hits(raw []byte, limit int) (string, error) {
	var resp struct {
		Hits int64 `json:"total"`
		Hit  []struct {
			Fields map[string]any `json:"fields"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("解析 _search 响应失败: %w", err)
	}
	if len(resp.Hit) == 0 {
		return "（时间窗口内无匹配日志）", nil
	}
	out := ""
	for i, h := range resp.Hit {
		f := flattenFields(h.Fields)
		ts := fmt.Sprintf("%v", f["_timestamp"])
		if v, ok := f["timestamp"].(string); ok && ts == "" {
			ts = v
		}
		msg := fmt.Sprintf("%v", f["message"])
		if len(msg) > 200 {
			msg = msg[:200] + "…"
		}
		out += fmt.Sprintf("%s | %v | %s\n", truncStr(ts, 24), f["level"], msg)
		if i >= limit-1 {
			break
		}
	}
	return fmt.Sprintf("命中 %d 条（展示前 %d 条）：\n%s", resp.Hits, min(len(resp.Hit), limit), out), nil
}

// renderPromResult 把 query_range 响应压成紧凑文本（每序列末值 + min/max）。
func renderPromResult(raw []byte) (string, error) {
	var resp struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
				Values [][2]any          `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("解析 query_range 响应失败: %w", err)
	}
	if resp.Status != "success" {
		return "", fmt.Errorf("O2 promql 查询失败: %.200s", raw)
	}
	if len(resp.Data.Result) == 0 {
		return "（该 PromQL 无数据点）", nil
	}
	out := ""
	for _, r := range resp.Data.Result {
		name := r.Metric["__name__"]
		for k, v := range r.Metric {
			if k == "__name__" {
				continue
			}
			name += "," + k + "=" + v
		}
		if len(r.Values) == 0 {
			out += name + ": 无点\n"
			continue
		}
		last := r.Values[len(r.Values)-1][1]
		out += fmt.Sprintf("%s: 最新=%v（%d 个点）\n", name, last, len(r.Values))
	}
	return out, nil
}

// flattenFields O2 fields 可能是数组壳（{"level":["info"]}）——取首元素。
func flattenFields(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if arr, ok := v.([]any); ok && len(arr) > 0 {
			v = arr[0]
		}
		out[k] = v
	}
	return out
}

func truncStr(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
