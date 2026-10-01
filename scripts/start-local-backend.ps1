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
# The dev password is read from the Go source at runtime instead of being
# written here, so no plaintext credential lands in a repo file.
#
# Usage:
#   powershell -ExecutionPolicy Bypass -File scripts\start-local-backend.ps1
#   powershell -ExecutionPolicy Bypass -File scripts\start-local-backend.ps1 -Port 18099 -Schema opencode_pocket
param(
  [int]$Port = 18099,
  [string]$Schema = "opencode_pocket",
  [string]$DataDir = ""
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
if (-not $DataDir) { $DataDir = Join-Path $root "logs\pocketd-data" }
New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
$bin = Join-Path $root "backend\.verify-bin\pocketd.exe"
$goSrc = Join-Path $root "backend\internal\server\server_assistant.go"

if (-not (Test-Path $bin)) {
  Write-Host "[backend] building pocketd ..."
  Push-Location (Join-Path $root "backend")
  go build -o $bin ./cmd/pocketd
  if ($LASTEXITCODE -ne 0) { Pop-Location; throw "backend build failed" }
  Pop-Location
}

# Dev password: single source of truth is the Go constant. maestro-run.mjs
# reads the same one, so the App and the backend cannot drift apart.
$devPass = $null
foreach ($line in (Get-Content $goSrc)) {
  if ($line -match 'devPass\s*=\s*"([^"]+)"') { $devPass = $Matches[1]; break }
}
if (-not $devPass) { throw "could not read devPass constant from $goSrc" }

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
$env:POCKET_JWT_SECRET  = "pocket-local-dev-jwt-secret-do-not-use-in-shared-env"
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
