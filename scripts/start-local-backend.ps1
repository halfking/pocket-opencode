# start-local-backend.ps1 - start this worktree's pocketd on 18099 (the API base the App is configured with).
#
# WHY THIS EXISTS (observed 2026-10-01 12:45 on a real device):
#   The App's API base is http://127.0.0.1:18099 (see the login page's
#   "backend server" line). That instance was GONE while 18111 was serving a
#   pocketd built from a DIFFERENT worktree
#   (C:\workspace\openpocket-wt-stt\backend\.verify-bin\pocketd.exe).
#   The result was extremely misleading:
#     * PostgreSQL still had all 17 tasks
#     * The App's task board showed "running 0 / all clear" with NO error at all
#   Easy to misread as "the list feature is broken / BUG-AL regressed".
#   It was just a dead backend. => Check 18099 BEFORE every real-device run;
#   it is a precondition, not a nicety.
#
# The script is deliberately ASCII-only: PowerShell 5.1 decodes a BOM-less
# .ps1 as ANSI, so UTF-8 Chinese comments get mangled and can break string
# quoting (it actually did - see the stray-quote parse error). Keep it ASCII.
# The dev password is read from the environment at runtime, never from the Go
# source and never from this file, so no plaintext credential lands in a repo
# file. See the block further down for why there is no fallback.
#
# Usage:
#   powershell -ExecutionPolicy Bypass -File scripts\start-local-backend.ps1
#   powershell -ExecutionPolicy Bypass -File scripts\start-local-backend.ps1 -Port 18099 -Schema opencode_pocket
param(
  [int]$Port = 18099,
  [string]$Schema = "opencode_pocket",
  [string]$DataDir = "",
  [string]$JwtSecret = "pocket-local-dev-jwt-secret-do-not-use-in-shared-env"
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
# DataDir defaults to backend\data, NOT logs\pocketd-data.
#
# 2026-10-01: POCKET_DATA_DIR used to be inert on the backend (it was only read
# by loadCompanionOverlay to find companion.env), so the real data directory was
# always filepath.Dir(POCKET_DB_PATH) = Dir("./data/pocket.sqlite") resolved
# against -WorkingDirectory, i.e. <root>\backend\data -- where the live
# chat_agents.sqlite and email_master.key actually live. POCKET_DATA_DIR now
# decides it for real (config.ResolveDataDir), so keeping the logs\ default
# would have silently moved this instance to an empty directory: a fresh
# email_master.key would be generated and every account would fail with
# "decrypt credential: cipher: message authentication failed".
#
# The old default was simply never in effect, so aligning it with reality
# changes nothing for existing instances and makes the two env vars agree.
if (-not $DataDir) { $DataDir = Join-Path $root "backend\data" }
New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
$bin = Join-Path $root "backend\.verify-bin\pocketd.exe"

if (-not (Test-Path $bin)) {
  Write-Host "[backend] building pocketd ..."
  Push-Location (Join-Path $root "backend")
  go build -o $bin ./cmd/pocketd
  if ($LASTEXITCODE -ne 0) { Pop-Location; throw "backend build failed" }
  Pop-Location
}

# Dev password: read from the caller's environment, NEVER from the Go source.
#
# 2026-10-03: this used to scrape a hardcoded 'devPass = "..."' constant out of
# server_assistant.go. The security remediation that removed that constant (the
# password sat in plaintext in 8 tracked files, so "dev mode" was really "an
# admin bypass guarded by a public password") left this script behind. Result:
# the script threw "could not read devPass constant" and the local backend could
# not be started AT ALL. maestro-run.mjs ensureBackend() calls this script, so
# the whole real-device rig lost its self-healing path - and it failed in a way
# that looked like "the backend is broken", not "the launcher is broken".
#
# Single source of truth is now the environment:
#   POCKET_AUTH_PASS - what the backend actually reads (cfg.DevAuthPass)
#   POCKET_DEV_PASS  - the name maestro-run.mjs uses; accepted as an alias
# Both must be the same value: the App logs in over HTTP with this password, so
# a mismatch is indistinguishable from a broken login path.
#
# Intentionally NO fallback and NO default. devBypassCredentials() refuses to
# run without an explicit password; this script must not be the thing that
# quietly puts one back.
$devPass = $env:POCKET_AUTH_PASS
if (-not $devPass) { $devPass = $env:POCKET_DEV_PASS }
if (-not $devPass) {
  Write-Host "[backend] POCKET_AUTH_PASS is not set."
  Write-Host "[backend] The dev auth bypass refuses to run without an explicit password"
  Write-Host "[backend] (see devBypassCredentials in backend/internal/server/server_assistant.go)."
  Write-Host "[backend] Set it for this shell, then re-run, e.g.:"
  Write-Host '[backend]   $env:POCKET_AUTH_PASS = "<your local dev password>"'
  exit 1
}

# Refuse to double-bind: two pocketd on one port means the App may randomly
# talk to the stale one, which is exactly the confusion described above.
$existing = Get-NetTCPConnection -State Listen -LocalPort $Port -ErrorAction SilentlyContinue
if ($existing) {
  Write-Host "[backend] port $Port held by pid $($existing.OwningProcess), stopping it first"
  Stop-Process -Id $existing.OwningProcess -Force -ErrorAction SilentlyContinue
  Start-Sleep -Seconds 2
}

$env:POCKET_POSTGRES_DSN = "postgresql://postgres@127.0.0.1:5432/postgres?sslmode=disable"
$env:POCKET_PG_SCHEMA   = $Schema
$env:POCKET_HTTP_PORT   = "$Port"
$env:POCKET_DATA_DIR    = $DataDir
# Keep POCKET_DB_PATH inside the same directory. It no longer opens a SQLite
# file (Postgres is the store); config.ResolveDataDir falls back to
# Dir(DBPath) whenever POCKET_DATA_DIR is unset, so pointing both at the same
# place means the instance stays self-consistent no matter which one wins.
# This is the fix for "dataDir depends on the directory you launched from",
# which showed up as invoice 404s and A4 export 400s (see handoff 2026-10-01
# section 2.3).
$env:POCKET_DB_PATH     = Join-Path $DataDir "pocket.db"
$env:POCKET_DEV_AUTH    = "true"
# Without this the process refuses to start:
#   "POCKET_REDCLAW_ADMIN_URL must be set (or set POCKET_AUTH_LEGACY_ONLY=true
#    for dev-only fallback)"
# Dev-only: it means "no RedClaw identity provider", which is what the local
# device test rig wants. Never set it on a shared/production backend.
$env:POCKET_AUTH_LEGACY_ONLY = "true"
$env:POCKET_AUTH_USER   = "admin"
$env:POCKET_AUTH_PASS   = $devPass
# ⚠️ JWT secret 必须是**固定值**，不能用随机数。
#    2026-10-01 13:15~13:30 踩出来的死循环：每次重启后端都换新 secret ⇒
#    设备上已签发的 token 全部作废 ⇒ App 拿到 401 ⇒ 任务列表空 ⇒
#    「点创建没反应」⇒ 看起来像 tasks 写路径坏了。真因是环境。
#    固定 secret 后，重启后端不再销毁登录态，回归才能反复跑。
#    仅限本机 dev 后端；共享/生产环境绝不能用固定 secret。
#    -JwtSecret 存在的理由：BUG-AX 真机回归要复现的正是「后端换 secret ⇒ 旧 token
#    全 401」这个场景，没有可切换的 secret 就只能靠伪造 token，而伪造 token 在
#    启动期就被 /api/auth/refresh 的 401 清掉了，压根到不了 /api/tasks。
$env:POCKET_JWT_SECRET  = $JwtSecret
$env:POCKET_LLM_GATEWAY_ALLOW_PRIVATE = "true"

# 日志文件名带时间戳：固定名会被上一个（刚 Stop-Process 但句柄尚未释放的）
# 实例占着，Start-Process -RedirectStandardError 直接抛
# "The process cannot access the file because it is being used by another process."
# 踩过一次，表现为「后端起不来」但其实只是日志文件锁。
$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$out = Join-Path $root "logs\pocketd-$Port-$stamp.out.log"
$err = Join-Path $root "logs\pocketd-$Port-$stamp.err.log"
Start-Sleep -Milliseconds 800
$p = Start-Process -FilePath $bin -WorkingDirectory (Join-Path $root "backend") `
  -RedirectStandardOutput $out -RedirectStandardError $err -PassThru -WindowStyle Hidden

# Poll /healthz. "Process exists" is NOT "backend is usable" - wait for the
# port to actually answer before telling anyone it is ready.
$ok = $false
for ($i = 0; $i -lt 60 -and -not $ok; $i++) {
  Start-Sleep -Milliseconds 500
  try {
    $r = Invoke-WebRequest -Uri "http://127.0.0.1:$Port/healthz" -TimeoutSec 3 -UseBasicParsing
    if ($r.StatusCode -eq 200) { $ok = $true }
  } catch { # not up yet
  }
}
if ($ok) {
  Write-Host "[backend] pid=$($p.Id) ready on $Port, schema=$Schema"
} else {
  Write-Host "[backend] FAILED: /healthz unanswered after 30s. stderr tail:"
  Get-Content $err -Tail 20
  exit 1
}
