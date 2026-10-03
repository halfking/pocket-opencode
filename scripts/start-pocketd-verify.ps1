$ErrorActionPreference = 'Continue'
# Isolated instance used only to verify the syncGatewayUserSetting fix.
# Runs on :8099 so it never touches the :8088 service another actor is running.
# Scheduler is disabled so the two instances don't both fire scheduled tasks.
# ASCII only on purpose: this file has no UTF-8 BOM, and Windows PowerShell 5.1
# decodes BOM-less files as ANSI/GBK, which mangles CJK bytes and can corrupt
# the following lines (it previously nulled $Root here).
$ErrorActionPreference = 'Continue'
$repo = 'C:\workspace\openpocket'
$exe = 'C:\workspace\openpocket\logs\pocketd-pg.exe'
$port = 8099
$keyFile = 'C:\workspace\openpocket\logs\.gateway-key'

$env:POCKET_POSTGRES_DSN = 'postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable'
$env:POCKET_PG_SCHEMA    = 'opencode_pocket'
$env:POCKET_DEV_AUTH     = 'true'
$env:POCKET_AUTH_LEGACY_ONLY = 'true'
$env:POCKET_HTTP_PORT    = "$port"
$env:POCKET_SCHEDULER_ENABLED = 'false'
# Separate data dir so this instance's email-body cache files never collide
# with the :8088 instance's (they use the same DB rows).
$env:POCKET_DATA_DIR     = 'C:\workspace\openpocket\logs\verify-data'
New-Item -ItemType Directory -Force -Path $env:POCKET_DATA_DIR | Out-Null
$env:POCKET_LLM_GATEWAY_API_KEY = (Get-Content -Raw -Path $keyFile).Trim()

Set-Location $repo
Start-Process -FilePath $exe -WindowStyle Hidden `
  -RedirectStandardOutput 'C:\workspace\openpocket\logs\pocketd-verify.out.log' `
  -RedirectStandardError  'C:\workspace\openpocket\logs\pocketd-verify.err.log'

Start-Sleep -Seconds 12
try {
  $hz = (Invoke-WebRequest -Uri "http://127.0.0.1:$port/healthz" -TimeoutSec 8).Content
  Write-Host "[OK] verify instance healthz = $hz on :$port" -ForegroundColor Green
} catch {
  Write-Warning "[FAIL] port $port not reachable: $($_.Exception.Message)"
  Get-Content 'C:\workspace\openpocket\logs\pocketd-verify.err.log' -Tail 8
  exit 3
}
