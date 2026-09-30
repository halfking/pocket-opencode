$ErrorActionPreference = 'Continue'
# Isolated instance for verifying the email-classify gateway fallback.
# Port 8098: 8088 is the shared service, 8099 is taken by another actor's
# pocketd-email-v1.exe, so this one takes a port of its own.
# Scheduler off, separate data dir, separate logs: it must not disturb anyone.
# ASCII only on purpose: this file has no UTF-8 BOM, and Windows PowerShell 5.1
# decodes BOM-less files as ANSI/GBK, which mangles CJK bytes and can corrupt
# the following lines (it previously nulled $Root in the sibling script).
$repo = 'C:\workspace\openpocket'
$exe = 'C:\workspace\openpocket\logs\pocketd-cls.exe'
$port = 8098
$keyFile = 'C:\workspace\openpocket\logs\.gateway-key'

$env:POCKET_POSTGRES_DSN = 'postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable'
$env:POCKET_PG_SCHEMA    = 'opencode_pocket'
$env:POCKET_DEV_AUTH     = 'true'
$env:POCKET_AUTH_LEGACY_ONLY = 'true'
$env:POCKET_HTTP_PORT    = "$port"
$env:POCKET_SCHEDULER_ENABLED = 'false'
$env:POCKET_DATA_DIR     = 'C:\workspace\openpocket\logs\cls-data'
New-Item -ItemType Directory -Force -Path $env:POCKET_DATA_DIR | Out-Null
$env:POCKET_LLM_GATEWAY_API_KEY = (Get-Content -Raw -Path $keyFile).Trim()

Set-Location $repo
Start-Process -FilePath $exe -WindowStyle Hidden `
  -RedirectStandardOutput 'C:\workspace\openpocket\logs\pocketd-cls.out.log' `
  -RedirectStandardError  'C:\workspace\openpocket\logs\pocketd-cls.err.log'

Start-Sleep -Seconds 12
try {
  $hz = (Invoke-WebRequest -Uri "http://127.0.0.1:$port/healthz" -TimeoutSec 8).Content
  Write-Host "[OK] classifier-verify instance healthz = $hz on :$port"
} catch {
  Write-Warning "[FAIL] port $port not reachable: $($_.Exception.Message)"
  Get-Content 'C:\workspace\openpocket\logs\pocketd-cls.err.log' -Tail 8
  exit 3
}
