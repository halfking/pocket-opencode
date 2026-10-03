#!/usr/bin/env pwsh
# 启动带 PostgreSQL 的 pocketd（dev 后端，真实数据层）
#
# 为什么必须带 DSN：backend/cmd/pocketd/main.go:70 只在 cfg.PostgresDSN != "" 时
# 初始化 pool；main.go:103 的 `if pool != nil` 决定 taskStore / notesStore /
# flashcards / marketplace / vault / llm-gateway 等全部 store 是否构造。
# 没有 DSN => store 为 nil => 一批端点恒 503，且症状与「代码坏了」几乎一样。
#
# ⚠️ 为什么用 PowerShell 而不是 .cmd（2026-09-30 实测踩坑）：
#   `Start-Process cmd /c xxx.cmd` 这条路径上，环境变量**不稳定地**丢失——
#   同一份脚本出现过 `POCKET_AUTH_LEGACY_ONLY` 生效但
#   `POCKET_POSTGRES_DSN` / `POCKET_DEV_AUTH` 没生效，pocketd 日志打
#     WARN: POCKET_POSTGRES_DSN not set, running in remote-only mode
#   而 503 症状与「没接 PG」完全相同，极易误判成代码问题。
#   直接 Start-Process exe + 先设 $env: 稳定可靠（本脚本采用）。
#
# 用法：
#   Get-Process pocketd-pg -ErrorAction SilentlyContinue | Stop-Process -Force
#   pwsh scripts/start-pocketd-pg.ps1
#   node scripts/backend-endpoint-matrix.mjs      # 期望 18/20
#
# 前置：PostgreSQL 已在 127.0.0.1:5432 运行。免安装步骤见
#   docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md §4.12
# 二进制 logs\pocketd-pg.exe 不入库，需自己 build：
#   cd backend; go build -o ..\logs\pocketd-pg.exe ./cmd/pocketd

$ErrorActionPreference = 'Continue'
$Root = 'C:\workspace\openpocket'
$Exe = Join-Path $Root 'logs\pocketd-pg.exe'

if (-not (Test-Path $Exe)) {
  Write-Error "$Exe 不存在。请先执行：cd backend; go build -o ..\logs\pocketd-pg.exe ./cmd/pocketd"
  exit 1
}

# 先清掉可能残留的旧实例，避免它占住 8088 导致新进程起不来
Get-Process -Name 'pocketd-pg' -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep -Seconds 3

$env:POCKET_POSTGRES_DSN = 'postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable'
$env:POCKET_PG_SCHEMA    = 'opencode_pocket'
$env:POCKET_DEV_AUTH     = 'true'
$env:POCKET_AUTH_LEGACY_ONLY = 'true'
# Pin the data dir explicitly; do NOT rely on inferring it from the CWD.
# Third occurrence of this trap, 2026-10-02.
# dataDir decides which <dataDir>/email_master.key is used. The 5 real mail
# account credentials were encrypted with
# C:\workspace\openpocket\data\email_master.key, while two other, different
# keys also exist on this box (backend\data\ and .scratch-sttdev\data\ --
# see config.go:626-634). With the wrong key the process starts normally,
# healthz answers ok, the scheduler still logs "Email scheduler started",
# and every account on every sync hits
# `decrypt credential: cipher: message authentication failed` -- zero mail.
# The startup self-check prints one ERROR line about this
# (cmd/pocketd/main.go:448-459); do not ignore it.
$env:POCKET_DATA_DIR     = 'C:\workspace\openpocket\data'
# Same class of footgun, second instance: server.go loadVersionConfig() reads
# "config/version.json" through os.ReadFile, i.e. **relative to the process CWD**,
# even though the comment above it claims it is relative to the executable.
# Run from the repo root (which this script does via Set-Location $Root) and the
# file at backend\config\version.json is not found, so every boot logs
# "version config not found ... using defaults" and the app reports a default
# version instead of the real one. Pin the absolute path.
$env:POCKET_VERSION_CONFIG_PATH = 'C:\workspace\openpocket\backend\config\version.json'
# 注意：端口变量名是 POCKET_HTTP_PORT（config.go:210），不是 POCKET_PORT。
# 写错的话会静默用默认 8088，看起来「生效了」其实没设。
$env:POCKET_HTTP_PORT    = '8088'

# LLM gateway (2026-09-30): default endpoint is now https://llm.kxpms.cn/v1
# (backend/internal/opencode/config_writer.go: DefaultLLMGatewayBaseURL).
# Tenant key must never be committed, so it is read from logs\.gateway-key
# (logs/ is already in .gitignore). Precedence: pre-set env > key file > empty.
# ASCII only below on purpose: this file has no UTF-8 BOM, and Windows
# PowerShell 5.1 decodes BOM-less files as ANSI/GBK, which mangles the CJK
# bytes in comments and can break quote pairing -> ParserError.
$KeyFile = Join-Path $Root 'logs\.gateway-key'
if ($env:POCKET_LLM_GATEWAY_API_KEY) {
  $keySource = 'env'
} elseif (Test-Path $KeyFile) {
  $env:POCKET_LLM_GATEWAY_API_KEY = (Get-Content -Raw -Path $KeyFile).Trim()
  $keySource = 'file'
} else {
  $keySource = 'missing'
}
$keyLen = $env:POCKET_LLM_GATEWAY_API_KEY.Length
Write-Host "gateway key source = $keySource (len=$keyLen)"
if ($env:POCKET_LLM_GATEWAY_URL) {
  $gwBase = $env:POCKET_LLM_GATEWAY_URL
} else {
  $gwBase = 'https://llm.kxpms.cn/v1 (code default)'
}
Write-Host "gateway base = $gwBase"

Write-Host "DSN    = $env:POCKET_POSTGRES_DSN"
Write-Host "schema = $env:POCKET_PG_SCHEMA"
Write-Host "启动后必须看到这两行，否则说明 DSN 没进去（503 症状会伪装成代码问题）："
Write-Host "  Postgres pool initialized (schema=`"opencode_pocket`")"
Write-Host "  Module stores initialized (PG, scheduled tasks and marketplace enabled)"

Set-Location $Root
Start-Process -FilePath $Exe -WindowStyle Hidden `
  -RedirectStandardOutput (Join-Path $Root 'logs\pocketd-pg.out.log') `
  -RedirectStandardError  (Join-Path $Root 'logs\pocketd-pg.err.log')

Start-Sleep -Seconds 12
$log = Join-Path $Root 'logs\pocketd-pg.err.log'
if (Select-String -Path $log -Pattern 'Postgres pool initialized' -Quiet -ErrorAction SilentlyContinue) {
  Write-Host "[OK] Postgres pool 已初始化" -ForegroundColor Green
} else {
  Write-Warning "[FAIL] 日志里没有 'Postgres pool initialized'；检查 remote-only 警告"
  Select-String -Path $log -Pattern 'remote-only|Postgres pool' -ErrorAction SilentlyContinue |
    ForEach-Object { Write-Host "    $($_.Line)" }
  exit 2
}
# Second gate: the email credential self-check. Without it a wrong data dir
# stays silent: healthz is ok, every endpoint answers, and no mail arrives.
# Cost me a full round on 2026-10-02 before anyone noticed. The process
# already prints its own verdict (cmd/pocketd/main.go:448-459); this turns
# that line into a non-zero exit code.
# The predicate is "the success line is present", NOT "no ERROR line": when
# the email module is switched off no self-check runs at all, and reading
# that silence as "check passed" would be exactly the bug this gate exists
# to catch.
if (Select-String -Path $log -Pattern 'Email credential self-check' -Quiet -ErrorAction SilentlyContinue) {
  Write-Host "[OK] email credential self-check passed (current dataDir master key decrypts the real DB credentials)" -ForegroundColor Green
} elseif (Select-String -Path $log -Pattern 'MASTER KEY LOOKS WRONG' -Quiet -ErrorAction SilentlyContinue) {
  Write-Warning "[FAIL] wrong email master key: none of the real DB credentials decrypt, no mail account will sync."
  Write-Warning "       the working key is C:\workspace\openpocket\data\email_master.key"
  Write-Warning "       POCKET_DATA_DIR must point at C:\workspace\openpocket\data (see comment above)."
  Select-String -Path $log -Pattern 'MASTER KEY LOOKS WRONG|email master key' -ErrorAction SilentlyContinue |
    ForEach-Object { Write-Host "    $($_.Line)" }
  exit 4
} else {
  Write-Host "[--] no email credential self-check line in this boot (email module may be off); gate skipped" -ForegroundColor Yellow
}
try {
  $hz = (Invoke-WebRequest -Uri 'http://localhost:8088/healthz' -TimeoutSec 8).Content
  Write-Host "[OK] healthz = $hz" -ForegroundColor Green
} catch {
  Write-Warning "[FAIL] healthz 不可达：$($_.Exception.Message)"
  exit 3
}

# Gateway smoke check: the endpoint pocketd actually resolves must be the
# one we intend. Without this, "configured wrong" and "gateway unreachable"
# look identical from the app.
try {
  $gw = (Invoke-RestMethod -Uri 'http://localhost:8088/api/llm-gateway/config' -TimeoutSec 8)
  Write-Host "[GW] baseURL = $($gw.baseURL)" -ForegroundColor Cyan
  Write-Host "[GW] preferredModels = $($gw.preferredModels -join ',')" -ForegroundColor Cyan
} catch {
  Write-Warning "[GW] GET /api/llm-gateway/config failed: $($_.Exception.Message)"
}
