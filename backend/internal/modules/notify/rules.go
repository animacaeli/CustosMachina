package notify

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/custos-machina/backend/internal/pkg/logger"
)

// 统一通知路由（P5 M1）：所有模块的事件经 NotifyEvent 汇入，
// 按规则（source + 最低级别）匹配后投递；支持静默时段与同类聚合窗口。
// 防膨胀边界（roadmap §1）：不做升级链/值班表/多渠道编排/去重持久化；
// 聚合缓冲为内存态（单实例假设，roadmap 原则 1）。

const aggSweepInterval = 30 * time.Second

// ---- 规则 CRUD ----

type SaveRuleInput struct {
	Name         string `json:"name" binding:"required,max=64"`
	Source       string `json:"source" binding:"required"`
	MinLevel     string `json:"minLevel" binding:"required,oneof=info warn critical"`
	GroupID      uint   `json:"groupId" binding:"required"`
	SilentStart  string `json:"silentStart" binding:"omitempty,max=5"`
	SilentEnd    string `json:"silentEnd" binding:"omitempty,max=5"`
	AggregateSec int    `json:"aggregateSec" binding:"min=0,max=3600"`
	Enabled      bool   `json:"enabled"`
}

func validSource(s string) bool {
	for _, v := range ValidSources {
		if v == s {
			return true
		}
	}
	return false
}

// validClock HH:MM 语法校验。
func validClock(s string) bool {
	if s == "" {
		return true
	}
	var h, m int
	if _, err := fmt.Sscanf(s, "%02d:%02d", &h, &m); err != nil {
		// 允许非补零写法 9:05
		if _, err2 := fmt.Sscanf(s, "%d:%d", &h, &m); err2 != nil {
			return false
		}
	}
	return h >= 0 && h <= 23 && m >= 0 && m <= 59
}

func (s *Service) validateRule(in SaveRuleInput) error {
	if !validSource(in.Source) {
		return fmt.Errorf("未知事件源 %q", in.Source)
	}
	if _, err := s.Get(context.Background(), in.GroupID); err != nil {
		return fmt.Errorf("目标通知群不存在（id=%d）", in.GroupID)
	}
	if (in.SilentStart == "") != (in.SilentEnd == "") {
		return fmt.Errorf("静默起止时间须成对配置")
	}
	if !validClock(in.SilentStart) || !validClock(in.SilentEnd) {
		return fmt.Errorf("静默时段格式须为 HH:MM")
	}
	return nil
}

func (s *Service) CreateRule(ctx context.Context, in SaveRuleInput) (*Rule, error) {
	if err := s.validateRule(in); err != nil {
		return nil, err
	}
	r := Rule{Name: in.Name, Source: in.Source, MinLevel: in.MinLevel, GroupID: in.GroupID,
		SilentStart: in.SilentStart, SilentEnd: in.SilentEnd, AggregateSec: in.AggregateSec, Enabled: in.Enabled}
	if err := s.db.WithContext(ctx).Create(&r).Error; err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Service) UpdateRule(ctx context.Context, id uint, in SaveRuleInput) (*Rule, error) {
	var r Rule
	if err := s.db.WithContext(ctx).First(&r, id).Error; err != nil {
		return nil, ErrNotFound
	}
	if err := s.validateRule(in); err != nil {
		return nil, err
	}
	r.Name, r.Source, r.MinLevel, r.GroupID = in.Name, in.Source, in.MinLevel, in.GroupID
	r.SilentStart, r.SilentEnd, r.AggregateSec, r.Enabled = in.SilentStart, in.SilentEnd, in.AggregateSec, in.Enabled
	if err := s.db.WithContext(ctx).Save(&r).Error; err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Service) DeleteRule(ctx context.Context, id uint) error {
	res := s.db.WithContext(ctx).Delete(&Rule{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) ListRules(ctx context.Context) ([]Rule, error) {
	var rs []Rule
	if err := s.db.WithContext(ctx).Order("id").Find(&rs).Error; err != nil {
		return nil, err
	}
	return rs, nil
}

// ---- 事件路由 ----

// NotifyEvent 统一事件入口：匹配规则 → 静默判断 → 聚合/投递。
// 无任何匹配规则时回退运维群（等价 M1 前的直推语义，保底知情权）。
func (s *Service) NotifyEvent(ctx context.Context, source, level, dedupKey, title, detail string) {
	rank, ok := levelRank[level]
	if !ok {
		rank = levelRank[LevelWarn]
		level = LevelWarn
	}
	s.flushDue()

	var rules []Rule
	if err := s.db.WithContext(ctx).Where("source = ? AND enabled = ?", source, true).Find(&rules).Error; err != nil {
		logger.Warnf("[notify] 查询路由规则失败: %v", err)
	}
	matched := false
	for _, r := range rules {
		if rank < levelRank[r.MinLevel] {
			continue
		}
		matched = true
		if silenced(r, level, time.Now()) {
			continue
		}
		s.deliver(ctx, r, dedupKey, title, detail)
	}
	if !matched {
		// 无规则兜底：推运维群（旧 NotifyOps 语义）
		s.NotifyOps(ctx, title, detail)
	}
}

// silenced 静默时段判断：critical 永不静默（知情权优先级最高的级别穿透）。
func silenced(r Rule, level string, now time.Time) bool {
	if level == LevelCritical || r.SilentStart == "" || r.SilentEnd == "" {
		return false
	}
	cur := now.Hour()*60 + now.Minute()
	st := clockMinutes(r.SilentStart)
	en := clockMinutes(r.SilentEnd)
	if st == en {
		return false
	}
	if st < en { // 常规区间 22:00→08:00 之外的正向区间
		return cur >= st && cur < en
	}
	// 跨零点区间（如 22:00→06:00）
	return cur >= st || cur < en
}

func clockMinutes(hhmm string) int {
	var h, m int
	_, _ = fmt.Sscanf(hhmm, "%d:%d", &h, &m)
	return h*60 + m
}

// ---- 聚合缓冲 ----

type aggItem struct {
	rule     Rule
	group    Group
	dedupKey string
	title    string
	first    string
	last     string
	count    int
	deadline time.Time
}

type aggregator struct {
	mu    sync.Mutex
	items map[string]*aggItem // key: ruleID|source|dedupKey
}

func newAggregator() *aggregator { return &aggregator{items: map[string]*aggItem{}} }

// deliver 按规则投递：无聚合窗口立即发；有则并入缓冲等窗口到期合并发一条。
func (s *Service) deliver(ctx context.Context, r Rule, dedupKey, title, detail string) {
	if r.AggregateSec <= 0 {
		g, err := s.Get(ctx, r.GroupID)
		if err != nil {
			logger.Warnf("[notify] 规则 %q 目标群不存在(id=%d): %v", r.Name, r.GroupID, err)
			return
		}
		if err := s.Send(ctx, g, title, detail); err != nil {
			logger.Warnf("[notify] 投递失败 rule=%q: %v", r.Name, err)
		}
		return
	}
	key := fmt.Sprintf("%d|%s|%s", r.ID, r.Source, dedupKey)
	s.agg.mu.Lock()
	defer s.agg.mu.Unlock()
	if it, ok := s.agg.items[key]; ok {
		it.count++
		it.last = detail
		return
	}
	s.agg.items[key] = &aggItem{
		rule: r, dedupKey: dedupKey, title: title,
		first: detail, last: detail, count: 1,
		deadline: time.Now().Add(time.Duration(r.AggregateSec) * time.Second),
	}
}

// flushDue 把到期的聚合项合并投递（NotifyEvent 顺带 + 30s 扫描兜底）。
func (s *Service) flushDue() {
	now := time.Now()
	s.agg.mu.Lock()
	due := map[string]*aggItem{}
	for k, it := range s.agg.items {
		if now.After(it.deadline) {
			due[k] = it
			delete(s.agg.items, k)
		}
	}
	s.agg.mu.Unlock()
	if len(due) == 0 {
		return
	}
	// 稳定顺序（测试可预期）
	keys := make([]string, 0, len(due))
	for k := range due {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ctx := context.Background()
	for _, k := range keys {
		it := due[k]
		g, err := s.Get(ctx, it.rule.GroupID)
		if err != nil {
			logger.Warnf("[notify] 聚合投递目标群不存在(id=%d)", it.rule.GroupID)
			continue
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s\n\n聚合 %d 条同类事件（窗口 %d 秒），首条：\n%s", it.title, it.count, it.rule.AggregateSec, it.first)
		if it.count > 1 {
			fmt.Fprintf(&b, "\n\n最后一条：\n%s", it.last)
		}
		if err := s.Send(ctx, g, it.title, b.String()); err != nil {
			logger.Warnf("[notify] 聚合投递失败 rule=%q: %v", it.rule.Name, err)
		}
	}
}

// startAggSweeper 聚合到期兜底扫描（30s；即使之后长时间无事件也能到期投出）。
func (s *Service) startAggSweeper() {
	go func() {
		t := time.NewTicker(aggSweepInterval)
		defer t.Stop()
		for range t.C {
			s.flushDue()
		}
	}()
}

// getRule 按 id 取规则（handler 测试事件用）。
func (s *Service) getRule(ctx context.Context, id uint) (*Rule, error) {
	var r Rule
	if err := s.db.WithContext(ctx).First(&r, id).Error; err != nil {
		return nil, ErrNotFound
	}
	return &r, nil
}
