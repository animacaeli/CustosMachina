// migrate_0011.go 配置文件环境语义返工的存量数据重写（版本 0011 Go 钩子，
// 决策与规则见 app.go init 内注释与 docs）。规则矩阵：
//
//	prod/...           → 不动
//	裸文件（无层级）    → 不动（旧形态兼容，reveal 按 prod 最严处理）
//	test/devN/...      → devN/...
//	test/...（其余）    → dev1/...（测试族默认槽位）
//	canary/...          内容=对应 prod 或自身无内容 → 删除（读侧引用 prod）
//	                     prod 无对应 / 内容有异 → 中止报错（人工定夺）
//	其他首段           → 中止报错（人工核对）
//	sensitive 列        → 全量清零（已废弃）
package app

import (
	"fmt"
	"regexp"
	"strings"

	"gorm.io/gorm"
)

var m0011SlotRe = regexp.MustCompile(`^dev[1-9][0-9]*$`)

func migrateConfigEnvSemantics(db *gorm.DB) error {
	if !db.Migrator().HasTable("config_files") {
		return nil // 跳级升级无此表：由模型补齐建出，无存量可迁移
	}
	if !db.Migrator().HasTable("config_versions") {
		return nil // 理论不会单表存在（同版本引入）；守卫防跳级异常形态炸迁移
	}
	type cfgRow struct {
		ID        uint
		ProjectID uint
		RelPath   string
		Content   string
	}
	var rows []cfgRow
	if err := db.Table("config_files").
		Select("id, project_id, rel_path, content").Find(&rows).Error; err != nil {
		return err
	}
	byKey := map[string]cfgRow{} // "projectID|relPath" → prod 行（canary 对比用）
	for _, r := range rows {
		if firstSegOf(r.RelPath) == "prod" {
			byKey[fmt.Sprint(r.ProjectID)+"|"+r.RelPath] = r
		}
	}
	for _, r := range rows {
		segs := strings.Split(strings.Trim(r.RelPath, "/"), "/")
		if len(segs) == 0 || segs[0] == "" || segs[0] == "prod" {
			continue // 裸文件（旧形态）/ prod：不动
		}
		switch {
		case segs[0] == "test":
			// test/devN/... → devN/...；其余（含直接文件）→ dev1/...
			var dst string
			if len(segs) >= 2 && m0011SlotRe.MatchString(segs[1]) {
				dst = strings.Join(segs[1:], "/")
			} else {
				dst = "dev1/" + strings.Join(segs[1:], "/")
			}
			if err := db.Exec("UPDATE config_files SET rel_path = ? WHERE id = ?", dst, r.ID).Error; err != nil {
				return fmt.Errorf("重写 %s → %s 失败: %w", r.RelPath, dst, err)
			}
		case segs[0] == "canary":
			rest := strings.Join(segs[1:], "/")
			prod, ok := byKey[fmt.Sprint(r.ProjectID)+"|prod/"+rest]
			if r.Content == "" {
				ok = true // 无内容即无信息，直接删
			}
			if !ok {
				return fmt.Errorf("迁移中止：canary 配置 %q（project=%d）在 prod 下无对应文件——灰度已改为全面引用 prod，请先合并或删除该文件", r.RelPath, r.ProjectID)
			}
			if r.Content != "" && prod.Content != r.Content {
				return fmt.Errorf("迁移中止：canary 配置 %q（project=%d）与 prod 内容不一致——请先对齐（环境同步）或删除", r.RelPath, r.ProjectID)
			}
			if err := db.Exec("DELETE FROM config_versions WHERE file_id = ?", r.ID).Error; err != nil {
				return err
			}
			if err := db.Exec("DELETE FROM config_files WHERE id = ?", r.ID).Error; err != nil {
				return err
			}
		default:
			return fmt.Errorf("迁移中止：配置 %q（project=%d）首段 %q 既非 prod/test/canary 旧枚举也非槽位名——请人工核对", r.RelPath, r.ProjectID, segs[0])
		}
	}
	return db.Exec("UPDATE config_files SET sensitive = 0").Error
}

func firstSegOf(relPath string) string {
	return strings.SplitN(strings.Trim(relPath, "/"), "/", 2)[0]
}
