$ErrorActionPreference = 'Continue'
# Install the APK (with the email-config deployment fixes) onto the real device
# and forward the backend port.
#
# IMPORTANT: this file is deliberately ASCII-only. PowerShell 5.1 decodes a
# BOM-less .ps1 as ANSI, so UTF-8 Chinese comments get mangled and BREAK STRING
# QUOTING -- a Chinese comment next to a double-quoted string produces
# "Array index expression is missing" / "missing the terminator" parse errors.
# The sibling script start-local-backend.ps1 documents the same trap. Keep ASCII.
#
# Prerequisites (all verified in this repo):
#   - backend listening on 18099 (scripts\start-local-backend.ps1)
#   - the App's API base is http://192.168.31.20:18099, injected at build time
#     and confirmed present inside the APK bundle.
#
# BLOCKER: the device's adbd session is wedged (TCP 5555 accepts, but
# `adb devices` reports "offline"). That can only be fixed on the phone.
# This script detects that and exits 2 with the reason instead of hanging.
#
# Usage: powershell -ExecutionPolicy Bypass -File scripts\install-apk-to-device.ps1

$adb    = 'C:\Users\86133\AppData\Local\Android\platform-tools\adb.exe'
$serial = '192.168.31.19:5555'
$apk    = 'C:\workspace\openpocket\frontend\android\app\build\outputs\apk\debug\app-debug.apk'
# 2026-10-02: 原先指向 C:\workspace\openpocket-wt-maildeploy\... 的临时 worktree。
# 那个 worktree 可能被删或停用，脚本会直接 [FAIL] APK not found；
# 正式产物在主工作区，由 scripts\build-audit-apk.ps1 生成。

if (-not (Test-Path $apk)) { Write-Host "[FAIL] APK not found: $apk"; exit 1 }
$ai = Get-Item $apk
Write-Host ("[apk] {0} bytes, {1}" -f $ai.Length, $ai.LastWriteTime)

# --- 1. connect ----------------------------------------------------------------
# Do NOT trust the return value of `adb connect`: it happily prints
# "already connected" for a device that is still offline. The only valid signal
# is the device listing ending in "device" rather than "offline".
& $adb connect $serial | Out-Null
Start-Sleep -Seconds 3
$line = (& $adb devices | Select-String -Pattern ([regex]::Escape($serial)) | Select-Object -First 1)
if ("$line" -notmatch '\sdevice\s*$') {
  Write-Host "[FAIL] device still offline: $line" -ForegroundColor Red
  Write-Host "  Fix on the phone: toggle Wireless debugging off then on (reboot if needed)." -ForegroundColor Yellow
  Write-Host "  Already ruled out (host side):" -ForegroundColor Yellow
  Write-Host "    - ping OK and TCP 5555 connects => network fine, adbd is not responding"
  Write-Host "    - kill-server / start-server / disconnect + reconnect: no effect"
  Write-Host "    - 192.168.31.29:5555 exists but refuses; it is a different device"
  exit 2
}
Write-Host "[device] $serial online" -ForegroundColor Green

# --- 2. reverse port ------------------------------------------------------------
# The App's API base is the LAN address, not localhost, so reverse is not strictly
# required; keep it anyway so a localhost fallback would also work.
& $adb -s $serial reverse tcp:18099 tcp:18099 | Out-Null
Write-Host "[reverse] tcp:18099 -> host 18099"

# --- 3. install -----------------------------------------------------------------
& $adb -s $serial install -r -g $apk
if ($LASTEXITCODE -ne 0) { Write-Host "[FAIL] install failed exit=$LASTEXITCODE" -ForegroundColor Red; exit 3 }
Write-Host "[OK] APK installed" -ForegroundColor Green

# --- 4. verify package + backend reachability ------------------------------------
$pkg = (& $adb -s $serial shell pm list packages | Select-String 'opencode.pocket')
Write-Host ("[pkg] " + ("$pkg" -replace '^\s+', ''))
$re = Test-NetConnection -ComputerName '192.168.31.20' -Port 18099 -InformationLevel Quiet -WarningAction SilentlyContinue
Write-Host "[backend] 192.168.31.20:18099 reachable = $re"

Write-Host "DONE - log in as admin, open the email settings page, expect 5 real accounts." -ForegroundColor Green
