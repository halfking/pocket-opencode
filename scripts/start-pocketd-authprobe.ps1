$ErrorActionPreference = 'Continue'
# Isolated instance for the forgot-password end-to-end probe only.
# Runs on :8098 so it never touches :8088. Scheduler disabled so it does not
# fire scheduled tasks. Separate data dir so email-body cache files do not
# collide with the other instance's (they share DB rows).
# ASCII only: this file has no UTF-8 BOM, and Windows PowerShell 5.1 decodes
# BOM-less files as ANSI/GBK, which mangles CJK bytes.
$repo  = 'C:\workspace\openpocket'
$exe   = 'C:\workspace\openpocket\logs\pocketd-authfix.exe'
$port  = 8098

$env:POCKET_POSTGRES_DSN      = 'postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable'
$env:POCKET_PG_SCHEMA         = 'opencode_pocket'
$env:POCKET_DEV_AUTH          = 'true'
$env:POCKET_AUTH_LEGACY_ONLY  = 'true'
$env:POCKET_HTTP_PORT         = "$port"
$env:POCKET_SCHEDULER_ENABLED = 'false'
# No POCKET_SMTP_HOST => notify.NewClient returns nil => smtpClient nil.
# Combined with DEBUG_ECHO, send-code returns the code in the response body,
# which is what lets the probe drive the flow without a mail server.
$env:POCKET_SMTP_DEBUG_ECHO   = 'true'
$env:POCKET_DATA_DIR          = 'C:\workspace\openpocket\logs\authprobe-data'
New-Item -ItemType Directory -Force -Path $env:POCKET_DATA_DIR | Out-Null

Set-Location $repo
Start-Process -FilePath $exe -WindowStyle Hidden `
  -RedirectStandardOutput 'C:\workspace\openpocket\logs\pocketd-authprobe.out.log' `
  -RedirectStandardError  'C:\workspace\openpocket\logs\pocketd-authprobe.err.log'

Start-Sleep -Seconds 12
try {
  $hz = (Invoke-WebRequest -Uri "http://127.0.0.1:$port/healthz" -TimeoutSec 8).Content
  Write-Host "[OK] authprobe instance healthz = $hz on :$port" -ForegroundColor Green
} catch {
  Write-Warning "[FAIL] port $port not reachable: $($_.Exception.Message)"
  Get-Content 'C:\workspace\openpocket\logs\pocketd-authprobe.err.log' -Tail 20
  exit 3
}
