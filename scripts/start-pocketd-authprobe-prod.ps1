$ErrorActionPreference = 'Continue'
# Production-shaped instance for the forgot-password probe: NO SMTP and NO
# POCKET_SMTP_DEBUG_ECHO. This is the shape a real deployment has, and it is the
# case that used to strand users silently on step 2 of the reset stepper.
# Runs on :8097 so it never touches :8088 or the :8098 debug instance.
# ASCII only: no UTF-8 BOM (PowerShell 5.1 decodes BOM-less files as ANSI/GBK).
$repo = 'C:\workspace\openpocket'
$exe  = 'C:\workspace\openpocket\logs\pocketd-authfix.exe'
$port = 8097

$env:POCKET_POSTGRES_DSN      = 'postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable'
$env:POCKET_PG_SCHEMA         = 'opencode_pocket'
$env:POCKET_DEV_AUTH          = 'true'
$env:POCKET_AUTH_LEGACY_ONLY  = 'true'
$env:POCKET_HTTP_PORT         = "$port"
$env:POCKET_SCHEDULER_ENABLED = 'false'
# Deliberately NOT setting POCKET_SMTP_DEBUG_ECHO, and no POCKET_SMTP_HOST.
Remove-Item Env:POCKET_SMTP_DEBUG_ECHO -ErrorAction SilentlyContinue
Remove-Item Env:POCKET_SMTP_HOST      -ErrorAction SilentlyContinue
$env:POCKET_DATA_DIR = 'C:\workspace\openpocket\logs\authprobe7-data'
New-Item -ItemType Directory -Force -Path $env:POCKET_DATA_DIR | Out-Null

Set-Location $repo
Start-Process -FilePath $exe -WindowStyle Hidden `
  -RedirectStandardOutput 'C:\workspace\openpocket\logs\pocketd-authprobe7.out.log' `
  -RedirectStandardError  'C:\workspace\openpocket\logs\pocketd-authprobe7.err.log'

Start-Sleep -Seconds 12
try {
  $hz = (Invoke-WebRequest -Uri "http://127.0.0.1:$port/healthz" -TimeoutSec 8).Content
  Write-Host "[OK] prod-shaped instance healthz = $hz on :$port" -ForegroundColor Green
} catch {
  Write-Warning "[FAIL] port $port not reachable: $($_.Exception.Message)"
  Get-Content 'C:\workspace\openpocket\logs\pocketd-authprobe7.err.log' -Tail 20
  exit 3
}
