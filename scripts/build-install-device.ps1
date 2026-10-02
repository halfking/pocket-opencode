# Build the device APK with a LAN API base, then install it on the real phone.
#
# This file is deliberately ASCII-only. PowerShell 5.1 decodes a BOM-less .ps1
# as ANSI, so UTF-8 comments get mangled and BREAK STRING QUOTING. Keep ASCII.
#
# 2026-10-02 rewrite. The previous version of this file was a landmine:
#   - it built from C:\workspace\openpocket\wt3, a directory that is NOT a git
#     repository, so the APK did not correspond to any branch of this code;
#   - it baked VITE_API_BASE=http://127.0.0.1:8088. On a phone 127.0.0.1 is the
#     PHONE ITSELF, and the backend runs on 18099, not 8088. The APK that was
#     actually on disk (built 03:00) carried http://localhost:18099 and could
#     not reach any backend at all -- yet install-apk-to-device.ps1 documents
#     that the base is "confirmed present inside the APK bundle". It was not.
# This version derives the repo from its own location, uses the LAN address, and
# then PROVES the base ended up inside the APK instead of asserting it.
#
# Prerequisites:
#   - backend listening on 18099 (scripts\start-local-backend.ps1)
#   - this machine reachable from the phone at $ApiHost
# Adjust $ApiHost if the DHCP lease moved; Test-NetConnection below will say.
#
# Usage:
#   powershell -ExecutionPolicy Bypass -File scripts\build-install-device.ps1
#   powershell -ExecutionPolicy Bypass -File scripts\build-install-device.ps1 -SkipInstall
param(
  [string]$ApiHost   = '192.168.31.20',
  [int]   $ApiPort   = 18099,
  [string]$Serial    = '192.168.31.19:5555',
  [switch]$SkipInstall,
  # The backend here speaks plain http on the LAN and there is no TLS
  # terminator, so the BUG-F guard (assert-no-plaintext-backend.mjs) refuses the
  # build. The guard documents its own escape hatch for exactly this case
  # ("real-device plaintext integration testing"). It is OFF by default so the
  # guard keeps protecting every ordinary build; turn it on deliberately and
  # only for a debug APK that talks to a backend on your own LAN.
  [switch]$AllowPlaintextApi
)
$ErrorActionPreference = 'Continue'

$repo    = Split-Path -Parent $PSScriptRoot
$adb     = 'C:\Users\86133\AppData\Local\Android\platform-tools\adb.exe'
$apkPath = Join-Path $repo 'frontend\android\app\build\outputs\apk\debug\app-debug.apk'
$apiBase = "http://${ApiHost}:${ApiPort}"

Write-Host "=== [0/5] preflight ===" -ForegroundColor Cyan
Write-Host "[repo] $repo"
$reachable = Test-NetConnection -ComputerName $ApiHost -Port $ApiPort -InformationLevel Quiet -WarningAction SilentlyContinue
Write-Host "[backend] ${ApiHost}:${ApiPort} reachable = $reachable"
if (-not $reachable) {
  Write-Host "[FAIL] backend not reachable on the LAN address. Fix that first; otherwise you" -ForegroundColor Red
  Write-Host "       ship an APK that cannot talk to the server." -ForegroundColor Red
  exit 1
}
if (-not (Test-Path (Join-Path $repo 'frontend\node_modules'))) {
  Write-Host "[FAIL] frontend\node_modules missing; run npm.cmd ci first" -ForegroundColor Red
  exit 1
}

# Both must be exported in the SAME shell invocation or the build-mobile guard
# reads a different effective value than the one that gets bundled.
$env:CAP_ANDROID_SCHEME = 'http'
$env:VITE_API_BASE      = $apiBase
if ($AllowPlaintextApi) {
  $env:POCKET_ALLOW_PLAINTEXT_API = '1'
  Write-Host "[inject] POCKET_ALLOW_PLAINTEXT_API=1 -- the BUG-F plaintext-backend guard is BYPASSED." -ForegroundColor Yellow
  Write-Host "         This APK sends mailbox credentials over plain http on the LAN." -ForegroundColor Yellow
} else {
  Remove-Item Env:\POCKET_ALLOW_PLAINTEXT_API -ErrorAction SilentlyContinue
  Write-Host "[inject] plaintext guard stays ON (pass -AllowPlaintextApi for a LAN debug build)" -ForegroundColor DarkGray
}
Write-Host "[inject] VITE_API_BASE=$apiBase  CAP_ANDROID_SCHEME=http"

# CAP_ANDROID_SCHEME is http, NOT https, and that is load-bearing. The backend
# speaks plain http, so an https://localhost page origin makes every API call
# mixed content and the WebView blocks it. Measured on the emulator with an
# https build, 2026-10-02:
#   Mixed Content: ... requested an insecure resource
#     'http://192.168.31.20:18099/api/app/check-update'
#     'http://192.168.31.20:18099/api/tasks'
#     insecure WebSocket endpoint 'ws://192.168.31.20:18099/ws?token=...'
# i.e. the app would have launched as an empty shell. The trade-off of an http
# origin is that localStorage is partitioned under http://localhost instead of
# https://localhost, so a device switching schemes has to log in again once.
# To use https the backend needs a real TLS terminator; there is none here.

Write-Host "=== [1/5] build-mobile (vite) ===" -ForegroundColor Cyan
# build-mobile.mjs refuses to build android/dev unless the profile file exists,
# and .env.* is gitignored, so create it from the template when missing.
$envFile = Join-Path $repo 'frontend\.env.android-dev'
if (-not (Test-Path $envFile)) {
  Write-Host "[setup] creating $envFile" -ForegroundColor Yellow
  Set-Content -Path $envFile -Encoding ASCII -Value @(
    '# Auto-created by build-install-device.ps1 (gitignored).',
    "VITE_API_BASE=$apiBase",
    'VITE_APP_ENV=dev'
  )
}
Push-Location (Join-Path $repo 'frontend')
node scripts/build-mobile.mjs android dev
$buildExit = $LASTEXITCODE
Pop-Location
# build-mobile runs `cap sync android` itself after the vite build, and that
# step exits null here EVERY time (2026-10-02, twice in a row). The same command
# run directly succeeds (exit=0, 1.42s); the log shows @capacitor/* resolving
# from a DIFFERENT worktree's node_modules, so npx inside that child process
# picks the wrong CLI. Do not treat it as fatal: the vite build and its own
# dist/assets sanity check are what matter, and step 4 proves the result from
# the APK itself. Our own cap sync below is the one that has to work.
$distAssets = Join-Path $repo 'frontend\dist\assets'
$distHasBase = $false
if (Test-Path $distAssets) {
  $distHasBase = @(Get-ChildItem $distAssets -Filter *.js |
    Select-String -Pattern ([regex]::Escape($apiBase)) -List).Count -gt 0
}
if ($buildExit -ne 0) {
  if ($distHasBase) {
    Write-Host "[WARN] build-mobile exited $buildExit (its internal cap sync), but dist/assets DOES contain $apiBase. Continuing." -ForegroundColor Yellow
  } else {
    Write-Host "BUILD_FAILED exit=$buildExit and $apiBase is NOT in dist/assets." -ForegroundColor Red
    exit 1
  }
}

Write-Host "=== [2/5] cap sync (this is the one that must succeed) ===" -ForegroundColor Cyan
Push-Location (Join-Path $repo 'frontend')
cmd /c "npx cap sync android"
$syncExit = $LASTEXITCODE
Pop-Location
if ($syncExit -ne 0) { Write-Host "[FAIL] cap sync exit=$syncExit" -ForegroundColor Red; exit 1 }

# cap sync silently exits null sometimes; read the file back instead of trusting it.
$cfg = Join-Path $repo 'frontend\android\app\src\main\assets\capacitor.config.json'
if (Test-Path $cfg) { Write-Host "[readback] $(Get-Content $cfg -Raw)" } else { Write-Host '[WARN] capacitor.config.json missing' }

Write-Host "=== [3/5] gradlew assembleDebug ===" -ForegroundColor Cyan
$env:JAVA_HOME = 'C:\Program Files\Eclipse Adoptium\jdk-21.0.12.101-hotspot'
Push-Location (Join-Path $repo 'frontend\android')
cmd /c "gradlew.bat assembleDebug"
$gradleExit = $LASTEXITCODE
Pop-Location
Write-Host "gradle exit=$gradleExit"
if ($gradleExit -ne 0) { exit 1 }

# cap sync rewrites these; they are not ours to commit.
Push-Location $repo
git checkout -- frontend/android/app/capacitor.build.gradle frontend/android/capacitor.settings.gradle
Pop-Location

Write-Host "=== [4/5] verify the API base is REALLY inside the APK ===" -ForegroundColor Cyan
if (-not (Test-Path $apkPath)) { Write-Host "[FAIL] APK not found: $apkPath" -ForegroundColor Red; exit 1 }
Add-Type -AssemblyName System.IO.Compression.FileSystem
$zip   = [System.IO.Compression.ZipFile]::OpenRead($apkPath)
$found = 0
$loop  = 0
foreach ($e in $zip.Entries) {
  if ($e.FullName -match '\.js$' -and $e.Length -gt 0) {
    $sr = New-Object System.IO.StreamReader($e.Open())
    $t  = $sr.ReadToEnd(); $sr.Close()
    if ($t.Contains($apiBase)) { $found++; Write-Host "[ok] $apiBase found in $($e.FullName)" }
    if ($t -match 'http://localhost:\d+|http://127\.0\.0\.1:\d+') { $loop++ }
  }
}
$zip.Dispose()
if ($found -eq 0) {
  Write-Host "[FAIL] $apiBase NOT found in the APK. Do not install it." -ForegroundColor Red
  exit 1
}
if ($loop -gt 0) {
  Write-Host "[WARN] $loop bundle file(s) still mention a loopback base; check whether that is a live code path." -ForegroundColor Yellow
}
$ai = Get-Item $apkPath
Write-Host ("[apk] {0:N0} bytes, {1}" -f $ai.Length, $ai.LastWriteTime)

if ($SkipInstall) { Write-Host 'SKIP-INSTALL requested; stopping before adb.' -ForegroundColor Cyan; exit 0 }

Write-Host "=== [5/5] adb install ===" -ForegroundColor Cyan
& $adb connect $Serial | Out-Null
Start-Sleep -Seconds 3
$line = (& $adb devices | Select-String -Pattern ([regex]::Escape($Serial)) | Select-Object -First 1)
if ("$line" -notmatch '\sdevice\s*$') {
  Write-Host "[FAIL] device still offline: $line" -ForegroundColor Red
  Write-Host "  Fix on the phone: toggle Wireless debugging off then on." -ForegroundColor Yellow
  Write-Host "  The APK is built and verified; re-run with -SkipInstall omitted once it is online." -ForegroundColor Yellow
  exit 2
}
& $adb -s $Serial reverse "tcp:${ApiPort}" "tcp:${ApiPort}" | Out-Null
& $adb -s $Serial install -r -g $apkPath
if ($LASTEXITCODE -ne 0) { Write-Host "[FAIL] install exit=$LASTEXITCODE" -ForegroundColor Red; exit 3 }
Write-Host 'DONE - log in as admin, open email settings, expect 5 real accounts.' -ForegroundColor Green
