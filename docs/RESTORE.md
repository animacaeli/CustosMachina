# 备份与恢复手册（P5 M2）

> 配套代码：`backend/internal/modules/backup`；恢复脚本：`deploy/restore.sh`。
> 计划：docs/plan-phase5-services.md M2。产物格式与口令加密格式均为"空机可恢复"设计——
> 恢复只依赖 `openssl` + `docker`，不需要平台二进制。

## 1. 备份任务类型

| 类型 | 备份内容 | 目标 | 说明 |
|---|---|---|---|
| `platform_self` | 平台 SQLite 库 + 数据目录（排除 backups/ 与 logs/）+ **口令加密的密钥份额** | local / S3 | 平台自身容灾；**必须配置备份口令**（用于加密 MASTER KEY，恢复时解出） |
| `remote_dir` | 远端主机任意目录（SSH `tar` 打包回拉） | local / S3 | 业务机数据目录备份 |
| `remote_db` | （暂缓）MySQL 逻辑备份 | — | 出现第一台真实 MySQL 业务机时补齐 |

产物结构（tar.gz）：

```
data/...              # platform_self：数据目录全量（含 custos.db）
remote/...            # remote_dir：远端目录内容
keyshare.enc          # platform_self：口令加密的 {"masterKey":...}
                      #   格式 = openssl enc -aes-256-cbc -pbkdf2
manifest.json         # 类型/时间/版本/条目数
```

## 2. 调度与保留

- 调度表达式：标准 5 段 crontab；**留空 = 仅手动**。错失不补跑（对齐 cron 模块语义）。
- 并发保护：同任务上轮未结束则跳过本触发点。
- 保留策略：每个任务独立保留数（默认 7），超限自动删最旧（产物名含 UTC 时间戳，字典序即时间序）。
- 运行历史：`backup_runs` 留痕 90 天，输出含产物名/体积/清理数。
- 失败通知：走统一通知路由（事件源 `backup_failed`，warn 级）——在管理后台「通知路由」配置目标群。

## 3. 存储

- **local**：产物落平台数据目录之外的自定义目录（如挂载的宿主盘）。注意：目录若在 `/data` 内会被自备份排除规则跳过（防递归）。
- **S3**：S3 兼容对象存储直连（MinIO / 云 OSS / 阿里 OSS 的 S3 兼容端点）。SecretKey 用平台主密钥加密落库。

## 4. 恢复演练（验收标准之一）

空机恢复三步（详见 `deploy/restore.sh` 头部注释）：

```bash
./deploy/restore.sh custos-backup-20261002T093000Z.tar.gz '备份口令' ./custos-data
# 脚本解出 MASTER KEY → 恢复数据目录 → 打印 docker run 启动命令
```

验收记录（2026-10-02 本地演练）：

1. 创建 `platform_self` 任务（local 存储，保留 3），手动触发 → 产物落盘；
2. 备份库与数据目录 → 删除 `custos.db` 模拟灾难 → `restore.sh` 恢复；
3. 用恢复后的 DB + 原 MASTER KEY 启动平台 → root 登录成功、用户/通知群/路由规则数据完整；
4. 连续触发 4 次备份（保留 3）→ 最旧产物被自动清理。

## 5. 注意事项

- **备份口令丢失 = 密钥份额不可解**：MASTER KEY 若未在其他地方保管，平台数据将无法解密（IM webhook 等密文全靠它）。请在密码管理器单独保存口令。
- MASTER KEY 本身仍应按原途径（部署 env）另行保管——备份口令是第二保险，不是替代。
- 服务器部署下请确认宿主 nginx 配置了 `X-Forwarded-For`（M1 可信代理依赖，见 plan-phase5 M1）。
- SQLite 文件以"拷贝快照"方式备份：极小概率撞上写入中的事务（ WAL 模式下拷贝页文件仍可用）。
  生产建议：备份任务安排在业务低峰（凌晨），或先 `VACUUM INTO`（演进项）。
