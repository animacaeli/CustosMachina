// migrate.go P6-M6 版本化 migration（#9，开源化前置）：
// 新库（无业务表）→ AutoMigrate 快速起步 + baseline 标记全部迁移已应用；
// 存量库 → 不再每次跑 AutoMigrate（只加不删、无序、生产不可控），
// schema 变更一律走 embed 的顺序迁移文件（每版本一个事务，SQLite/MySQL
// 双方言约束：只写两者兼容的 DDL——ADD COLUMN/CREATE INDEX 等；
// 方言特有操作放 Go 迁移钩子）。
package database

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// schemaMigration 版本记录表（只在版本化路径使用）。
type schemaMigration struct {
	Version   string    `gorm:"primarykey"`
	AppliedAt time.Time `json:"appliedAt"`
}

func (schemaMigration) TableName() string { return "schema_migrations" }

// migrationFile 解析后的迁移项。
type migrationFile struct {
	Version string // 文件名前缀（如 0001）
	Stmts   []string
}

// parseMigrations 从 embed FS 按版本序解析迁移文件（分号分段，去注释与空段）。
func parseMigrations() ([]migrationFile, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil, err
	}
	var out []migrationFile
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		raw, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, err
		}
		version := strings.SplitN(name, "_", 2)[0]
		if version == "" || version == name {
			return nil, fmt.Errorf("迁移文件名须为 NNNN_描述.sql: %s", name)
		}
		mf := migrationFile{Version: version}
		for _, seg := range splitSQL(string(raw)) {
			if seg != "" {
				mf.Stmts = append(mf.Stmts, seg)
			}
		}
		out = append(out, mf)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	// 版本号唯一
	for i := 1; i < len(out); i++ {
		if out[i].Version == out[i-1].Version {
			return nil, fmt.Errorf("迁移版本号重复: %s", out[i].Version)
		}
	}
	return out, nil
}

// splitSQL 逐字符扫分号分段，跳过 -- 行注释（迁移文件不嵌套分号字符串的
// 简单约定；复杂语句放 Go 钩子）。
func splitSQL(s string) []string {
	var stmts []string
	for _, seg := range strings.Split(s, ";") {
		// 逐行去 -- 注释与空行后仍有内容才算一条语句（简单约定：
		// 语句内不含字符串分号；复杂语句写 Go 迁移钩子）
		var keep []string
		for _, line := range strings.Split(seg, "\n") {
			if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "--") {
				keep = append(keep, line)
			}
		}
		if len(keep) > 0 {
			stmts = append(stmts, strings.Join(keep, "\n"))
		}
	}
	return stmts
}

// hasBusinessTables 库里是否已有业务表（判断新库/存量库）。
func hasBusinessTables(db *gorm.DB) bool {
	var n int64
	db.Table("sqlite_master").Where("type = 'table' AND name NOT LIKE 'sqlite_%' AND name = 'servers'").Count(&n)
	if n > 0 {
		return true
	}
	// MySQL/Postgres：information_schema
	db.Raw(`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'servers'`).Scan(&n)
	return n > 0
}

// Migrate 版本化迁移入口：新库走 AutoMigrate + baseline；存量库跑增量。
// pkg/database 无业务模型知识——models 由 app 层传入（同 Open 现状）。
func Migrate(db *gorm.DB, models []any) error {
	files, err := parseMigrations()
	if err != nil {
		return err
	}
	if err := db.AutoMigrate(&schemaMigration{}); err != nil {
		return fmt.Errorf("建版本表失败: %w", err)
	}
	applied := map[string]bool{}
	var rows []schemaMigration
	db.Find(&rows)
	for _, r := range rows {
		applied[r.Version] = true
	}

	if !hasBusinessTables(db) && len(models) > 0 {
		// 新库：AutoMigrate 全量建表 + baseline（迁移文件视为已含于建表）
		if err := db.AutoMigrate(models...); err != nil {
			return fmt.Errorf("自动迁移失败: %w", err)
		}
		now := time.Now()
		for _, f := range files {
			if !applied[f.Version] {
				db.Create(&schemaMigration{Version: f.Version, AppliedAt: now})
			}
		}
		return nil
	}

	// 存量库：执行未应用的迁移（每版本一个事务）
	for _, f := range files {
		if applied[f.Version] {
			continue
		}
		err := db.Transaction(func(tx *gorm.DB) error {
			for _, stmt := range f.Stmts {
				if err := tx.Exec(stmt).Error; err != nil {
					return fmt.Errorf("版本 %s 语句失败: %w\n%s", f.Version, err, stmt)
				}
			}
			return tx.Create(&schemaMigration{Version: f.Version, AppliedAt: time.Now()}).Error
		})
		if err != nil {
			return err
		}
	}
	return nil
}
