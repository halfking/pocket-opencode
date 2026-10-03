$ErrorActionPreference = 'Continue'
# Backend for host-side UI inspection of the settings page (browser stand-in for
# the blocked real device). Runs on :8096, never touches :8088.
# ASCII only: no UTF-8 BOM (PowerShell 5.1 decodes BOM-less files as ANSI/GBK).
$repo = 'C:\workspace\openpocket'
$exe  = 'C:\workspace\openpocket\logs\pocketd-authfix.exe'
$port = 8096

$env:POCKET_POSTGRES_DSN      = 'postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable'
$env:POCKET_PG_SCHEMA         = 'opencode_pocket'
$env:POCKET_DEV_AUTH          = 'true'
$env:POCKET_AUTH_LEGACY_ONLY  = 'true'
$env:POCKET_HTTP_PORT         = "$port"
$env:POCKET_SCHEDULER_ENABLED = 'false'
$env:POCKET_DATA_DIR          = 'C:\workspace\openpocket\logs\ui-inspect-data'
New-Item -ItemType Directory -Force -Path $env:POCKET_DATA_DIR | Out-Null

Set-Location $repo
Start-Process -FilePath $exe -WindowStyle Hidden `
  -RedirectStandardOutput 'C:\workspace\openpocket\logs\pocketd-ui.out.log' `
  -RedirectStandardError  'C:\workspace\openpocket\logs\pocketd-ui.err.log'

Start-Sleep -Seconds 12
try {
  $hz = (Invoke-WebRequest -Uri "http://127.0.0.1:$port/healthz" -TimeoutSec 8).Content
  Write-Host "[OK] ui-inspect backend healthz = $hz on :$port" -ForegroundColor Green
} catch {
  Write-Warning "[FAIL] port $port not reachable: $($_.Exception.Message)"
  Get-Content 'C:\workspace\openpocket\logs\pocketd-ui.err.log' -Tail 20
  exit 3
}
