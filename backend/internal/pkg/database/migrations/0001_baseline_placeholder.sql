-- 版本化 migration 基线占位（P6-M6 #9）：
-- 001 之前的历史 schema 变更全部经由 AutoMigrate 完成，无增量需补——
-- 本文件仅建立版本链起点。后续 schema 变更（删列/改类型/数据回填等
-- AutoMigrate 覆盖不了的操作）一律新增 NNNN_描述.sql，只写
-- SQLite/MySQL 双方言兼容的 DDL。
SELECT 1;
