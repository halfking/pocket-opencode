@echo off
rem ---------------------------------------------------------------------------
rem 启动带 PostgreSQL 的 pocketd（dev 后端，真实数据层）
rem
rem 为什么必须带 DSN：backend/cmd/pocketd/main.go:70 只有在 cfg.PostgresDSN != ""
rem 时才初始化 pool；main.go:103 的 `if pool != nil` 决定 taskStore / notesStore /
rem flashcards / marketplace / vault / llm-gateway 等全部 store 是否构造。
rem 没有 DSN => store 为 nil => 一批端点恒 503
rem   POST /api/tasks        503 local task store not configured (remote-only mode)
rem   POST /api/notes        503
rem   GET  /api/flashcards   503
rem   GET  /api/llm/usage    503
rem   GET  /api/llm-gateway/nodes  503 requires PostgreSQL
rem 接上之后端点可用性 18/20（见 scripts/backend-endpoint-matrix.mjs）。
rem
rem 前置：PostgreSQL 已在 127.0.0.1:5432 运行。免安装步骤见
rem   docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md §4.12
rem
rem 二进制 logs\pocketd-pg.exe 不入库，需自己 build：
rem   cd backend
rem   go build -o ..\logs\pocketd-pg.exe ./cmd/pocketd
rem
rem 用法：
rem   taskkill /IM pocketd-new.exe /F
rem   scripts\start-pocketd-pg.cmd
rem ---------------------------------------------------------------------------
setlocal

rem ⚠️ 踩坑记录（2026-09-30）：这五行必须原样保留在 pocketd-pg.exe 启动之前。
rem    曾出现「POCKET_AUTH_LEGACY_ONLY 生效但 POCKET_POSTGRES_DSN 没生效」的情况，
rem    导致 pocketd 以 remote-only 模式启动、日志打
rem    `WARN: POCKET_POSTGRES_DSN not set, running in remote-only mode`，
rem    而 503 症状与「没接 PG」完全一样，极易误判成代码问题。
rem    自检方法：启动后看日志有没有 `Postgres pool initialized (schema=...)`。
set "POCKET_POSTGRES_DSN=postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable"
set "POCKET_PG_SCHEMA=opencode_pocket"
set "POCKET_DEV_AUTH=true"
set "POCKET_AUTH_LEGACY_ONLY=true"
set "POCKET_PORT=8088"

if not exist "C:\workspace\openpocket\logs\pocketd-pg.exe" (
  echo [ERROR] logs\pocketd-pg.exe 不存在，请先执行：
  echo    cd backend ^&^& go build -o ..\logs\pocketd-pg.exe ./cmd/pocketd
  exit /b 1
)

echo DSN    = %POCKET_POSTGRES_DSN%
echo schema = %POCKET_PG_SCHEMA%
echo 启动后应看到：
echo    Postgres pool initialized (schema="opencode_pocket")
echo    Module stores initialized (PG, scheduled tasks and marketplace enabled)
echo.
cd /d C:\workspace\openpocket
C:\workspace\openpocket\logs\pocketd-pg.exe
endlocal
