package configs

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"time"
)

func fastSum(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}

// postRefresh 调应用的 refresh 端点（Spring @RefreshScope / 自定义热载端点）。
func postRefresh(ctx context.Context, url string) error {
	c, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(c, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("refresh 端点调用失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("refresh 端点返回 %d", resp.StatusCode)
	}
	return nil
}
