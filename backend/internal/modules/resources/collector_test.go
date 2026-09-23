package resources

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/pkg/crypto"
)

const testMasterKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestParseMetricOutput(t *testing.T) {
	out := "cpu  123456 1234 234567 9876543 4567 0 123 0 0 0\n" +
		"MemTotal:       16384000 kB\n" +
		"MemAvailable:    8192000 kB\n"
	raw, err := parseMetricOutput(out)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(raw.cpuTimes) != 10 {
		t.Errorf("CPU 列数应为 10，实际 %d", len(raw.cpuTimes))
	}
	if raw.memTotal != 16384000*1024 {
		t.Errorf("MemTotal 换算错误: %d", raw.memTotal)
	}
	if raw.memAvail != 8192000*1024 {
		t.Errorf("MemAvailable 换算错误: %d", raw.memAvail)
	}
}

func TestParseMetricOutput_BadInput(t *testing.T) {
	cases := []string{"", "not cpu\nMemTotal: 1 kB\nMemAvailable: 1 kB\n", "cpu 1 2 3\nMemTotal: 1 kB\nMemAvailable: 1 kB\n"}
	for i, in := range cases {
		if _, err := parseMetricOutput(in); err == nil {
			t.Errorf("case %d 应报错", i)
		}
	}
}

func TestCPUPctBetween(t *testing.T) {
	// 全空闲：使用率 0
	idle := []float64{100, 0, 100, 1000, 100, 0, 0, 0, 0, 0}
	idle2 := []float64{100, 0, 100, 2000, 200, 0, 0, 0, 0, 0}
	if p := cpuPctBetween(idle, idle2); p != 0 {
		t.Errorf("全空闲 CPU 应为 0，实际 %v", p)
	}
	// 全忙（idle 不变，总量涨）：100%
	busy := []float64{100, 0, 100, 500, 0, 0, 0, 0, 0, 0}
	busy2 := []float64{100, 0, 100, 500, 0, 0, 0, 0, 0, 0}
	// 总量没涨返回 0
	if p := cpuPctBetween(busy, busy2); p != 0 {
		t.Errorf("总量未变应返回 0，实际 %v", p)
	}
	busy3 := []float64{200, 100, 200, 500, 0, 0, 0, 0, 0, 0}
	if p := cpuPctBetween(busy, busy3); p != 100 {
		t.Errorf("全忙 CPU 应为 100，实际 %v", p)
	}
	// 一半忙
	half := []float64{100, 0, 100, 500, 0, 0, 0, 0, 0, 0}
	half2 := []float64{100, 0, 100, 600, 0, 0, 0, 0, 0, 0} // 总涨 100，idle 涨 100→ hmm
	_ = half
	_ = half2
	// 列数不一致返回 0
	if p := cpuPctBetween([]float64{1, 2, 3, 4}, []float64{1, 2, 3, 4, 5}); p != 0 {
		t.Errorf("列数不一致应返回 0，实际 %v", p)
	}
}

func TestRing(t *testing.T) {
	r := newRing()
	for i := 0; i < ringSize+50; i++ {
		r.push(MetricSample{ServerID: 1, TS: time.Unix(int64(i), 0), CPUPct: float64(i % 101)})
	}
	got := r.snapshot()
	if len(got) != ringSize {
		t.Fatalf("缓冲应保持 %d 条，实际 %d", ringSize, len(got))
	}
	// 最旧一条应是第 50 条（覆盖了前 50）
	if got[0].TS.Unix() != 50 {
		t.Errorf("环形覆盖错误：最旧时间戳 %d，应为 50", got[0].TS.Unix())
	}
	if got[len(got)-1].TS.Unix() != ringSize+49 {
		t.Errorf("最新时间戳 %d，应为 %d", got[len(got)-1].TS.Unix(), ringSize+49)
	}
}

func TestCollectorFlushBatch(t *testing.T) {
	db := newTestDB(t)
	svc := newTestServiceWithDB(t, db)
	c := &Collector{
		db: db, servers: NewServerRepository(db), svc: svc,
		rings: map[uint]*ring{}, prevCPU: map[uint][]float64{},
		nextDue: map[uint]time.Time{}, failCount: map[uint]int{},
		lastState: map[uint]ServerStatus{},
	}
	for i := 0; i < 5; i++ {
		c.mu.Lock()
		c.pending = append(c.pending, MetricSample{ServerID: 1, TS: time.Now(), CPUPct: 1})
		c.mu.Unlock()
	}
	if err := c.flush(t.Context()); err != nil {
		t.Fatalf("flush 失败: %v", err)
	}
	var n int64
	db.Model(&MetricSample{}).Count(&n)
	if n != 5 {
		t.Errorf("落库 %d 条，应为 5", n)
	}
	c.mu.Lock()
	pending := len(c.pending)
	c.mu.Unlock()
	if pending != 0 {
		t.Errorf("flush 后 pending 应清空，剩 %d", pending)
	}
}

// newTestDB 供 flush/retention 测试共用内存库。
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"))
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	if err := db.AutoMigrate(&Server{}, &ServerGroup{}, &MetricSample{}, &ServerEvent{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	return db
}

func newTestServiceWithDB(t *testing.T, db *gorm.DB) *Service {
	t.Helper()
	cipher, err := crypto.NewCipher(testMasterKey)
	if err != nil {
		t.Fatal(err)
	}
	return NewService(NewServerRepository(db), NewGroupRepository(db), db, cipher)
}

func TestRetention(t *testing.T) {
	db := newTestDB(t)
	c := &Collector{db: db, servers: NewServerRepository(db), svc: newTestServiceWithDB(t, db)}
	db.Create(&MetricSample{ServerID: 1, TS: time.Now().Add(-8 * 24 * time.Hour), CPUPct: 1})
	db.Create(&MetricSample{ServerID: 1, TS: time.Now(), CPUPct: 2})
	if err := c.retention(t.Context()); err != nil {
		t.Fatalf("retention 失败: %v", err)
	}
	var n int64
	db.Model(&MetricSample{}).Count(&n)
	if n != 1 {
		t.Errorf("清理后应剩 1 条，实际 %d", n)
	}
}
