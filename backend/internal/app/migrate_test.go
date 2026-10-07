package app

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/modules/identity"
	"github.com/custos-machina/backend/internal/pkg/database"
)

// v0.12.2 复核 N7 回归：存量库（升级自 ≤v0.11.1，已有业务表、版本表记录
// 到 0008）跑增量迁移路径——该路径不执行 AutoMigrate，v0.12.0/0.12.1 新增的
// k3s_clusters 与 alert_events 必须经 0009/0010 钩子建出，否则集群管理/
// 告警历史在存量安装上整块 500。
func TestUpgradeMigrationsCreateLateTables(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	// 造存量库形态：业务表已存在（触发增量路径）+ 版本表已应用到 0008
	if err := db.Exec("CREATE TABLE servers (id integer primary key, name text)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(database.SchemaMigrationModel()); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"0001", "0002", "0003", "0004", "0005", "0006", "0007", "0008"} {
		if err := db.Exec("INSERT INTO schema_migrations (version, applied_at) VALUES (?, datetime('now'))", v).Error; err != nil {
			t.Fatal(err)
		}
	}

	if err := database.Migrate(db, nil); err != nil {
		t.Fatalf("增量迁移失败: %v", err)
	}

	for _, table := range []string{"alert_events", "k3s_clusters"} {
		if !db.Migrator().HasTable(table) {
			t.Errorf("存量库升级后应建出 %s 表", table)
		}
	}
	var cnt int64
	db.Table("schema_migrations").Where("version IN ?", []string{"0009", "0010"}).Count(&cnt)
	if cnt != 2 {
		t.Errorf("0009/0010 应记为已应用，got %d", cnt)
	}
}

// 2026-10-07 生产事故回归：v0.9.x 时代老库（无 ai_conversations、无版本表、
// platform_settings 主键列还叫 key）直升当前版——0005 曾对从未建过的表做
// ALTER 直接炸掉启动。修复后：迁移全放行 + 模型补齐建出缺失表，且 0006 的
// key→skey 改名仍正确执行（skey 须为主键，否则 UpsertSetting 全线崩）。
func TestJumpUpgradeFromPreMigrationEra(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	// 造 v0.9.x 形态：业务表在、版本表不在、聊天表不在、设置表主键列叫 key
	if err := db.Exec(`CREATE TABLE servers (id integer primary key, name text)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE platform_settings ("key" TEXT PRIMARY KEY, value TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO platform_settings ("key", value) VALUES ('x', 'y')`).Error; err != nil {
		t.Fatal(err)
	}

	if err := database.Migrate(db, ProvideDBModelList()); err != nil {
		t.Fatalf("跳级迁移失败: %v", err)
	}

	// 聊天表由模型补齐建出（0005 钩子对缺表跳过）
	if !db.Migrator().HasTable("ai_conversations") {
		t.Error("ai_conversations 应由模型补齐建出")
	}
	// 0006 改名在补齐之前执行：skey 为主键、key 列不复存在、数据保留
	var pi []struct {
		Name string `gorm:"column:name"`
		Pk   int    `gorm:"column:pk"`
	}
	db.Raw("PRAGMA table_info(platform_settings)").Scan(&pi)
	byName := map[string]int{}
	for _, c := range pi {
		byName[c.Name] = c.Pk
	}
	if _, hasOld := byName["key"]; hasOld {
		t.Error("旧列 key 应已被改名")
	}
	if byName["skey"] != 1 {
		t.Errorf("skey 应为主键（pk=1），got %v", byName["skey"])
	}
	var row struct {
		Skey   string
		Value_ string `gorm:"column:value"`
	}
	if err := db.Table("platform_settings").Where("skey = ?", "x").Scan(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Value_ != "y" {
		t.Errorf("改名应保留数据，got %q", row.Value_)
	}
	// 方言安全 upsert 可用（事故第二炸点）
	if err := identity.UpsertSetting(db, t.Context(), "rbac.seed_version", "26"); err != nil {
		t.Fatalf("UpsertSetting 失败（skey 主键缺失?）: %v", err)
	}
}

// 跳级自愈的另一路径：表已存在但缺列（如手工造的极简基表）——Go 钩子
// （HasTable+!HasColumn 守卫）补列，模型补齐兜底其余。
func TestJumpUpgradeExistingTableMissingColumn(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "t.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE servers (id INTEGER PRIMARY KEY, name TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	// builds 存在但无 log_tail；ai_conversations 存在但无 compact 两列
	if err := db.Exec(`CREATE TABLE builds (id INTEGER PRIMARY KEY, status TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE ai_conversations (id INTEGER PRIMARY KEY, title TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db, ProvideDBModelList()); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if !db.Migrator().HasColumn("builds", "log_tail") {
		t.Error("0004 钩子应为存量 builds 补 log_tail")
	}
	if !db.Migrator().HasColumn("ai_conversations", "compact_text") ||
		!db.Migrator().HasColumn("ai_conversations", "compact_after_id") {
		t.Error("0005 钩子应为存量 ai_conversations 补 compact 两列")
	}
}
