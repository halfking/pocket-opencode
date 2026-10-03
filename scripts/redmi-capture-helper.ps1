# redmi-capture-helper.ps1 - simple adb screencap helper.
#
# ASCII-ONLY: PowerShell 5.1 reads a BOM-less .ps1 as ANSI, so UTF-8 Chinese
# comments get mangled and break string quoting (see install-apk-to-device.ps1).
param(
    [string]$Out    = "C:\workspace\openpocket\logs\redmi-shot.png",
    [string]$Serial = ""
)
$ErrorActionPreference = 'Stop'
$env:PATH = "C:\Users\86133\AppData\Local\Android\platform-tools;$env:PATH"

# 2026-10-02: the serial used to be hardcoded as "4c308e2e" (USB). That device
# is no longer connected; it now shows up over WiFi-adb as 192.168.31.19:5555.
# A hardcoded serial turns every capture into "device not found", and the
# usual workaround people reach for -- `adb shell screencap > file.png` --
# silently corrupts the PNG via CRLF translation.
#
# So: auto-pick the single connected device when no serial is given, and fail
# loudly if there is not exactly one.
if (-not $Serial) {
    $lines = & adb devices | Select-Object -Skip 1 | Where-Object { $_.Trim() }
    $devs = @($lines | ForEach-Object { ($_ -split '\s+')[0] } | Where-Object { $_ })
    if ($devs.Count -eq 0) { Write-Host "[FAIL] no adb device connected"; exit 2 }
    if ($devs.Count -gt 1) {
        Write-Host "[FAIL] multiple devices, pass -Serial explicitly:"
        $devs | ForEach-Object { Write-Host "  $_" }
        exit 2
    }
    $Serial = $devs[0]
}
Write-Host "[device] $Serial"

# Redirect through Start-Process so the PNG bytes are not CRLF-translated.
$proc = Start-Process -FilePath "adb.exe" -ArgumentList @("-s", $Serial, "exec-out", "screencap", "-p") `
                     -NoNewWindow -PassThru -RedirectStandardOutput $Out -Wait
Write-Host "Saved: $Out (exit $($proc.ExitCode))"
Get-Item $Out | Select-Object Name, Length | Format-Table -AutoSize
