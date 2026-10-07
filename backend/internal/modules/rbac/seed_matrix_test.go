package rbac

import (
	"strconv"
	"testing"

	"github.com/custos-machina/backend/internal/modules/identity"
)

// delta 表完备性（v0.12.17 独立复核 R1 根治配套）：引入版本表与
// defaultPolicies 逐条互为充要——新增默认策略漏标引入版本时本测试红，
// 防止 delta 补种漏补新资源点。
func TestPolicyVersionTableConsistency(t *testing.T) {
	if len(policyIntroducedAt) != len(defaultPolicies) {
		t.Fatalf("表 (%d) 与 defaultPolicies (%d) 条数不一致", len(policyIntroducedAt), len(defaultPolicies))
	}
	for _, p := range defaultPolicies {
		v, ok := policyIntroducedAt[policyKey{p[0], p[1], p[2]}]
		if !ok {
			t.Errorf("defaultPolicies 条目 %v 未标注引入版本——delta 补种将漏补", p)
			continue
		}
		if v < 2 || v > seedVersionNum() {
			t.Errorf("条目 %v 引入版本 %d 越界 [2, %s]", p, v, policySeedVersion)
		}
	}
	for k := range policyIntroducedAt {
		found := false
		for _, p := range defaultPolicies {
			pk := policyKey{p[0], p[1], p[2]}
			if pk == k {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("表条目 %v 已不在 defaultPolicies 中（已废弃形态应随移除剔除）", k)
		}
	}
}

// 全版本升级矩阵（v0.12.17 独立复核 R1 验收）：对 seed 2~26 每一档——
// ①该版本之后引入的默认条目升级后全部直达；②人为删除的既有条目
// （引入版本 ≤ 库版本）绝不复活；③迁移幂等（二次迁移策略集不变）。
// 库形态构造：全量种入后按引入版本裁剪出「vN 视野」的库。
func TestSeedUpgradeMatrix(t *testing.T) {
	curN := seedVersionNum()
	for n := 2; n < curN; n++ {
		t.Run("v"+strconv.Itoa(n), func(t *testing.T) {
			svc := newTestEnforcer(t)
			db, e := svc.db, svc.enforcer

			// 构造 vN 库：移除所有「引入版本 > n」的条目（vN 库不认识它们）
			for _, p := range defaultPolicies {
				if policyIntroducedAt[policyKey{p[0], p[1], p[2]}] > n {
					if _, err := e.RemovePolicy(p[0], p[1], p[2]); err != nil {
						t.Fatal(err)
					}
				}
			}
			// 人为删除：ops 一条引入版本 ≤ n 的既有条目（vN 库上管理员主动收权）
			var deleted [3]string
			for _, p := range defaultPolicies {
				if p[0] == "ops" && policyIntroducedAt[policyKey{p[0], p[1], p[2]}] <= n {
					deleted = [3]string{p[0], p[1], p[2]}
					ok, err := e.RemovePolicy(p[0], p[1], p[2])
					if err != nil || !ok {
						t.Fatalf("人为删除失败（ok=%v err=%v）——测试前提不成立", ok, err)
					}
					break
				}
			}
			// 置版本为 n，执行迁移
			if err := db.Model(&identity.PlatformSetting{}).
				Where("skey = ?", "rbac.seed_version").
				Update("value", strconv.Itoa(n)).Error; err != nil {
				t.Fatal(err)
			}
			changed, err := migrateSeedVersion(db, e)
			if err != nil {
				t.Fatalf("v%d 迁移失败: %v", n, err)
			}
			if !changed {
				t.Fatal("迁移应返回 true（版本需推进）")
			}
			// ① 新条目直达
			for _, p := range defaultPolicies {
				if policyIntroducedAt[policyKey{p[0], p[1], p[2]}] > n {
					if has, _ := e.HasPolicy(p[0], p[1], p[2]); !has {
						t.Errorf("v%d 升级后新条目 %v 应被补齐（引入版本 %d > %d）",
							n, p, policyIntroducedAt[policyKey{p[0], p[1], p[2]}], n)
					}
				}
			}
			// ② 人为删除不复活
			if deleted[0] != "" {
				if has, _ := e.HasPolicy(deleted[0], deleted[1], deleted[2]); has {
					t.Errorf("v%d 升级复活了人为删除的条目 %v（引入版本 %d ≤ %d）",
						n, deleted, policyIntroducedAt[deleted], n)
				}
			}
			// ③ 幂等：快照 → 二次迁移 → 对比
			snap, _ := e.GetPolicy()
			if _, err := migrateSeedVersion(db, e); err != nil {
				t.Fatalf("二次迁移失败: %v", err)
			}
			again, _ := e.GetPolicy()
			if len(snap) != len(again) {
				t.Errorf("v%d 二次迁移不幂等（%d → %d 条）", n, len(snap), len(again))
			}
		})
	}
}

// 非法/缺失 seed 值的明确策略（v0.12.17 独立复核 R1 验收）：只补 home
// 基础面（内置+自定义角色），绝不以「极旧部署」名义全量恢复——管理员
// 删除的条目保持删除。
func TestIllegalSeedOnlySeedsHome(t *testing.T) {
	svc := newTestEnforcer(t)
	db, e := svc.db, svc.enforcer
	// 人为删除一条既有默认权限
	ok, err := e.RemovePolicy("ops", "/certs", "GET|POST|PUT|DELETE")
	if err != nil || !ok {
		t.Fatalf("删除未生效（ok=%v err=%v）", ok, err)
	}
	// 删除全部 home 条目，模拟老库无 home 面
	for _, sub := range []string{"admin", "ops", "dev"} {
		for _, obj := range []string{"/home/summary", "/home/readiness"} {
			_, _ = e.RemovePolicy(sub, obj, "GET")
		}
	}
	// 版本置为不可解析值
	if err := db.Model(&identity.PlatformSetting{}).
		Where("skey = ?", "rbac.seed_version").
		Update("value", "bogus").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := migrateSeedVersion(db, e); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"admin", "ops", "dev"} {
		if has, _ := e.HasPolicy(sub, "/home/summary", "GET"); !has {
			t.Errorf("非法 seed 下 %s 仍应补得 home 基础面", sub)
		}
	}
	if has, _ := e.HasPolicy("ops", "/certs", "GET|POST|PUT|DELETE"); has {
		t.Error("非法 seed 不得成为全量恢复的口子——人为删除应保持")
	}
}
