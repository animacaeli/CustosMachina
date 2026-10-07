package rbac

import (
	"testing"

	"github.com/custos-machina/backend/internal/modules/identity"
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

	// 幂等：同版本再跑不报错、不产生新变更
	if _, err := migrateSeedVersion(db, e); err != nil {
		t.Fatalf("重复迁移应幂等: %v", err)
	}
}
