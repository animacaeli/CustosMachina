package rbac

import (
	"testing"

	"github.com/custos-machina/backend/internal/modules/identity"

	casbin "github.com/casbin/casbin/v2"
)

// v0.12.14 复核 §7.1 回归：/home summary+readiness 必须对非超管内置角色可读。
// v0.12.8~v0.12.10 上线首页/就绪度时漏种策略，admin/ops/dev 登录后首屏
// 调用即 403（E2E 全绿是因超管旁路——所有现存门禁都覆盖不到）。
func TestHomeReadableByNonSuperRoles(t *testing.T) {
	svc := newTestEnforcer(t)
	for _, role := range []string{"admin", "ops", "dev"} {
		for _, obj := range []string{"/home/summary", "/home/readiness"} {
			ok, err := EnforceAny(svc.enforcer, identity.ParseRoleList(role), obj, "GET")
			if err != nil || !ok {
				t.Errorf("%s 应可读 %s（got=%v err=%v）——非超管登录首屏将 403", role, obj, ok, err)
			}
		}
	}
	// guest 不给（扫码即得的角色，与 v26 收权方向一致）；写操作一律无
	if ok, _ := EnforceAny(svc.enforcer, identity.ParseRoleList("guest"), "/home/summary", "GET"); ok {
		t.Error("guest 不应可读 /home/summary")
	}
	if ok, _ := EnforceAny(svc.enforcer, identity.ParseRoleList("ops"), "/home/summary", "POST"); ok {
		t.Error("/home 应只读（无 POST）")
	}
}

// v26→v27 迁移：存量库（seed_version=26，无 home 条目）逐条补种；
// 存量自定义角色同步补基础读集；迁移幂等。
func TestHomeSeedMigrationFromV26(t *testing.T) {
	svc := newTestEnforcer(t)
	db, e := svc.db, svc.enforcer

	// 回退到 v26 存量形态：去掉首次种入带入的 home 条目，版本号置 26
	for _, sub := range []string{"admin", "ops", "dev"} {
		for _, obj := range []string{"/home/summary", "/home/readiness"} {
			if has, _ := e.HasPolicy(sub, obj, "GET"); has {
				if _, err := e.RemovePolicy(sub, obj, "GET"); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	// 存量自定义角色（已有策略，v27 前创建——基础读集里没有 home）
	if _, err := e.AddPolicy("发布员", "/projects", "GET"); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&identity.PlatformSetting{}).
		Where("skey = ?", "rbac.seed_version").
		Update("value", "26").Error; err != nil {
		t.Fatal(err)
	}

	changed, err := migrateSeedVersion(db, e)
	if err != nil {
		t.Fatalf("v26→v27 迁移失败: %v", err)
	}
	if !changed {
		t.Fatal("v26 存量库迁移应有变更")
	}
	for _, sub := range []string{"admin", "ops", "dev", "发布员"} {
		for _, obj := range []string{"/home/summary", "/home/readiness"} {
			if has, _ := e.HasPolicy(sub, obj, "GET"); !has {
				t.Errorf("迁移后 %s 应有 %s GET 策略", sub, obj)
			}
		}
	}
	// guest 不在迁移补种面
	if has, _ := e.HasPolicy("guest", "/home/summary", "GET"); has {
		t.Error("guest 不应被补种 home 策略")
	}

	// 幂等：同版本再跑不报错、不重复添加（返回 true 恒成立——版本推进
	// 语义，v0.12.15 复核后返回值不再表示策略变更）
	if _, err := migrateSeedVersion(db, e); err != nil {
		t.Fatalf("重复迁移应幂等: %v", err)
	}
	for _, sub := range []string{"admin", "ops", "dev", "发布员"} {
		if n := countPolicies(e, sub, "/home/summary"); n != 1 {
			t.Errorf("重复迁移后 %s 的 /home/summary 策略应恰 1 条，实际 %d", sub, n)
		}
	}
}

// v0.12.15 复核 §八.1-B 回归：跳级升级（v0.12.1="24"/v0.12.2="25"/
// v0.11.x="22" 直升）不得漏种 home——精确匹配 "26" 的旧实现对跳级库
// 既不补种也不推进版本号（永久卡死）；数值比较后任意旧版本直升均补齐。
func TestHomeSeedSkippedLevelUpgrade(t *testing.T) {
	for _, old := range []string{"22", "24", "25"} {
		t.Run("v"+old+" 直升", func(t *testing.T) {
			svc := newTestEnforcer(t)
			db, e := svc.db, svc.enforcer
			for _, sub := range []string{"admin", "ops", "dev"} {
				for _, obj := range []string{"/home/summary", "/home/readiness"} {
					_, _ = e.RemovePolicy(sub, obj, "GET")
				}
			}
			if _, err := e.AddPolicy("发布员", "/projects", "GET"); err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&identity.PlatformSetting{}).
				Where("skey = ?", "rbac.seed_version").
				Update("value", old).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := migrateSeedVersion(db, e); err != nil {
				t.Fatalf("跳级迁移失败: %v", err)
			}
			for _, sub := range []string{"admin", "ops", "dev", "发布员"} {
				if has, _ := e.HasPolicy(sub, "/home/summary", "GET"); !has {
					t.Errorf("跳级 %s 直升后 %s 应补种 /home/summary", old, sub)
				}
			}
		})
	}
}

// customRoleSubjects 补种带版本 gate（v0.12.15 复核 §八.1-C）：v27 及以上
// 的库不再重放自定义角色补种——管理员删除自定义角色的 home 条目后，
// 后续种子版本升级不会复活它。
func TestCustomRoleHomeSeedNotReplayedOnV27Plus(t *testing.T) {
	svc := newTestEnforcer(t)
	db, e := svc.db, svc.enforcer
	if _, err := e.AddPolicy("发布员", "/projects", "GET"); err != nil {
		t.Fatal(err)
	}
	// 模拟 v27 库：管理员删除自定义角色的 home 条目
	if err := db.Model(&identity.PlatformSetting{}).
		Where("skey = ?", "rbac.seed_version").
		Update("value", "26").Error; err != nil {
		t.Fatal(err)
	}
	_, _ = migrateSeedVersion(db, e) // 先补种（v26 → home 附带）
	// 升到 v28 语义：版本置 27（= 已补过），删除人为条目后再跑迁移不应复活
	if err := db.Model(&identity.PlatformSetting{}).
		Where("skey = ?", "rbac.seed_version").
		Update("value", "27").Error; err != nil {
		t.Fatal(err)
	}
	_, _ = e.RemovePolicy("发布员", "/home/summary", "GET")
	if _, err := migrateSeedVersion(db, e); err != nil {
		t.Fatal(err)
	}
	if has, _ := e.HasPolicy("发布员", "/home/summary", "GET"); has {
		t.Error("v27+ 的库上人为删除的自定义角色 home 条目不应被复活")
	}
}

func countPolicies(e *casbin.SyncedEnforcer, sub, obj string) int {
	ps, _ := e.GetFilteredPolicy(0, sub, obj)
	return len(ps)
}
