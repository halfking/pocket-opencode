# android-apk-fingerprint.ps1 - fingerprint the debug APK of THIS checkout.
#
# BUG-V2 (2026-10-01): this script used to hardcode
#   C:\workspace\openpocket\frontend\android\...\app-debug.apk
# i.e. the MAIN checkout, no matter which worktree it was invoked from.
# The failure mode is worse than a crash: on this machine BOTH APKs exist, so
# the script exited 0 and wrote a confident-looking fingerprint -- including a
# "runbook SHA256 matches / DRIFT" verdict -- that described a DIFFERENT source
# tree than the one under test. A verification gate that attests the wrong
# artifact is a false green, which is worse than no gate at all.
#
# Fixes in this version:
#   1. every path is derived from $PSScriptRoot, so the script always fingerprints
#      the checkout it lives in (override with -RepoRoot when that is not enough);
#   2. the fingerprint now records git provenance (commit / branch / dirty count)
#      so a report can be attributed to an exact source state instead of floating;
#   3. a missing APK is a hard failure with a nonzero exit, not a silent skip;
#   4. if the worktree is dirty, the report says so loudly.
#
# ASCII-only on purpose: PowerShell 5.1 decodes a BOM-less .ps1 as ANSI, so
# non-ASCII comments here can break string quoting.
param(
  [string]$RepoRoot = "",
  [string]$OutFile = ""
)

$ErrorActionPreference = 'Stop'

if (-not $RepoRoot) { $RepoRoot = Split-Path -Parent $PSScriptRoot }
$RepoRoot = (Resolve-Path $RepoRoot).Path
if (-not $OutFile) { $OutFile = Join-Path $RepoRoot 'logs\apk-fingerprint.txt' }

$env:JAVA_HOME = 'C:\Program Files\Eclipse Adoptium\jdk-21.0.12.101-hotspot'
$env:ANDROID_HOME = "$env:LOCALAPPDATA\Android"
$env:PATH = "$env:JAVA_HOME\bin;$env:ANDROID_HOME\build-tools\34.0.0;$env:ANDROID_HOME\platform-tools;$env:PATH"

$apkDir = Join-Path $RepoRoot 'frontend\android\app\build\outputs\apk\debug'
$apk = Join-Path $apkDir 'app-debug.apk'
$meta = Join-Path $apkDir 'output-metadata.json'
$runbook = Join-Path $RepoRoot 'docs\audits\2026-09-20-real-device-emulator-runbook.md'

if (-not (Test-Path $apk)) {
  Write-Host "FAIL - APK missing at $apk"
  Write-Host "      This is now a hard error on purpose (BUG-V2): fingerprinting some other"
  Write-Host "      checkout's APK would attest the wrong source tree."
  Write-Host "      Build it first:  cd <repo>\frontend\android; .\gradlew.bat assembleDebug"
  exit 1
}

New-Item -ItemType Directory -Force -Path (Split-Path -Parent $OutFile) | Out-Null
"" | Out-File $OutFile

# ---- 1. Source provenance ------------------------------------------------
# Without this, a SHA256 says nothing about WHICH code produced the APK.
$commit = '<unknown>'
$branch = '<unknown>'
$dirty = '<unknown>'
try {
  $commit = (& git -C $RepoRoot rev-parse HEAD 2>$null).Trim()
  if ($LASTEXITCODE -ne 0 -or -not $commit) { $commit = '<git failed>' }
} catch { $commit = '<git failed>' }
try {
  $branch = (& git -C $RepoRoot rev-parse --abbrev-ref HEAD 2>$null).Trim()
  if ($LASTEXITCODE -ne 0 -or -not $branch) { $branch = '<git failed>' }
} catch { $branch = '<git failed>' }
try {
  $dirty = @(& git -C $RepoRoot status --porcelain 2>$null | Where-Object { $_ -notmatch '^\?\?' }).Count
} catch { $dirty = '<git failed>' }

# Whole-worktree dirty is the WRONG question in a shared worktree: 8 concurrent
# sessions work out of the same repo, so backend/e2e/docs churn is normal and has
# zero effect on this APK. What actually decides attribution is whether any file
# inside the APK's input closure is dirty. Report BOTH -- this ADDS a signal, it
# does not weaken the warning above (2026-10-01).
$apkInputClosure = @(
  'frontend/src', 'frontend/public', 'frontend/index.html', 'frontend/vite.config.ts',
  'frontend/package.json', 'frontend/capacitor.config.ts', 'frontend/android'
)
# NOTE: the closure count deliberately ALSO includes untracked (??) files, unlike
# the whole-worktree number above. An untracked new file under frontend/src is
# perfectly buildable locally and would silently change the bundle, so for
# attribution purposes it must count.
$closureDirty = '<git failed>'
try {
  $closureDirty = @(& git -C $RepoRoot status --porcelain -- $apkInputClosure 2>$null).Count
} catch { $closureDirty = '<git failed>' }

Add-Content $OutFile "=== Source provenance ==="
Add-Content $OutFile ("RepoRoot       : {0}" -f $RepoRoot)
Add-Content $OutFile ("git commit     : {0}" -f $commit)
Add-Content $OutFile ("git branch     : {0}" -f $branch)
Add-Content $OutFile ("dirty (tracked): {0}" -f $dirty)
Add-Content $OutFile ("dirty in APK input closure: {0}   <- the number that decides attribution" -f $closureDirty)
if ($dirty -ne '0') {
  Add-Content $OutFile "[!] WORKTREE HAS UNCOMMITTED TRACKED CHANGES - the APK below cannot be"
  Add-Content $OutFile "[!] attributed to a commit. Do not treat a green runbook check as proof."
  if ($closureDirty -eq '0') {
    Add-Content $OutFile "[i] ...but every dirty file is OUTSIDE the APK input closure, so this"
    Add-Content $OutFile "[i] specific APK still corresponds to the commit above. See the closure"
    Add-Content $OutFile "[i] list below; it is the authoritative statement, not the count above."
  }
}
if ($closureDirty -ne '0' -and $closureDirty -ne '<git failed>') {
  Add-Content $OutFile "[!] FILES INSIDE THE APK INPUT CLOSURE ARE DIRTY - this APK genuinely"
  Add-Content $OutFile "[!] cannot be attributed to a commit:"
  & git -C $RepoRoot status --porcelain -- $apkInputClosure 2>$null |
    ForEach-Object { Add-Content $OutFile ("      " + $_) }
}
Add-Content $OutFile ""

# ---- 2. APK identity ----------------------------------------------------
$sha = (Get-FileHash -Algorithm SHA256 -Path $apk).Hash
$len = (Get-Item $apk).Length
$ts = (Get-Item $apk).LastWriteTime.ToString('yyyy-MM-dd HH:mm:ss')
Add-Content $OutFile "=== APK fingerprint ($(Get-Date -Format 'yyyy-MM-dd HH:mm:ss')) ==="
Add-Content $OutFile ("Path        : {0}" -f $apk)
Add-Content $OutFile ("Size        : {0} bytes ({1:N2} MB)" -f $len, ($len / 1MB))
Add-Content $OutFile ("Modified    : {0}" -f $ts)
Add-Content $OutFile ("SHA256      : {0}" -f $sha)
Add-Content $OutFile ""

# ---- 3. Build metadata --------------------------------------------------
Add-Content $OutFile "=== Build metadata ==="
if (Test-Path $meta) {
  try {
    $j = Get-Content $meta -Raw | ConvertFrom-Json
    Add-Content $OutFile ("applicationId : {0}" -f $j.applicationId)
    Add-Content $OutFile ("variantName   : {0}" -f $j.variantName)
    if ($j.elements -and $j.elements.Count -gt 0) {
      Add-Content $OutFile ("versionCode   : {0}" -f $j.elements[0].versionCode)
      Add-Content $OutFile ("versionName   : {0}" -f $j.elements[0].versionName)
    }
  } catch {
    Add-Content $OutFile ("[-] output-metadata.json unreadable: {0}" -f $_.Exception.Message)
  }
} else {
  Add-Content $OutFile "[-] output-metadata.json missing"
}
Add-Content $OutFile ""

# ---- 4. aapt2 badging ---------------------------------------------------
Add-Content $OutFile "=== aapt2 dump badging ==="
try {
  $badging = & aapt2 dump badging $apk 2>&1 | Select-Object -First 5
  foreach ($line in $badging) { Add-Content $OutFile ("{0}" -f $line) }
} catch {
  Add-Content $OutFile ("[-] aapt2 unavailable: {0}" -f $_.Exception.Message)
}
Add-Content $OutFile ""

# ---- 5. Runbook drift check (against THIS checkout's runbook) ----------
Add-Content $OutFile "=== Runbook drift check ==="
if (Test-Path $runbook) {
  $runbookText = Get-Content $runbook -Raw
  $runbookSHA = $null
  if ($runbookText -match 'SHA256\s*\|\s*`([0-9A-F]{64})`') { $runbookSHA = $matches[1] }
  if ($null -ne $runbookSHA) {
    Add-Content $OutFile ("runbook SHA256 : {0}" -f $runbookSHA)
    Add-Content $OutFile ("current  SHA256: {0}" -f $sha)
    if ($runbookSHA -eq $sha) {
      Add-Content $OutFile "[+] Match - runbook records this exact APK"
    } else {
      Add-Content $OutFile "[!] DRIFT - runbook records a different SHA256."
      Add-Content $OutFile "[!] Either the APK was rebuilt since, or the runbook describes another"
      Add-Content $OutFile "[!] build. Re-verify on device before trusting any runbook step."
    }
  } else {
    Add-Content $OutFile "[-] runbook has no SHA256 table row, skip drift check"
  }
} else {
  Add-Content $OutFile ("[-] runbook missing at {0}" -f $runbook)
}

# ---- 6. verifier -------------------------------------------------------
Add-Content $OutFile ""
Add-Content $OutFile "=== Apksigner verifier ==="
try {
  $verify = & apksigner verify --print-certs $apk 2>&1 | Select-Object -First 8
  foreach ($line in $verify) { Add-Content $OutFile ("{0}" -f $line) }
} catch {
  Add-Content $OutFile ("[-] apksigner unavailable: {0}" -f $_.Exception.Message)
}

Write-Host "DONE - $OutFile"
Write-Host "  repo    : $RepoRoot"
Write-Host "  commit  : $commit (dirty=$dirty)"
Write-Host "  sha256  : $sha"
