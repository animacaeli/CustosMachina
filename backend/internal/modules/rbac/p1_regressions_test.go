package rbac

import (
	"fmt"
	"testing"

	"github.com/custos-machina/backend/internal/modules/identity"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	casbin "github.com/casbin/casbin/v2"
)

// v0.12.19 复核 P1-A：oldVer 必须等于旧形态的真实引入版本（曾误填
// ver-1，[真实引入, ver-1) 窗口内库的「人为删除不复活」失效）。本测试用
// git 固化的真实 fixture 做**区间校验**：对每个替换，oldVer 必须落在
// 「最晚不含旧形态的档」与「最早含旧形态的档」之间（L < oldVer ≤ F）——
// 手滑回填 ver-1 或任意值都会被夹出。
func TestReplacementOldVerAgainstFixtures(t *testing.T) {
	// {替换索引, 最晚不含档的 fixture, 最早含档的 fixture}
	cases := []struct {
		name        string
		r           policyReplacement
		lastWithout int
		firstWith   int
	}{
		{"ai v16", policyReplacement{ver: 16, oldVer: 13, old: policyKey{"admin", "/ai/*", "GET|PUT|POST"}}, 5, 13},
		{"config-kv v19", policyReplacement{ver: 19, oldVer: 18, old: policyKey{"admin", "/config-kv", "GET|POST|PUT|DELETE"}}, 15, 18},
		{"alerts v25", policyReplacement{ver: 25, oldVer: 14, old: policyKey{"dev", "/observ/alerts/*", "GET|DELETE|POST"}}, 5, 14},
		{"slots v26", policyReplacement{ver: 26, oldVer: 6, old: policyKey{"guest", "/slots", "GET|POST"}}, 5, 15},
	}
	fixtures := map[int][][3]string{
		5: seed5Policies, 13: seed13Policies, 14: seed14Policies,
		15: seed15Policies, 18: seed18Policies,
	}
	has := func(n int, k [3]string) bool {
		for _, p := range fixtures[n] {
			if p == k {
				return true
			}
		}
		return false
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if has(tc.firstWith, tc.r.old) {
				// 最早含档确实含——校验 oldVer ≤ firstWith 且 > lastWithout
			} else {
				t.Fatalf("校验锚点错误：fixture v%d 应含旧形态 %v", tc.firstWith, tc.r.old)
			}
			if has(tc.lastWithout, tc.r.old) {
				t.Fatalf("校验锚点错误：fixture v%d 不应含旧形态 %v", tc.lastWithout, tc.r.old)
			}
			if !(tc.lastWithout < tc.r.oldVer && tc.r.oldVer <= tc.firstWith) {
				t.Errorf("oldVer=%d 不在真实引入区间 (%d, %d] 内——git 实锤的引入版本才是合法值", tc.r.oldVer, tc.lastWithout, tc.firstWith)
			}
		})
	}
	// 全表与 policyReplacements 实际声明一致（防测试用例与表脱节；
	// 按 ver/oldVer/old 三字段匹配——new 指针不参与比较）
	if len(policyReplacements) != 10 {
		t.Fatalf("policyReplacements 条数 %d != 10——本测试的锚点用例需同步更新", len(policyReplacements))
	}
	for _, tc := range cases {
		found := false
		for _, r := range policyReplacements {
			if r.ver == tc.r.ver && r.oldVer == tc.r.oldVer && r.old == tc.r.old {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("表声明与校验用例不一致（ver=%d oldVer=%d old=%v）", tc.r.ver, tc.r.oldVer, tc.r.old)
		}
	}
}

// v0.12.19 复核 P1-A 六场景反例回归：窗口内库（ai {13,14}、alerts {14..23}、
// slots {5..24}）删除旧形态后升级，新形态不得复活。
func TestWindowLibrariesDeletedOldNotResurrected(t *testing.T) {
	cases := []struct {
		name   string
		seed   int
		oldKey [3]string
		newKey [3]string
	}{
		{"seed13 删 ai 旧形态", 13,
			[3]string{"admin", "/ai/*", "GET|PUT|POST"},
			[3]string{"admin", "/ai/*", "GET|PUT|POST|DELETE"}},
		{"seed14 删 ai 旧形态", 14,
			[3]string{"admin", "/ai/*", "GET|PUT|POST"},
			[3]string{"admin", "/ai/*", "GET|PUT|POST|DELETE"}},
		{"seed18 删 alerts 旧形态", 18,
			[3]string{"dev", "/observ/alerts/*", "GET|DELETE|POST"},
			[3]string{"dev", "/observ/alerts/*", "GET|POST"}},
		{"seed24 删 slots 旧形态", 24,
			[3]string{"guest", "/slots", "GET|POST"},
			[3]string{"guest", "/slots", "GET"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestEnforcer(t)
			db, e := svc.db, svc.enforcer
			policies, _ := e.GetPolicy()
			for _, p := range policies {
				_, _ = e.RemovePolicy(p[0], p[1], p[2])
			}
			// 构造「该年代库曾持有旧形态、管理员已收权」的状态：旧形态
			// 不在现矩阵（历史形态），删除后 casbin 无痕迹——等价于
			// 现矩阵跳过新形态（旧形态本就不可种）
			for _, p := range defaultPolicies {
				if [3]string{p[0], p[1], p[2]} == tc.newKey {
					continue // 管理员收权：新形态不种
				}
				if _, err := e.AddPolicy(p[0], p[1], p[2]); err != nil {
					t.Fatal(err)
				}
			}
			if err := setSeedVersion(t, db, fmt.Sprint(tc.seed)); err != nil {
				t.Fatal(err)
			}
			if _, err := migrateSeedVersion(db, e); err != nil {
				t.Fatal(err)
			}
			if has, _ := e.HasPolicy(tc.newKey[0], tc.newKey[1], tc.newKey[2]); has {
				t.Errorf("seed %d 删除旧形态后，新形态 %v 被升级复活", tc.seed, tc.newKey)
			}
		})
	}
}

// v0.12.19 复核 P1-B：全表清空（合法配置）后重启，不得全量重种——
// 首次初始化判据必须是「表空 且 无 seed_version 记录」。
func TestGloballyClearedNotReseededOnRestart(t *testing.T) {
	for _, seed := range []string{"27", "26"} {
		t.Run("seed="+seed, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			if err := db.AutoMigrate(&identity.PlatformSetting{}, &identity.User{}); err != nil {
				t.Fatal(err)
			}
			if err := db.AutoMigrate(Models()...); err != nil {
				t.Fatal(err)
			}
			// 正常初始化（首启全量种入 + 版本写 27）
			e1, _, err := NewEnforcer(db)
			if err != nil {
				t.Fatal(err)
			}
			// 管理员依次清空全部内置角色策略（合法配置）
			policies, _ := e1.GetPolicy()
			for _, p := range policies {
				_, _ = e1.RemovePolicy(p[0], p[1], p[2])
			}
			// 按场景改版本（27=当前；26=旧版全清库，重启应走 delta 只补 home）
			if err := setSeedVersion(t, db, seed); err != nil {
				t.Fatal(err)
			}
			// 重启
			e2, _, err := NewEnforcer(db)
			if err != nil {
				t.Fatal(err)
			}
			after, _ := e2.GetPolicy()
			if seed == "27" {
				if len(after) != 0 {
					t.Fatalf("seed=27 全清空库重启后被重种 %d 条——全表清空是合法配置", len(after))
				}
			} else {
				// seed=26：delta 只补 v27 新面 = home 六条，其余保持清空
				if len(after) != 6 {
					t.Fatalf("seed=26 全清空库重启后应有且仅有 home 六条（v27 新面直达），实际 %d 条", len(after))
				}
				for _, sub := range []string{"admin", "ops", "dev"} {
					if has, _ := e2.HasPolicy(sub, "/home/summary", "GET"); !has {
						t.Errorf("seed=26 全清空库重启后 %s 应获 home 新面", sub)
					}
				}
			}
		})
	}
}

// v0.12.19 复核 P1-C：替换持久化失败必须上抛且不推进 seed（下次启动
// 重试），不得吞错后半迁移永久化。SQLite trigger 注入故障。
func TestReplacementPersistenceFailurePropagates(t *testing.T) {
	for _, tc := range []struct {
		name    string
		trigger string
	}{
		{"remove 失败", "CREATE TRIGGER zz_fail_delete BEFORE DELETE ON casbin_rule BEGIN SELECT RAISE(ABORT, 'injected delete failure'); END"},
		{"add 失败", "CREATE TRIGGER zz_fail_insert BEFORE INSERT ON casbin_rule BEGIN SELECT RAISE(ABORT, 'injected insert failure'); END"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestEnforcer(t)
			db, e := svc.db, svc.enforcer
			// 构造 seed 18 宽库（存在待收窄的 config-kv 宽形态）——
			// 先构造后注错（add 失败用全挡 INSERT trigger，构造须在其前）
			ok, err := e.AddPolicy("admin", "/config-kv", "GET|POST|PUT|DELETE")
			if err != nil || !ok {
				t.Fatalf("构造失败（ok=%v err=%v）", ok, err)
			}
			_, _ = e.AddPolicy("admin", "/config-kv/*", "GET|POST|PUT|DELETE")
			// 移除新形态（模拟管理员收权过）——否则替换块 HasPolicy 命中
			// 跳过 add，INSERT 故障注入不生效
			for _, obj := range []string{"/config-kv", "/config-kv/*"} {
				_, _ = e.RemovePolicy("admin", obj, "GET|POST|PUT")
			}
			if err := db.Exec(tc.trigger).Error; err != nil {
				t.Fatal(err)
			}
			defer db.Exec("DROP TRIGGER IF EXISTS zz_fail_delete")
			defer db.Exec("DROP TRIGGER IF EXISTS zz_fail_insert")
			if err := setSeedVersion(t, db, "18"); err != nil {
				t.Fatal(err)
			}
			changed, err := migrateSeedVersion(db, e)
			if err == nil {
				t.Fatalf("%s：迁移应上抛持久化错误（changed=%v）——吞错导致半迁移永久化", tc.name, changed)
			}
			if changed {
				t.Errorf("%s：失败路径不得返回 true（true 会推进 seed 使失败永久化）", tc.name)
			}
		})
	}
}

func setSeedVersion(t *testing.T, db *gorm.DB, v string) error {
	t.Helper()
	return db.Exec("INSERT INTO platform_settings (skey, value) VALUES ('rbac.seed_version', ?) "+
		"ON CONFLICT(skey) DO UPDATE SET value = excluded.value", v).Error
}

var _ = casbin.SyncedEnforcer{} // 保留 casbin 引用（fixture 类型一致性）

// v0.12.20 复核 P1-1 完整重启回归：add 失败 → 解除故障 → 重建 enforcer →
// 自动补回新形态 → 版本才推进。旧序「先删后加」在该场景永久丢新形态。
func TestReplacementAddFailureRecoversAfterRestart(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&identity.PlatformSetting{}, &identity.User{}); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(Models()...); err != nil {
		t.Fatal(err)
	}
	e1, _, err := NewEnforcer(db)
	if err != nil {
		t.Fatal(err)
	}
	// 构造 seed 18 宽库：持有 config-kv 宽形态、无窄形态
	policies, _ := e1.GetPolicy()
	for _, p := range policies {
		_, _ = e1.RemovePolicy(p[0], p[1], p[2])
	}
	for _, p := range seed18Policies {
		if _, err := e1.AddPolicy(p[0], p[1], p[2]); err != nil {
			t.Fatal(err)
		}
	}
	for _, obj := range []string{"/config-kv", "/config-kv/*"} {
		for _, sub := range []string{"admin", "ops"} {
			_, _ = e1.RemovePolicy(sub, obj, "GET|POST|PUT") // 去窄形态
		}
	}
	if err := setSeedVersion(t, db, "18"); err != nil {
		t.Fatal(err)
	}
	// 注入 add 故障（只挡窄形态插入；宽形态的删除不受影响）→ 首轮迁移失败
	if err := db.Exec("CREATE TRIGGER zz_fail_narrow BEFORE INSERT ON casbin_rule " +
		"WHEN new.v1 LIKE '/config-kv%' AND new.v2 = 'GET|POST|PUT' " +
		"BEGIN SELECT RAISE(ABORT, 'injected narrow insert failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := migrateSeedVersion(db, e1); err == nil {
		t.Fatal("首轮迁移应失败（add 故障）")
	}
	// 解除故障，重启（新 enforcer 从库重新加载）
	if err := db.Exec("DROP TRIGGER zz_fail_narrow").Error; err != nil {
		t.Fatal(err)
	}
	e2, _, err := NewEnforcer(db)
	if err != nil {
		t.Fatalf("重启失败: %v", err)
	}
	// 新形态自动补回 + 宽形态不残留 + 版本推进
	for _, obj := range []string{"/config-kv", "/config-kv/*"} {
		if has, _ := e2.HasPolicy("admin", obj, "GET|POST|PUT"); !has {
			t.Errorf("重启后新形态 %s 未补回——旧序会永久丢失", obj)
		}
		if has, _ := e2.HasPolicy("admin", obj, "GET|POST|PUT|DELETE"); has {
			t.Errorf("重启后宽形态 %s 仍残留", obj)
		}
	}
	var setting identity.PlatformSetting
	if err := db.Where("skey = ?", "rbac.seed_version").First(&setting).Error; err != nil {
		t.Fatal(err)
	}
	if setting.Value != policySeedVersion {
		t.Errorf("重启收敛后 seed 应推进到 %s，实际 %q", policySeedVersion, setting.Value)
	}
}

// v0.12.20 复核 P1-2：seed 查询故障必须 fail-closed——表空 + 查询错误时
// 不得写入任何默认策略（旧实现把错误折叠为「不存在」→ 重种 196 条）。
func TestSeedLookupFailureFailClosed(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&identity.PlatformSetting{}, &identity.User{}); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(Models()...); err != nil {
		t.Fatal(err)
	}
	// 正常初始化后全清空 + seed=27（P1-B 场景），再令 seed 查询故障
	e1, _, err := NewEnforcer(db)
	if err != nil {
		t.Fatal(err)
	}
	policies, _ := e1.GetPolicy()
	for _, p := range policies {
		_, _ = e1.RemovePolicy(p[0], p[1], p[2])
	}
	if err := db.Exec("ALTER TABLE platform_settings RENAME TO zz_broken_settings").Error; err != nil {
		t.Fatal(err)
	}
	defer db.Exec("ALTER TABLE zz_broken_settings RENAME TO platform_settings")
	if _, _, err := NewEnforcer(db); err == nil {
		t.Fatal("seed 查询故障应导致启动失败（fail-closed）")
	}
	// casbin 表必须保持为空（未写入任何默认策略）
	var n int64
	if err := db.Raw("SELECT COUNT(*) FROM casbin_rule").Scan(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("查询故障期间写入了 %d 条策略——必须 fail-closed 不动策略表", n)
	}
	// 恢复后重启：全清空语义保持（P1-B 行为不回归）
	if err := db.Exec("ALTER TABLE zz_broken_settings RENAME TO platform_settings").Error; err != nil {
		t.Fatal(err)
	}
	e2, _, err := NewEnforcer(db)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := e2.GetPolicy()
	if len(after) != 0 {
		t.Fatalf("故障恢复重启后被重种 %d 条——全清空语义被破坏", len(after))
	}
}

// 占位 seed 机制的续种闭环（v0.12.20 P1-2 配套）：全新库首种中途失败
// （种入被阻）→ 重启 → delta(oldN=0) 续种补齐全部默认策略——不残留
// 「表空但 seed 缺失」的模糊态（那会再次触发全量首种路径）。
func TestFirstSeedInterruptedResumesOnRestart(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&identity.PlatformSetting{}, &identity.User{}); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(Models()...); err != nil {
		t.Fatal(err)
	}
	// 先正常初始化（建 casbin 表），再重置为「占位已写、种入被阻」的中断态
	e0, _, err := NewEnforcer(db)
	if err != nil {
		t.Fatal(err)
	}
	policies, _ := e0.GetPolicy()
	for _, p := range policies {
		_, _ = e0.RemovePolicy(p[0], p[1], p[2])
	}
	if err := setSeedVersion(t, db, "0"); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TRIGGER zz_block_seed BEFORE INSERT ON casbin_rule BEGIN SELECT RAISE(ABORT, 'blocked'); END").Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := NewEnforcer(db); err == nil {
		t.Fatal("种入被阻应报错")
	}
	if err := db.Exec("DROP TRIGGER zz_block_seed").Error; err != nil {
		t.Fatal(err)
	}
	// 重启：续种收敛（delta(oldN=0) 补全部缺失 + 版本推进）
	e, _, err := NewEnforcer(db)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := e.GetPolicy()
	if len(after) != len(defaultPolicies) {
		t.Fatalf("重启后续种应补齐 %d 条默认策略，实际 %d 条", len(defaultPolicies), len(after))
	}
	var setting identity.PlatformSetting
	if err := db.Where("skey = ?", "rbac.seed_version").First(&setting).Error; err != nil {
		t.Fatal(err)
	}
	if setting.Value != policySeedVersion {
		t.Errorf("续种收敛后版本应推进到 %s，实际 %q", policySeedVersion, setting.Value)
	}
}
