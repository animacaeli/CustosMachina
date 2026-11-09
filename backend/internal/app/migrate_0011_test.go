package app

import (
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func m0011DB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := db.Exec(`CREATE TABLE config_files (
		id INTEGER PRIMARY KEY, project_id INTEGER, rel_path TEXT,
		path TEXT, content TEXT, sensitive INTEGER)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE config_versions (
		id INTEGER PRIMARY KEY, file_id INTEGER, content TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func seedM0011(t *testing.T, db *gorm.DB, id int, projectID int, relPath, content string, sensitive int) {
	t.Helper()
	if err := db.Exec(`INSERT INTO config_files (id, project_id, rel_path, path, content, sensitive)
		VALUES (?, ?, ?, ?, ?, ?)`, id, projectID, relPath, "/x", content, sensitive).Error; err != nil {
		t.Fatal(err)
	}
}

// relPaths 迁移后全量快照（逐条对照，防批量误伤——画像规程）。
func relPaths(t *testing.T, db *gorm.DB) map[int]string {
	t.Helper()
	rows := []struct {
		ID      int
		RelPath string
	}{}
	if err := db.Raw(`SELECT id, rel_path FROM config_files ORDER BY id`).Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	out := map[int]string{}
	for _, r := range rows {
		out[r.ID] = r.RelPath
	}
	return out
}

// 迁移规则矩阵（决策见 migrate_0011.go 头注）。
func TestMigrateConfigEnvSemantics(t *testing.T) {
	db := m0011DB(t)
	seedM0011(t, db, 1, 1, "prod/demo/app.yaml", "a: 1", 0)   // prod 不动
	seedM0011(t, db, 2, 1, "", "bare", 1)                     // 裸文件不动（sensitive 清零）
	seedM0011(t, db, 3, 1, "test/dev2/sub/app.env", "k=v", 0) // → dev2/sub/app.env
	seedM0011(t, db, 4, 1, "test/demo/app.yaml", "b: 2", 0)   // → dev1/demo/app.yaml（本地演示形态）
	seedM0011(t, db, 5, 1, "test/app.yaml", "c: 3", 0)        // → dev1/app.yaml（直接文件）
	seedM0011(t, db, 6, 1, "canary/same.yaml", "s: 1", 0)     // 与 prod 一致 → 删除
	seedM0011(t, db, 7, 1, "prod/same.yaml", "s: 1", 0)       // （canary 对比基准）
	seedM0011(t, db, 8, 1, "canary/empty.yaml", "", 0)        // 无内容 → 删除
	if err := db.Exec(`INSERT INTO config_versions (file_id, content) VALUES (6, 's: 1'), (8, '')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateConfigEnvSemantics(db); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	got := relPaths(t, db)
	want := map[int]string{
		1: "prod/demo/app.yaml",
		2: "",
		3: "dev2/sub/app.env",
		4: "dev1/demo/app.yaml",
		5: "dev1/app.yaml",
		7: "prod/same.yaml",
		// 6/8 已删（canary 引用 prod）
	}
	if len(got) != len(want) {
		t.Fatalf("行数不符: got=%v want=%v", got, want)
	}
	for id, p := range want {
		if got[id] != p {
			t.Errorf("id=%d rel_path=%q want %q", id, got[id], p)
		}
	}
	// canary 版本链随之删除
	var n int64
	db.Raw(`SELECT COUNT(*) FROM config_versions`).Scan(&n)
	if n != 0 {
		t.Errorf("canary 版本应删除: %d", n)
	}
	// sensitive 全量清零
	db.Raw(`SELECT COUNT(*) FROM config_files WHERE sensitive != 0`).Scan(&n)
	if n != 0 {
		t.Errorf("sensitive 应清零: %d", n)
	}
}

// fail-closed：canary 有独立内容（prod 无对应 / 内容不一致）中止并指名文件。
func TestMigrateConfigEnvSemanticsCanaryGuard(t *testing.T) {
	for name, tc := range map[string]struct {
		relPath, canaryContent, prodContent string
		setProd                             bool
	}{
		"prod 无对应": {relPath: "canary/orphan.yaml", canaryContent: "x: 1", setProd: false},
		"内容不一致":    {relPath: "canary/diff.yaml", canaryContent: "x: 1", prodContent: "x: 2", setProd: true},
	} {
		t.Run(name, func(t *testing.T) {
			db := m0011DB(t)
			seedM0011(t, db, 1, 1, tc.relPath, tc.canaryContent, 0)
			if tc.setProd {
				seedM0011(t, db, 2, 1, "prod/"+strings.TrimPrefix(tc.relPath, "canary/"), tc.prodContent, 0)
			}
			err := migrateConfigEnvSemantics(db)
			if err == nil {
				t.Fatal("应中止报错")
			}
			if !strings.Contains(err.Error(), tc.relPath) {
				t.Errorf("报错应指名文件: %v", err)
			}
		})
	}
}

// 未知首段中止；无表跳级兼容。
func TestMigrateConfigEnvSemanticsMisc(t *testing.T) {
	db := m0011DB(t)
	seedM0011(t, db, 1, 1, "staging/app.yaml", "a: 1", 0)
	if err := migrateConfigEnvSemantics(db); err == nil || !strings.Contains(err.Error(), "staging/app.yaml") {
		t.Fatalf("未知首段应中止: %v", err)
	}
	// 无 config_files 表（跳级升级）静默通过
	db2, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateConfigEnvSemantics(db2); err != nil {
		t.Fatalf("无表应跳过: %v", err)
	}
}
