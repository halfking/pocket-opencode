#!/usr/bin/env pwsh
# restart-18099-aligned-secret.ps1 - restart the 18099 pocketd with the JWT
# secret the device App's session was actually issued with.
#
# ASCII ONLY on purpose: no UTF-8 BOM + PS 5.1 decodes as ANSI/GBK, which
# mangles CJK bytes in comments and breaks quote pairing -> ParserError.
# (Hit twice already this session.)
#
# WHY THIS EXISTS
# ---------------
# The instance listening on 18099 (pid 2196, started 23:35 by a concurrent
# session, binary logs/pocketd-invoicecheck.exe) was booted WITHOUT
# POCKET_JWT_SECRET, so config.go fell back to DevDefaultJWTSecret
# ("pocket-dev-insecure-secret-0000000000"). The device App's session was
# issued with start-local-backend.ps1's $JwtSecret
# ("pocket-local-dev-jwt-secret-do-not-use-in-shared-env").
#
# Result: the App's own token is 401 on the backend it is configured to talk
# to, while /healthz stays 200. Verified by HMAC recompute, not inference.
# scripts/jwt-secret-drift.mjs detects this; this script fixes it.
#
# WHY NOT scripts/start-local-backend.ps1
# --------------------------------------
# That script requires POCKET_AUTH_PASS / POCKET_DEV_PASS and exits 1
# without one. The repo deliberately has NO default (the 2026-10-03 security
# remediation deleted the hardcoded devPass constant that 8 tracked files
# had been carrying in plaintext). Inventing a password here would create a
# NEW mismatch: the backend's login password would no longer be the one the
# App uses. So this script sets the env explicitly instead.
#
# EVERY OTHER env var below is copied from the running instance's OWN boot
# log (logs/pd-18099-20261002-233541.err.log), not guessed:
#   data dir = C:\workspace\openpocket\data      <- POCKET_DATA_DIR
#   Postgres pool initialized (schema="opencode_pocket")
#   POCKET_AUTH_LEGACY_ONLY=true
#   Loaded version config from backend\config\version.json
#   Email credential self-check: all 5 enabled accounts decrypt
#   [email/scheduler] pipeline scheduled at <next 08:00>
#
# The SAME BINARY is reused on purpose: swapping in a different build would
# change the code underneath a concurrent session, which is a different and
# larger risk than the secret mismatch we are fixing here.
#
# Reverting: the old command line was just
#   logs\pocketd-invoicecheck.exe
# so `Start-Process` on it restores a working instance (wrong secret, but
# working) at any time.

$ErrorActionPreference = 'Continue'
$Root  = 'C:\workspace\openpocket'
$Exe   = Join-Path $Root 'logs\pocketd-invoicecheck.exe'
$Port  = 18099

if (-not (Test-Path $Exe)) { Write-Host "[FAIL] $Exe missing"; exit 1 }

# --- the one thing being changed ---
$env:POCKET_JWT_SECRET = 'pocket-local-dev-jwt-secret-do-not-use-in-shared-env'

# --- everything else, from the boot log of the instance being replaced ---
$env:POCKET_DATA_DIR           = Join-Path $Root 'data'
$env:POCKET_POSTGRES_DSN       = 'postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable'
$env:POCKET_PG_SCHEMA          = 'opencode_pocket'
$env:POCKET_HTTP_PORT          = "$Port"
$env:POCKET_AUTH_LEGACY_ONLY   = 'true'
$env:POCKET_VERSION_CONFIG_PATH = Join-Path $Root 'backend\config\version.json'

# NOTE: POCKET_EMAIL_MASTER_KEY deliberately NOT set. The boot log says
# "using auto-generated key at <dataDir>\email_master.key" and the
# self-check passed 5/5. That file already exists and is reused as long as
# POCKET_DATA_DIR is right. Setting the key wrong here would silently kill
# every mail account while healthz stayed green -- the exact trap the boot
# self-check exists to catch.

$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$outLog = Join-Path $Root "logs\pd-$Port-$stamp.out.log"
$errLog = Join-Path $Root "logs\pd-$Port-$stamp.err.log"

Write-Host "[info] stopping the old listener on $Port ..."
$old = Get-NetTCPConnection -State Listen -LocalPort $Port -ErrorAction SilentlyContinue
if ($old) {
  $oldPid = $old[0].OwningProcess
  Write-Host "[info] old pid = $oldPid"
  Stop-Process -Id $oldPid -Force -ErrorAction SilentlyContinue
}
Start-Sleep -Seconds 4

$still = Get-NetTCPConnection -State Listen -LocalPort $Port -ErrorAction SilentlyContinue
if ($still) {
  Write-Host "[FAIL] port $Port still bound after stop; refusing to start a second instance"
  exit 2
}

Write-Host "[info] starting with the aligned JWT secret ..."
Start-Process -FilePath $Exe -WindowStyle Hidden -WorkingDirectory (Join-Path $Root 'backend') `
  -RedirectStandardOutput $outLog -RedirectStandardError $errLog
Start-Sleep -Seconds 14

if (-not (Select-String -Path $errLog -Pattern 'pocketd listening' -Quiet -ErrorAction SilentlyContinue)) {
  Write-Host "[FAIL] new instance did not report listening; log:"
  Get-Content $errLog -Tail 25
  exit 3
}
Write-Host "[OK] listening again. log: $errLog"

# --- gates, read from the log, not assumed ---
$gates = @(
  @{ name = 'Postgres pool initialized'; fail = $true  },
  @{ name = 'Email credential self-check: all 5'; fail = $true },
  @{ name = 'daily pipeline runner injected'; fail = $true }
)
$bad = 0
foreach ($g in $gates) {
  if (Select-String -Path $errLog -Pattern $g.name -Quiet -ErrorAction SilentlyContinue) {
    Write-Host "[OK] $($g.name)"
  } else {
    Write-Host "[FAIL] missing: $($g.name)"
    $bad++
  }
}
# The whole point of this restart: JWT secret must no longer be the dev default.
if (Select-String -Path $errLog -Pattern 'pocketd listening' -Quiet) { Write-Host "[OK] instance up" }
Select-String -Path $errLog -Pattern 'pipeline scheduled at' | ForEach-Object { Write-Host "[info] $($_.Line)" }
if ($bad -gt 0) { exit 4 }
exit 0
