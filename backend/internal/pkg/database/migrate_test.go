package database

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type migTestModel struct {
	ID   uint `gorm:"primarykey"`
	Name string
}

func (migTestModel) TableName() string { return "mig_test_users" }

func TestMigrateFreshAndIncremental(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "t.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开库失败: %v", err)
	}
	// 新库：AutoMigrate 建表 + baseline 全部版本
	if err := Migrate(db, []any{&migTestModel{}}); err != nil {
		t.Fatalf("新库迁移失败: %v", err)
	}
	if !db.Migrator().HasTable(&migTestModel{}) {
		t.Fatal("新库应建业务表")
	}
	var n int64
	db.Model(&schemaMigration{}).Count(&n)
	if n == 0 {
		t.Fatal("新库应 baseline 标记迁移版本")
	}
	// 幂等：再跑不报错不重复
	if err := Migrate(db, []any{&migTestModel{}}); err != nil {
		t.Fatalf("幂等迁移失败: %v", err)
	}
	var n2 int64
	db.Model(&schemaMigration{}).Count(&n2)
	if n2 != n {
		t.Fatalf("重复应用迁移: %d -> %d", n, n2)
	}
}

func TestMigrateExistingDBSkipsAutoMigrate(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "t.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	// 模拟存量库：有 servers/builds 表但没有新模型表（0004 起 ALTER builds 依赖基表）
	if err := db.Exec(`CREATE TABLE servers (id INTEGER PRIMARY KEY, name TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE builds (id INTEGER PRIMARY KEY, status TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db, []any{&migTestModel{}}); err != nil {
		t.Fatalf("存量库迁移失败: %v", err)
	}
	if !db.Migrator().HasColumn("builds", "log_tail") {
		t.Fatal("0004 应为存量 builds 表加 log_tail 列")
	}
	// 存量库不走 AutoMigrate：新模型表不应被建
	if db.Migrator().HasTable(&migTestModel{}) {
		t.Fatal("存量库不应自动建新模型表（变更须走版本化迁移）")
	}
}

func TestSplitSQL(t *testing.T) {
	got := splitSQL("-- 注释\nSELECT 1;\n\nSELECT 2;\n-- 尾注")
	if len(got) != 2 || got[0] != "SELECT 1" || got[1] != "SELECT 2" {
		t.Fatalf("分段异常: %#v", got)
	}
}
