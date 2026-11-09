-- 0012 项目备注 + 构建外部定位（2026-11-09 维护期批次 12）：
-- projects.remark 备注（纯展示）；builds.ci_ref 外部构建定位 job#number
--（test 记录由 CI 回调携带 BUILD_NUMBER，构建抽屉据此直拉 Jenkins 日志）。
-- 双方言 ADD COLUMN 均无 IF NOT EXISTS（SQLite/老 MySQL），列已由当前模型
-- AutoMigrate 带上的库会重复执行——统一走 app 层守卫式 Go 钩子（addColIfMissing），
-- 本文件仅作版本链占位，无 DDL。
SELECT 1;
