# 部署测试汇总（2026-09-03）

## 测试统计

| 套件                       | PASS | FAIL |
|----------------------------|------|------|
| test_os_detect.sh          |   20 |    0 |
| test_init_dirs.sh          |   38 |    0 |
| test_blue_green.sh         |   12 |    0 |
| test_database_detect.sh    |    7 |    0 |
| deploy-integration-test.sh |   27 |    0 |
| **合计**                   | **104** | **0** |

## 文件

| 文件 | 大小 | 说明 |
|------|------|------|
| DEPLOYMENT_PLAN.md | 10 KB | 完整部署方案（OS 派生、子目录、blue-green、DB 复用、风险与回滚） |
| run-all-output.log | 16 KB | run-all.sh 完整输出 |
| test_*.log | 4 个 | 单套单元测试输出 |
| deploy-integration-test.log | 58 KB | 集成 dry-run 完整输出 |

## 一键复跑

```bash
bash deploy/bin/tests/run-all.sh
```

## 关键决策（与用户需求对齐）

1. **OS 感知目录**：macOS=`~/kaixuan/openpocket`、Linux=`/opt/kaixuan/openpocket`、Windows=D:/>C:/
2. **子目录齐全**：attachments/ bin/ backups/ logs/ raw-logs/ run/ + data/ config/ images/（始终）+ postgres/ redis/ mysql/（条件）
3. **DB 复用优先**：detect 命中外部实例（docker / systemd / 端口）→ 直接复用；未命中 + OPP_DEPLOY_*=true → 容器化；其余 → remote-required（DSN 由 .env 注入）
4. **blue-green**：bin/{version}.{build}/ + bin/current 符号链接；失败自动回滚到上一个 verified 版本
5. **三个入口**：deploy-local.sh / deploy-154.sh / deploy-245.sh，对应 macOS / 154 Linux 生产 / 245 Linux 生产
6. **154 / 245 同构不同配**：154=8090/4175 @ 172.16.2.154、245=8091/4176 @ 172.16.2.245
7. **252 集中**：154 / 245 都连 252 内网 PG（172.16.2.210:5432），本机不起 PG
