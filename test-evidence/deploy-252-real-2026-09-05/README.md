本地两连部署 blue-green 验证 2026-09-05 01:13:40
DEPLOY #1: stage+bootstrap switch, last_healthy_at 落盘
DEPLOY #2: stage 新版本 → 健康通过 → bin/current 前移 → previous 链建立
回归: 104/104 PASS (run-all.sh)
