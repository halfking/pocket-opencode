param(
  [switch]$Confirm,
  # The data dir whose email_master.key can actually decrypt the IMAP credentials.
  # Measured 2026-10-02: only wt3\backend\data decrypts them. The
  # openpocket\data key decrypts the LLM gateway key but NOT the mail credentials.
  [string]$DataDir = 'C:\workspace\openpocket\wt3\backend\data',
  [int]$Port = 18110,
  [string]$Psql = 'C:\workspace\openpocket\logs\pg\dist\pgsql\bin\psql.exe',
  [string]$SeedSql = (Join-Path $PSScriptRoot '..\deploy\sql\chat_agents_seed.sql')
)

# ============================================================================
# recover-opencode-pocket-schema.ps1
#
# Rebuilds the production `opencode_pocket` schema after it was dropped whole
# by `DROP SCHEMA ... CASCADE` (twice on 2026-10-01, 19:47 and 22:48).
# Executes step by step and VERIFIES each step before moving on; any failed
# check aborts instead of continuing.
#
# Constraints baked in (each one is a lesson from that incident):
#   * Idempotent. Safe to re-run: every step either detects "already fine" and
#     skips, or does the work and then verifies the result.
#   * No secrets in this file. POCKET_AUTH_PASS and the gateway key are read
#     from environment variables only -- never from argv, never written to disk.
#   * Refuses to run without explicit -Confirm. Without it, it prints the plan
#     and exits.
#   * Does NOT fetch mail. POCKET_SCHEDULER_ENABLED=false throughout. Mail
#     re-fetch is a separate decision with its own authorization.
#
# ASCII only, no UTF-8 BOM: PowerShell 5.1 decodes BOM-less files as ANSI/GBK,
# and non-ASCII bytes there can corrupt string literals and break parsing.
#
# Usage:
#   # plan only
#   powershell -ExecutionPolicy Bypass -File scripts\recover-opencode-pocket-schema.ps1
#
#   # execute (secrets via environment)
#   $env:POCKET_RECOVER_AUTH_PASS='<>=8 chars>'
#   $env:POCKET_RECOVER_GATEWAY_KEY='<llm gateway api key>'   # optional
#   powershell -ExecutionPolicy Bypass -File scripts\recover-opencode-pocket-schema.ps1 -Confirm
# ============================================================================

$ErrorActionPreference = 'Stop'

$Schema    = 'opencode_pocket'
$PgHost    = '127.0.0.1'
$PgPort    = '5432'
$PgUser    = 'postgres'
$PgDb      = 'postgres'
$Exe       = Join-Path (Split-Path $PSScriptRoot -Parent) '.verify-bin\pocketd-recover.exe'
$JwtSecret = 'recover-local-throwaway-' + [guid]::NewGuid().ToString('N').Substring(0,24)

function Say($msg) { Write-Host ("==> " + $msg) }
function Die($msg) { Write-Host ("!! ABORT: " + $msg) -ForegroundColor Red; exit 1 }

# Runs a query and returns a single scalar.
#
# A psql failure ABORTS instead of returning $null. That distinction is the
# whole point: `[int]$null` is 0, so a connection failure and a genuine
# "count is zero" used to be indistinguishable -- on a script whose entire job
# is to tell whether the production schema is empty. Same failure mode that let
# the DROP SCHEMA incident report "ok".
function PsqlScalar([string]$sql) {
  $tmp = [System.IO.Path]::GetTempFileName() + '.sql'
  # No `SET client_encoding` preamble here on purpose: psql echoes the command
  # tag "SET" as the first result row, which then poisons the [int] cast.
  # PGCLIENTENCODING below already does the job.
  [System.IO.File]::WriteAllText($tmp, $sql)
  $env:PGHOST = $PgHost
  $env:PGPORT = $PgPort
  $env:PGUSER = $PgUser
  $env:PGDATABASE = $PgDb
  $env:PGCLIENTENCODING = 'SQL_ASCII'
  $out = & $Psql -t -A -f $tmp 2>&1
  $rc = $LASTEXITCODE
  if ($rc -ne 0) { Die ("psql failed (exit " + $rc + "): " + (($out | Select-Object -Last 5) -join ' ')) }
  $first = $out | Where-Object { $_ -ne '' } | Select-Object -First 1
  if ($null -eq $first) { Die ('psql returned no rows for: ' + $sql) }
  if ($first -notmatch '^-?\d+$') { Die ("expected a number from psql, got '" + $first + "' for: " + $sql) }
  return $first
}

# Applies a .sql FILE (not a query) with the target schema pinned as
# search_path. The seed file uses unqualified table names and contains no
# SET search_path, so without this it lands in `public` instead.
#
# Verified 2026-10-02 with a throwaway database:
#   unpinned -> public.chat_agents, 277 rows written, exit 0
#   pinned   -> opencode_pocket.chat_agents, 277 rows, public stays at 0 tables
# An unpinned run also pollutes the production database's public schema.
function PsqlApplyFile([string]$file) {
  $env:PGHOST = $PgHost
  $env:PGPORT = $PgPort
  $env:PGUSER = $PgUser
  $env:PGDATABASE = $PgDb
  $env:PGCLIENTENCODING = 'SQL_ASCII'
  $env:PGOPTIONS = '-c search_path=' + $Schema
  $out = & $Psql -v ON_ERROR_STOP=1 -f $file 2>&1
  $rc = $LASTEXITCODE
  # PGOPTIONS only lives in this process; each psql call re-pins what it needs.
  $env:PGOPTIONS = $null
  return @{ exit = $rc; out = $out }
}

function WaitForPort([int]$port, [int]$seconds) {
  $sw = [Diagnostics.Stopwatch]::StartNew()
  while ($sw.Elapsed.TotalSeconds -lt $seconds) {
    $c = Get-NetTCPConnection -LocalPort $port -State Listen -ErrorAction SilentlyContinue
    if ($c) { return [int]$c[0].OwningProcess }
    Start-Sleep -Seconds 2
  }
  return 0
}

# ---------------------------------------------------------------------------
# 0. Preflight
# ---------------------------------------------------------------------------
Say 'PREFLIGHT'

if (-not (Test-Path $Psql))    { Die ("psql not found: " + $Psql) }
if (-not (Test-Path $SeedSql)) { Die ("seed sql not found: " + $SeedSql) }
if (-not (Test-Path $DataDir))  { Die ("data dir not found: " + $DataDir) }

$keyPath = Join-Path $DataDir 'email_master.key'
if (-not (Test-Path $keyPath)) { Die ("no email_master.key under " + $DataDir) }
if ((Get-Item $keyPath).Length -ne 32) { Die ("email_master.key is not 32 bytes under " + $DataDir) }

$authPass = $env:POCKET_RECOVER_AUTH_PASS
$gwKey    = $env:POCKET_RECOVER_GATEWAY_KEY
if ($authPass -and $authPass.Length -lt 8) { Die 'POCKET_RECOVER_AUTH_PASS must be >= 8 chars' }

Say ("master key  : " + $keyPath + " (32 bytes, OK)")
Say ("seed sql    : " + $SeedSql)
if ($authPass) { Say 'admin pass  : from env (>= 8 chars)' } else { Say 'admin pass  : NOT SET - admin bootstrap will be skipped' }
if ($gwKey)    { Say 'gateway key : from env' }              else { Say 'gateway key : NOT SET - gateway step will be skipped' }

$tablesNow = [int](PsqlScalar ("SELECT count(*) FROM information_schema.tables WHERE table_schema='$Schema';"))
Say ("current tables in " + $Schema + " : " + $tablesNow)
if ($tablesNow -gt 0) {
  Say 'Schema already has tables. Nothing to recover -- refusing to touch it.'
  Say 'If you really want a clean rebuild, drop the schema yourself first. This script will not.'
  exit 0
}

# Leftovers from the incident. These predate this script and are NOT created by
# it -- pocketd pins search_path, so the rebuild will not write to public.
# public.chat_agents currently holds 3 rows (c1/c2/builtin, workspace ws-1,
# written 2026-10-01 21:31 local, i.e. between the two DROP SCHEMA events):
# test fixtures from the chatagent harness that used to write to the live DB.
$stray = [int](PsqlScalar ("SELECT count(*) FROM information_schema.tables WHERE table_schema='public';"))
if ($stray -gt 0) {
  Say ("NOTE: public schema already holds " + $stray + " table(s) from before. Left untouched.")
}

if (Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue) {
  Die ("port " + $Port + " is already in use")
}

Write-Host ''
Say 'PLAN'
Say '  1. build pocketd from this worktree'
Say '  2. start it on a SEPARATE port with SCHEDULER_ENABLED=false (no mail fetching)'
Say '     (the instance already running on its own port is NOT stopped by this script)'
Say '  3. verify the schema was recreated and the core tables exist'
Say '  4. backfill the 277 builtin chat agents from the seed sql (search_path pinned, idempotent)'
Say '  5. verify admin user bootstrap, then log in through /api/auth/login'
Say '  6. rewrite the LLM gateway config through the app own endpoint + live-verify'
Say '  7. print the manual follow-ups (email accounts, mail re-fetch)'
Write-Host ''
Say 'Each step verifies before continuing. Any failure aborts.'
Write-Host ''
Say 'Steps 2-6 were rehearsed end-to-end on 2026-10-02 against a throwaway'
Say 'database, including both the failing and the passing variant of the seed.'
Write-Host ''

if (-not $Confirm) {
  Write-Host 'Dry run only. Re-run with -Confirm to execute.' -ForegroundColor Yellow
  exit 0
}

# ---------------------------------------------------------------------------
# 1. Build
# ---------------------------------------------------------------------------
Say 'STEP 1/7  building pocketd'
New-Item -ItemType Directory -Force -Path (Split-Path $Exe) | Out-Null
Push-Location (Join-Path $PSScriptRoot '..\backend')
& go build -p 1 -o $Exe ./cmd/pocketd
$rc = $LASTEXITCODE
Pop-Location
if ($rc -ne 0) { Die ("go build failed: " + $rc) }
Say ("built " + $Exe)

# ---------------------------------------------------------------------------
# 2. Start (scheduler OFF)
# ---------------------------------------------------------------------------
Say 'STEP 2/7  starting pocketd (scheduler disabled, no mail fetching)'
$env:POCKET_POSTGRES_DSN      = "postgres://$PgUser@$PgHost`:$PgPort/$PgDb`?sslmode=disable"
$env:POCKET_PG_SCHEMA         = $Schema
$env:POCKET_DATA_DIR          = $DataDir
$env:POCKET_DEV_AUTH          = 'true'
$env:POCKET_AUTH_LEGACY_ONLY  = 'true'
$env:POCKET_HTTP_PORT         = "$Port"
$env:POCKET_SCHEDULER_ENABLED = 'false'
$env:POCKET_JWT_SECRET        = $JwtSecret
if ($authPass) { $env:POCKET_AUTH_PASS = $authPass }
# Never let env-provided gateway defaults fight the config written in step 6.
Remove-Item Env:POCKET_LLM_GATEWAY_URL     -ErrorAction SilentlyContinue
Remove-Item Env:POCKET_LLM_GATEWAY_API_KEY -ErrorAction SilentlyContinue

$outLog = [System.IO.Path]::GetTempFileName()
$errLog = [System.IO.Path]::GetTempFileName()
Start-Process -FilePath $Exe -WindowStyle Hidden -RedirectStandardOutput $outLog -RedirectStandardError $errLog
$proc = WaitForPort $Port 90
if ($proc -eq 0) {
  Get-Content $errLog -Tail 30 -ErrorAction SilentlyContinue | Write-Host
  Die ("pocketd did not listen on :" + $Port + " within 90s")
}
Say ("pocketd listening on :" + $Port + " (pid " + $proc + ")")

# ---------------------------------------------------------------------------
# 3. Verify the schema came back
# ---------------------------------------------------------------------------
Say 'STEP 3/7  verifying the schema was recreated'
$tables = [int](PsqlScalar ("SELECT count(*) FROM information_schema.tables WHERE table_schema='$Schema';"))
if ($tables -lt 1) {
  Get-Content $errLog -Tail 30 -ErrorAction SilentlyContinue | Write-Host
  Die 'schema still has 0 tables -- the server did not migrate. Aborting.'
}
Say ("tables recreated: " + $tables)
# Rehearsed 2026-10-02 against a throwaway DB: a healthy boot migrates 66 tables.
# chat_agents and llm_gateway_configs come from module-level migrations
# (chatagent/store.go and llm_gateway_store.go migrate()), NOT from pg.go, so
# they only exist if the process survived long enough to reach them.
foreach ($t in @('users', 'chat_agents', 'email_accounts', 'llm_gateway_configs')) {
  $n = [int](PsqlScalar ("SELECT count(*) FROM information_schema.tables WHERE table_schema='$Schema' AND table_name='$t';"))
  if ($n -ne 1) { Die ("expected table missing: " + $t + " -- a module migration did not run") }
  Write-Host ("    ok  " + $t)
}

# ---------------------------------------------------------------------------
# 4. Backfill builtin agents (idempotent)
# ---------------------------------------------------------------------------
Say 'STEP 4/7  backfilling builtin chat agents'
$seed = (Resolve-Path $SeedSql).Path
$r = PsqlApplyFile $seed
if ($r.exit -ne 0) {
  $r.out | Select-Object -Last 10 | Write-Host
  Die ("seed failed (exit " + $r.exit + ") -- the schema may be half-populated. Fix that before re-running.")
}
$agents = [int](PsqlScalar ("SELECT count(*) FROM $Schema.chat_agents;"))
Say ("chat_agents rows: " + $agents)
if ($agents -lt 200) { Die 'chat_agents looks empty (expected 277) -- aborting before going further' }
$canary = [int](PsqlScalar ("SELECT count(*) FROM $Schema.chat_agents WHERE id='customer-success-manager';"))
Say ("canary agent customer-success-manager present: " + $canary)
if ($canary -ne 1) { Die 'canary agent missing -- the seed did not apply' }

# Guard against the failure mode proven during rehearsal: if search_path is ever
# not pinned, the seed silently creates public.chat_agents and reports exit 0.
$leak = [int](PsqlScalar ("SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name='chat_agents';"))
$leakRows = 0
if ($leak -eq 1) {
  $leakRows = [int](PsqlScalar ("SELECT count(*) FROM public.chat_agents;"))
}
Say ("public.chat_agents: present=" + $leak + " rows=" + $leakRows)
if ($leakRows -ge 200) {
  Die 'the seed landed in public, not in the target schema. Aborting -- do not leave that in place.'
}

# ---------------------------------------------------------------------------
# 5. Admin user
# ---------------------------------------------------------------------------
Say 'STEP 5/7  admin user'
$users = [int](PsqlScalar ("SELECT count(*) FROM $Schema.users;"))
Say ("users rows: " + $users)
if ($users -eq 0 -and -not $authPass) {
  Write-Host '!! users table is EMPTY and POCKET_RECOVER_AUTH_PASS was not set.' -ForegroundColor Red
  Write-Host '!! pocketd refuses to auto-create the built-in admin with a default password'
  Write-Host '!! (336c883: the built-in 6-char password collides with the 8-char minimum, so'
  Write-Host '!!  that bootstrap path was doomed). Set POCKET_RECOVER_AUTH_PASS and re-run.'
  Write-Host '!! Stopping on purpose -- do NOT paper over it by inserting a row by hand.'
  exit 1
}
if ($users -eq 0) {
  Say 'restarting pocketd once so it bootstraps the admin with the supplied password'
  Stop-Process -Id $proc -Force -ErrorAction SilentlyContinue
  Start-Sleep -Seconds 3
  Start-Process -FilePath $Exe -WindowStyle Hidden -RedirectStandardOutput $outLog -RedirectStandardError $errLog
  $proc = WaitForPort $Port 90
  if ($proc -eq 0) { Die 'pocketd did not come back within 90s' }
  $users = [int](PsqlScalar ("SELECT count(*) FROM $Schema.users;"))
  Say ("users rows after bootstrap: " + $users)
  if ($users -lt 1) { Die 'admin bootstrap did not produce a user row' }
}

# ---------------------------------------------------------------------------
# 6. Gateway config through the app own endpoint, then live-verify
# ---------------------------------------------------------------------------
if (-not $gwKey) {
  Say 'STEP 6/7  SKIPPED (no POCKET_RECOVER_GATEWAY_KEY in env)'
} else {
  Say 'STEP 6/7  rewriting the gateway config via POST /api/llm-gateway/config'

  # Authenticate through the real login endpoint rather than minting a token
  # with cmd/gen-jwt. Rehearsed 2026-10-02: this also proves the recovered
  # instance can actually authenticate, and it returns the workspace id instead
  # of us hardcoding ws_<userID> and hoping it still matches.
  $devPass = $authPass
  if (-not $devPass) { Die 'POCKET_RECOVER_AUTH_PASS not set - cannot log in without a password' }
  $loginBody = @{ username = 'admin'; password = $devPass } | ConvertTo-Json
  try {
    $login = Invoke-RestMethod -Uri ('http://127.0.0.1:' + $Port + '/api/auth/login') `
      -Method Post -Body $loginBody -ContentType 'application/json' -TimeoutSec 20
  } catch {
    Die ('login failed: ' + $_.Exception.Message)
  }
  Say ('login ok: auth_method=' + $login.auth_method + ' user_id=' + $login.user_id + ' workspace_id=' + $login.workspace_id)
  if (-not $login.token) { Die 'login returned no token' }

  $hdr  = @{ Authorization = "Bearer $($login.token)" }
  $body = @{ baseURL = 'https://llm.kxpms.cn/v1'; apiKey = $gwKey } | ConvertTo-Json
  try {
    $resp = Invoke-RestMethod -Uri ("http://127.0.0.1:" + $Port + "/api/llm-gateway/config") `
      -Method Post -Headers $hdr -Body $body -ContentType 'application/json' -TimeoutSec 30
  } catch {
    Die ("gateway POST failed: " + $_.Exception.Message)
  }
  Say ('POST accepted: ok=' + $resp.ok + ' models=' + @($resp.models).Count)

  $cfg = Invoke-RestMethod -Uri ("http://127.0.0.1:" + $Port + "/api/llm-gateway/config") -Headers $hdr -TimeoutSec 20
  $nModels = @($cfg.models).Count
  $nPref = @($cfg.preferredModels).Count
  Say ("readback: baseURL=" + $cfg.baseURL + " apiKeySet=" + $cfg.apiKeySet + " models=" + $nModels + " preferred=" + $nPref)
  if (-not $cfg.apiKeySet) { Die 'readback shows apiKeySet=false' }
  # A fresh schema seeds the 9 bundled defaults. The incident this script
  # repairs produced a FRAGMENTED config (key set but zero models), so a
  # non-zero count here is a real assertion, not a formality.
  if ($nModels -lt 9)     { Die ('readback shows ' + $nModels + ' models, expected the 9 bundled defaults -- this is the fragmentation bug') }
  if ($nPref  -lt 9)      { Die ('readback shows ' + $nPref + ' preferred models, expected 9') }
  if ($cfg.baseURL -ne 'https://llm.kxpms.cn/v1') { Die ('readback baseURL is ' + $cfg.baseURL) }

  $status = Invoke-RestMethod -Uri ("http://127.0.0.1:" + $Port + "/api/integration/status") -Headers $hdr -TimeoutSec 20
  Say ('integration_status.llm_gateway: ' + ($status.integrations.llm_gateway | ConvertTo-Json -Compress))

  # Live-verify the credential straight against the gateway, independent of pocketd.
  try {
    $m = Invoke-RestMethod -Uri 'https://llm.kxpms.cn/v1/models' `
      -Headers @{ Authorization = "Bearer $gwKey" } -TimeoutSec 45
    Say ('live gateway /models -> HTTP 200, ' + @($m.data).Count + ' models')
  } catch {
    Die ('live gateway /models failed: ' + $_.Exception.Message)
  }
}

# ---------------------------------------------------------------------------
# 7. Hand off the manual bits
# ---------------------------------------------------------------------------
Say 'STEP 7/7  done -- what still needs a human'
Write-Host ''
Write-Host '  [ ] email accounts: scripts\seed_email_accounts.sh (needs the envs loader + admin password)'
Write-Host '  [ ] mail re-fetch : needs real IMAP credentials. Start an instance with'
Write-Host '                      POCKET_SCHEDULER_ENABLED=true and POCKET_DATA_DIR pointing at'
Write-Host ('                      ' + $DataDir + '  (the only key that decrypts them)')
Write-Host '  [ ] turn on DDL logging on this PG so a third incident is actually diagnosable'
Write-Host '  [ ] add an automatic backup for this bare-metal PG (there was none today)'
Write-Host ''
Write-Host ("recover instance still running on :" + $Port + " (pid " + $proc + ")  logs: " + $outLog)
Write-Host ("stop it with: Stop-Process -Id " + $proc)
