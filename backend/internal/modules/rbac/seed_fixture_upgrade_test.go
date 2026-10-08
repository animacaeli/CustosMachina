package rbac

import (
	"strconv"
	"testing"

	"github.com/custos-machina/backend/internal/modules/identity"
)

// 真实历史快照升级测试（v0.12.19 独立复核 R1 验收）：用 git 固化的各档
// 真实矩阵（seed_fixtures_test.go）构造升级库——不依赖当前版本表裁剪，
// 断言「新面直达 / 废弃形态收窄 / 人为删除不复活 / 幂等」。
func TestRealFixtureUpgrade(t *testing.T) {
	fixtures := map[int][][3]string{
		5:  seed5Policies,
		15: seed15Policies,
		18: seed18Policies,
		24: seed24Policies,
		25: seed25Policies,
		26: seed26Policies,
	}
	for n, fx := range fixtures {
		t.Run("v"+strconv.Itoa(n), func(t *testing.T) {
			svc := newTestEnforcer(t)
			db, e := svc.db, svc.enforcer
			// 清空首次种入，替换为真实 vN 形态
			policies, _ := e.GetPolicy()
			for _, p := range policies {
				_, _ = e.RemovePolicy(p[0], p[1], p[2])
			}
			for _, p := range fx {
				if _, err := e.AddPolicy(p[0], p[1], p[2]); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Model(&identity.PlatformSetting{}).
				Where("skey = ?", "rbac.seed_version").
				Update("value", strconv.Itoa(n)).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := migrateSeedVersion(db, e); err != nil {
				t.Fatal(err)
			}
			// ① 新面直达：现矩阵中引入版本 > n 且非替换新形态的条目应在
			for _, p := range defaultPolicies {
				pk := policyKey{p[0], p[1], p[2]}
				if policyIntroducedAt[pk] <= n {
					continue
				}
				if _, replaced := replacementNewKeys[pk]; replaced {
					continue // 替换新形态的断言见 ②
				}
				if has, _ := e.HasPolicy(p[0], p[1], p[2]); !has {
					t.Errorf("v%d 真实库升级后新条目 %v 未直达", n, p)
				}
			}
			// ② 废弃形态收窄：n ∈ [oldVer, ver) 的旧形态必须消失，新形态应在
			for _, r := range policyReplacements {
				if n >= r.oldVer && n < r.ver {
					if has, _ := e.HasPolicy(r.old[0], r.old[1], r.old[2]); has {
						t.Errorf("v%d 真实库升级后废弃形态 %v 仍残留", n, r.old)
					}
					if r.new != nil {
						if has, _ := e.HasPolicy(r.new[0], r.new[1], r.new[2]); !has {
							t.Errorf("v%d 真实库升级后替换新形态 %v 缺失", n, *r.new)
						}
					}
				}
			}
			// ③ 幂等
			snap, _ := e.GetPolicy()
			_, _ = migrateSeedVersion(db, e)
			again, _ := e.GetPolicy()
			if len(snap) != len(again) {
				t.Errorf("v%d 真实库二次迁移不幂等（%d → %d）", n, len(snap), len(again))
			}
		})
	}
}

// v0.12.19 独立复核 R1 反例（转回归）：真实 seed 18 宽形态库——
// ① admin 的 config-kv 宽形态（含 DELETE）升级后必须收窄；
// ② dev 的 config-kv 只读面是 v19 废弃面，升级后必须移除；
// ③ 管理员删除过 ops 宽形态的，升级后 ops 不得以新形态变相复活。
func TestSeed18DeletedOpsConfigKvNotResurrected(t *testing.T) {
	svc := newTestEnforcer(t)
	db, e := svc.db, svc.enforcer
	policies, _ := e.GetPolicy()
	for _, p := range policies {
		_, _ = e.RemovePolicy(p[0], p[1], p[2])
	}
	for _, p := range seed18Policies {
		if _, err := e.AddPolicy(p[0], p[1], p[2]); err != nil {
			t.Fatal(err)
		}
	}
	// 管理员删除 ops 的 config-kv 宽形态（独立复核反例操作）
	ok, err := e.RemovePolicy("ops", "/config-kv", "GET|POST|PUT|DELETE")
	if err != nil || !ok {
		t.Fatalf("删除未生效（ok=%v err=%v）", ok, err)
	}
	_, _ = e.RemovePolicy("ops", "/config-kv/*", "GET|POST|PUT|DELETE")
	if err := db.Model(&identity.PlatformSetting{}).
		Where("skey = ?", "rbac.seed_version").
		Update("value", "18").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := migrateSeedVersion(db, e); err != nil {
		t.Fatal(err)
	}
	// ① admin 宽形态收窄（独立复核反例：obsolete survived）
	if has, _ := e.HasPolicy("admin", "/config-kv", "GET|POST|PUT|DELETE"); has {
		t.Error("admin config-kv 宽形态（含 DELETE）升级后仍残留")
	}
	if has, _ := e.HasPolicy("admin", "/config-kv", "GET|POST|PUT"); !has {
		t.Error("admin config-kv 应获得收窄后形态")
	}
	// ② dev 废弃面移除
	if has, _ := e.HasPolicy("dev", "/config-kv", "GET"); has {
		t.Error("dev config-kv 只读面（v19 废弃）升级后仍残留")
	}
	// ③ ops 人为删除不复活（核心：新形态也不得出现）
	for _, obj := range []string{"/config-kv", "/config-kv/*"} {
		for _, act := range []string{"GET|POST|PUT|DELETE", "GET|POST|PUT"} {
			if has, _ := e.HasPolicy("ops", obj, act); has {
				t.Errorf("ops %s %s 被升级复活（人为删除不复活原则）", obj, act)
			}
		}
	}
}

// v0.12.19 独立复核 R2 反例（转回归）：管理员清空内置角色全部策略是
// 合法配置——迁移不得据此恢复默认矩阵（策略数 0 ≠ 未初始化）。
func TestClearedBuiltinRoleNotReseeded(t *testing.T) {
	svc := newTestEnforcer(t)
	db, e := svc.db, svc.enforcer
	// 清空 guest 全部策略（独立复核反例操作）
	policies, _ := e.GetFilteredPolicy(0, "guest")
	for _, p := range policies {
		_, _ = e.RemovePolicy(p[0], p[1], p[2])
	}
	if err := db.Model(&identity.PlatformSetting{}).
		Where("skey = ?", "rbac.seed_version").
		Update("value", "26").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := migrateSeedVersion(db, e); err != nil {
		t.Fatal(err)
	}
	after, _ := e.GetFilteredPolicy(0, "guest")
	if len(after) > 0 {
		t.Errorf("清空的 guest 升级后被恢复 %d 条默认策略——清空是用户意图", len(after))
	}
}

// seed 18 晚期库（ecf3afa 收窄后、bump v19 前）：config-kv 已是窄形态——
// 升级后窄形态保留、宽形态不出现（替换 old 不在 → 不动）。
func TestSeed18LateNarrowShapePreserved(t *testing.T) {
	svc := newTestEnforcer(t)
	db, e := svc.db, svc.enforcer
	policies, _ := e.GetPolicy()
	for _, p := range policies {
		_, _ = e.RemovePolicy(p[0], p[1], p[2])
	}
	for _, p := range seed18Policies {
		if p[1] == "/config-kv" || p[1] == "/config-kv/*" {
			continue // 跳过宽形态，模拟 ecf3afa 后的窄形态库
		}
		if _, err := e.AddPolicy(p[0], p[1], p[2]); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range [][3]string{
		{"admin", "/config-kv", "GET|POST|PUT"},
		{"admin", "/config-kv/*", "GET|POST|PUT"},
		{"ops", "/config-kv", "GET|POST|PUT"},
		{"ops", "/config-kv/*", "GET|POST|PUT"},
	} {
		if _, err := e.AddPolicy(p[0], p[1], p[2]); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(&identity.PlatformSetting{}).
		Where("skey = ?", "rbac.seed_version").
		Update("value", "18").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := migrateSeedVersion(db, e); err != nil {
		t.Fatal(err)
	}
	if has, _ := e.HasPolicy("admin", "/config-kv", "GET|POST|PUT"); !has {
		t.Error("seed 18 晚期库的窄形态升级后应保留")
	}
	for _, sub := range []string{"admin", "ops"} {
		for _, obj := range []string{"/config-kv", "/config-kv/*"} {
			if has, _ := e.HasPolicy(sub, obj, "GET|POST|PUT|DELETE"); has {
				t.Error("升级不得引入宽形态")
			}
		}
	}
}
