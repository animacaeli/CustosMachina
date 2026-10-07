package notify

import (
	"testing"
	"time"

	crypto "github.com/custos-machina/backend/internal/pkg/crypto"
)

// T7 回归 + 独立审核 N4 停机语义：Stop 后拒新入队；Stop 等待在途事件
// 排空（worker for-range 排干队列后退出）；重复 Stop 幂等。
func TestEnqueueBusinessDrainOnStop(t *testing.T) {
	db := testDB(t)
	cipher, err := crypto.NewCipher("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(db, cipher)

	// 正常受理（无群可投时 NotifyEvent 只记失败留痕，不阻塞）
	if !s.EnqueueBusiness("warn", "k1", "t1", "d1") {
		t.Fatal("运行期入队应受理")
	}
	// Stop：拒绝新入队 + 排空在途 + worker 退出
	s.Stop()
	if s.EnqueueBusiness("warn", "k2", "t2", "d2") {
		t.Fatal("停机后入队应被拒绝（429 语义）")
	}
	// 幂等：二次 Stop 不 panic（close 已关通道）
	s.Stop()
}

// Stop 在无在途任务时应快速返回（不挂 drain 超时）。
func TestStopFastWhenIdle(t *testing.T) {
	db := testDB(t)
	cipher, err := crypto.NewCipher("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(db, cipher)
	start := time.Now()
	s.Stop()
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("空队列 Stop 应快速返回，耗时 %v", elapsed)
	}
}
