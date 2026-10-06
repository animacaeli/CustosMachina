package app

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

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
