param(
  [int]$Port = 18190,
  [string]$Schema = 'rssdemo_test',
  [string]$DataDir = 'C:\workspace\openpocket\data\rssdemo',
  [string]$LogPrefix = 'C:\workspace\openpocket\logs\rssdemo',
  [Parameter(Mandatory = $false)][string]$AuthUser = 'admin',
  [Parameter(Mandatory = $false)][string]$AuthPass = ''
)
# Starts an ISOLATED pocketd instance (own schema + own port + own dataDir) to
# verify the whole chain in a REAL process with REAL outbound fetching:
#   built-in feed catalog -> one-tap import -> real fetch -> daily digest -> notification
#
# Isolation: schema rssdemo_test, port 18190, dataDir data/rssdemo.
# Never touches the production schema (opencode_pocket) or the production port.
#
# NOTE: keep this file ASCII-only. PowerShell 5.1 reads BOM-less files as ANSI,
# and a mis-decoded non-ASCII comment can swallow the next line and break parsing.
$ErrorActionPreference = 'Stop'

New-Item -ItemType Directory -Force -Path $DataDir | Out-Null

# Refuse to start if the port is taken: a stale process answering /healthz
# would make this whole verification meaningless.
if (Get-NetTCPConnection -State Listen -LocalPort $Port -ErrorAction SilentlyContinue) {
  throw "port $Port already in use; refusing to start a second instance"
}

$env:POCKET_POSTGRES_DSN = 'postgresql://postgres@127.0.0.1:5432/postgres?sslmode=disable'
$env:POCKET_PG_SCHEMA   = $Schema
$env:POCKET_HTTP_PORT   = "$Port"
$env:POCKET_DATA_DIR    = $DataDir
$env:POCKET_DB_PATH     = Join-Path $DataDir 'pocket.db'
$env:POCKET_DEV_AUTH    = 'true'
$env:POCKET_AUTH_LEGACY_ONLY = 'true'
$env:POCKET_AUTH_USER   = $AuthUser
# AuthPass is passed in at call time on purpose: never bake a real password into
# a committed script. When empty, generate a throwaway one and print it, so the
# log always says which credential this instance actually uses.
if ([string]::IsNullOrEmpty($AuthPass)) {
  $AuthPass = 'demo-pass-' + (Get-Random -Minimum 1000 -Maximum 9999)
  "GENERATED_AUTH_PASS=$AuthPass"
}
$env:POCKET_AUTH_PASS   = $AuthPass
$env:POCKET_JWT_SECRET  = 'rssdemo-local-secret-0123456789abcdef'
# Digest: run once at startup instead of waiting for 08:30, so the notification
# can be observed within minutes instead of a day.
$env:POCKET_RSS_ENABLED = 'true'
$env:POCKET_RSS_DIGEST_ENABLED = 'true'
$env:POCKET_RSS_DIGEST_STARTUP_RUN = 'true'
$env:POCKET_RSS_FETCH_INTERVAL = '30s'
$env:POCKET_RSS_SOURCE_INTERVAL = '5m'

$p = Start-Process -FilePath 'C:\workspace\openpocket\backend\pocketd-demo.exe' `
  -WorkingDirectory 'C:\workspace\openpocket\backend' `
  -RedirectStandardOutput "$LogPrefix.out.log" -RedirectStandardError "$LogPrefix.err.log" `
  -PassThru -WindowStyle Hidden

$deadline = (Get-Date).AddSeconds(60)
$ok = $false
while ((Get-Date) -lt $deadline) {
  Start-Sleep -Seconds 2
  if ($p.HasExited) { throw "pocketd exited early with code $($p.ExitCode); see $LogPrefix.err.log" }
  try {
    $h = Invoke-RestMethod -Uri "http://127.0.0.1:$Port/healthz" -TimeoutSec 3
    if ($h) { $ok = $true; break }
  } catch { }
}
if (-not $ok) { throw "pocketd did not answer /healthz within 60s; see $LogPrefix.err.log" }

"PID=$($p.Id) PORT=$Port SCHEMA=$Schema READY=1"
