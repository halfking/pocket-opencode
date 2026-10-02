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
# Reachability must be measured FROM THE DEVICE. A host-side
# Test-NetConnection here measures the WRONG SUBJECT: it can only ever answer
# "can this box reach itself", never "can the handset reach the backend".
# The old check printed
# "[backend] 192.168.31.20:18099 reachable = True" - a green line that was
# true about the host while saying nothing about the only thing that matters.
#
# Measured on the handset (2026-10-03, Redmi 2411DRN47C / Android 14,
# adb 192.168.31.19:5555): at the time this check was written the device could
# not even ARP-resolve 192.168.31.20 (AP client isolation) while the host
# check said True. Re-measured later the same day, all three now return 200.
# The divergence is history; the blind spot is not. A host-side green line
# still cannot distinguish "the phone can reach the baked address after a
# data wipe" from "only the host can" - which is exactly the case that
# matters, since a fresh install has no runtime override to fall back on.
#
# ASCII-ONLY COMMENTS ON PURPOSE: PowerShell 5.1 decodes a BOM-less .ps1 as
# ANSI, so UTF-8 CJK comments get mangled and can break string quoting. Keep
# this block English. (scripts/wecom-live-check.ps1 documents the same trap.)
#
# Device probe needs curl: verified /system/bin/curl exists on this handset.
# The probe returns the literal string "unreachable" for anything that is not
# a 3-digit status, so a missing curl degrades to a warning rather than a
# silent pass.
#
# Two different addresses, deliberately reported separately:
#   - 192.168.31.20 is what the APK BAKES IN (build-audit-apk.ps1 sets
#     VITE_API_BASE). If this fails, a fresh install with no runtime override
#     cannot reach the backend at all.
#   - localhost works only because `adb reverse tcp:18099` is set up above,
#     and it is what the device's pocket_api_base setting actually uses.
$probe = {
  param($url)
  $out = (& $adb -s $serial shell "curl -s -o /dev/null -w '%{http_code}' --max-time 6 $url" 2>$null | Out-String).Trim()
  if ($out -match '^\d{3}$') { return $out } else { return "unreachable" }
}
$baked = & $probe 'http://192.168.31.20:18099/healthz'
$tunnel = & $probe 'http://localhost:18099/healthz'
Write-Host "[backend from DEVICE] baked 192.168.31.20:18099/healthz -> $baked"
Write-Host "[backend from DEVICE] adb-reverse localhost:18099/healthz -> $tunnel"
if ($baked -ne '200') {
  Write-Warning "[WARN] the address baked into the APK is NOT reachable from the phone."
  Write-Warning "       A fresh install (app data cleared) will have no runtime override and cannot reach the backend."
  Write-Warning "       Either fix the phone->host path, or build with the reversedev profile so localhost is baked in."
}
if ($tunnel -ne '200') {
  Write-Warning "[FAIL] adb reverse tunnel is not working; the app cannot reach the backend right now." -ForegroundColor Red
  exit 4
}

Write-Host "DONE - log in as admin, open the email settings page, expect 5 real accounts." -ForegroundColor Green
