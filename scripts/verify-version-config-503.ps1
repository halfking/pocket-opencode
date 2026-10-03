#!/usr/bin/env pwsh
# Real-process black-box check of the loadVersionConfig contract (2026-10-03).
#
# ASCII ONLY below, on purpose: this file has no UTF-8 BOM, and Windows
# PowerShell 5.1 decodes BOM-less files as ANSI/GBK. The CJK bytes then mangle
# the comments and break quote pairing -> ParserError. That is not
# hypothetical: the first version of this script was written with CJK
# comments and refused to parse at all.
#
# Why a real process is required (not just the Go unit tests): the unit tests
# use httptest with a bare &Server{} and skip config validation, route
# registration, and the real CWD. A defect on exactly that path has bitten
# this repo before -- the test constructor always assigned userSettings,
# which masked a nil that existed in production.
#
# Two scenarios, both must really run:
#   A. POCKET_VERSION_CONFIG_PATH -> nonexistent file  => expect 503 + error code
#   B. POCKET_VERSION_CONFIG_PATH -> real file          => expect 200 + real version
# Only A proves the fix holds on a real process. B proves it is not
# "always 503" -- a guard that only asserts A cannot tell a fix from a
# blanket outage.
#
# Port 18102 is independent: 8088 is another session's backend, and 18099 is
# what the device's adb reverse points at.
$ErrorActionPreference = 'Continue'
$Root = 'C:\workspace\openpocket'
# Overridable so the negative control can point this at a pre-fix binary.
# A guard that can only ever run against the fixed build cannot tell "the fix
# works" from "the guard is vacuous" -- see the header of the handoff.
$Exe  = if ($env:VF_EXE) { $env:VF_EXE } else { Join-Path $Root 'backend\.verify-bin\pocketd-versionfix.exe' }
$ProcName = if ($env:VF_EXE) { [System.IO.Path]::GetFileNameWithoutExtension($env:VF_EXE) } else { 'pocketd-versionfix' }
$Port = 18102
$Cfg  = Join-Path $Root 'backend\config\version.json'
$Bad  = Join-Path $Root 'backend\config\version-does-not-exist.json'

if (-not (Test-Path $Exe)) { Write-Host "[FAIL] $Exe missing"; exit 1 }
Write-Host "[info] binary = $Exe"

$env:POCKET_POSTGRES_DSN      = 'postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable'
$env:POCKET_PG_SCHEMA         = 'opencode_pocket'
$env:POCKET_DEV_AUTH          = 'true'
$env:POCKET_AUTH_LEGACY_ONLY  = 'true'
$env:POCKET_DATA_DIR          = Join-Path $Root 'data'
$env:POCKET_HTTP_PORT         = "$Port"
$env:POCKET_REDCLAW_ADMIN_URL = 'http://127.0.0.1:1/admin'

function Boot([string]$cfgPath, [string]$tag) {
  Get-Process -Name $ProcName -ErrorAction SilentlyContinue | Stop-Process -Force
  Start-Sleep -Seconds 2
  $env:POCKET_VERSION_CONFIG_PATH = $cfgPath
  Start-Process -FilePath $Exe -WindowStyle Hidden -WorkingDirectory (Join-Path $Root 'backend') `
    -RedirectStandardOutput (Join-Path $Root "logs\vf-$tag.out.log") `
    -RedirectStandardError  (Join-Path $Root "logs\vf-$tag.err.log")
  Start-Sleep -Seconds 10
}

function Probe() {
  # curl.exe instead of Invoke-WebRequest: IWR throws on non-2xx and the
  # error path's GetResponseStream() is already consumed, so the first version
  # of this probe read an EMPTY body from a perfectly good 503 and the guard
  # reported "503 body lacks error code" -- the probe was broken, the
  # implementation was fine. curl.exe gives status and body in one shot.
  $tmp = Join-Path $env:TEMP ("vf-probe-$([Guid]::NewGuid().ToString('N')).txt")
  $code = & curl.exe -s -o $tmp -w '%{http_code}' -X POST `
    "http://127.0.0.1:$Port/api/app/check-update" `
    -H 'Content-Type: application/json' -d '{}'
  $body = ''
  if (Test-Path $tmp) { $body = [System.IO.File]::ReadAllText($tmp) }
  return @{ code = [int]$code; body = $body }
}

Boot $Bad 'missing'
$a = Probe
Write-Host "[A] cfg=$Bad"
Write-Host "[A] status=$($a.code) len=$($a.body.Length)"

Boot $Cfg 'present'
$b = Probe
Write-Host "[B] cfg=$Cfg"
Write-Host "[B] status=$($b.code) len=$($b.body.Length)"

$fail = 0
# Reverse control first: the probe itself must be able to read a body.
# If B is also short, the whole verdict is untrustworthy -- a guard that
# cannot read the good case would "pass" for the wrong reason.
if ($b.body.Length -lt 50)   { Write-Host '[FAIL] probe cannot read body even on 200 -- verdict untrustworthy'; $fail++ }
elseif ($a.body.Length -lt 50){ Write-Host '[FAIL] probe cannot read body on 503 (probe defect, not implementation defect)'; $fail++ }
elseif ($a.code -ne 503)      { Write-Host "[FAIL] A: expected 503, got $($a.code)"; $fail++ }
elseif ($a.body -notmatch 'version_config_not_found') { Write-Host '[FAIL] A: 503 body lacks the error code'; $fail++ }
elseif ($a.body -notmatch 'version-does-not-exist\.json') { Write-Host '[FAIL] A: 503 body lacks the tried path'; $fail++ }
else { Write-Host '[OK] A: 503 + error code + tried path' }

if ($b.code -ne 200)          { Write-Host "[FAIL] B: expected 200, got $($b.code)"; $fail++ }
elseif ($b.body -notmatch '"version":"') { Write-Host '[FAIL] B: 200 body has no real version'; $fail++ }
else { Write-Host '[OK] B: 200 with real version' }

Get-Process -Name $ProcName -ErrorAction SilentlyContinue | Stop-Process -Force
if ($fail -eq 0) { Write-Host 'ALL GREEN' } else { Write-Host "FAILURES=$fail" }
exit $fail
