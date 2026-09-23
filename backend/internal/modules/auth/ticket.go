package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	jwtpkg "github.com/custos-machina/backend/internal/pkg/jwt"
)

var errTooManyTickets = errors.New("ticket 数量超上限，稍后重试")

// 一次性短时 ticket（M3/M4 硬化项）：浏览器 WebSocket / EventSource 无法带
// Authorization 头，直传 JWT 会把长期 token 暴露给访问日志。改为先领一张
// 120 秒、单次使用的 ticket，用完即焚；泄漏窗口与重放面都被压到最小。
const (
	ticketTTL     = 120 * time.Second
	ticketSweep   = 5 * time.Minute
	maxLiveTicket = 10_000
)

type ticketEntry struct {
	claims  *jwtpkg.Claims
	expires time.Time
}

type ticketStore struct {
	mu sync.Mutex
	m  map[string]ticketEntry
}

func newTicketStore() *ticketStore {
	ts := &ticketStore{m: map[string]ticketEntry{}}
	go ts.sweepLoop()
	return ts
}

func (ts *ticketStore) sweepLoop() {
	for now := range time.Tick(ticketSweep) {
		ts.mu.Lock()
		for k, v := range ts.m {
			if now.After(v.expires) {
				delete(ts.m, k)
			}
		}
		ts.mu.Unlock()
	}
}

// Issue 签发一次性 ticket（已认证用户调用）。
func (ts *ticketStore) Issue(claims *jwtpkg.Claims) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	t := hex.EncodeToString(buf)
	ts.mu.Lock()
	if len(ts.m) > maxLiveTicket { // 防御性上限
		ts.mu.Unlock()
		return "", errTooManyTickets
	}
	ts.m[t] = ticketEntry{claims: claims, expires: time.Now().Add(ticketTTL)}
	ts.mu.Unlock()
	return t, nil
}

// Consume 校验并消费（一次性）：不存在/过期返回 nil。
func (ts *ticketStore) Consume(t string) *jwtpkg.Claims {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	e, ok := ts.m[t]
	if !ok || time.Now().After(e.expires) {
		if ok {
			delete(ts.m, t)
		}
		return nil
	}
	delete(ts.m, t)
	return e.claims
}
