# real-device-capture.ps1 — Capture 30-min test results
# Run 30 min after start to verify app + FGS still alive
$env:PATH = "$env:LOCALAPPDATA\Android\platform-tools;$env:PATH"
$REDMISERIAL = "4c308e2e"
$OUT = "C:\workspace\openpocket\logs\real-device-summary-$(Get-Date -Format 'yyyyMMdd-HHmmss').txt"

function Section($title) {
    Write-Output ""
    Write-Output "===== $title =====" | Tee-Object -Append $OUT
}

# Reset adb
adb kill-server | Out-Null
Start-Sleep -Seconds 2
adb start-server | Out-Null
Start-Sleep -Seconds 4

"=== Real-device capture @ $(Get-Date -Format 'yyyy-MM-dd HH:mm:ss') ===" | Out-File -FilePath $OUT -Encoding UTF8
"" | Out-File -Append $OUT -Encoding UTF8

Section "Device"
"Model:    " + (adb -s $REDMISERIAL shell getprop ro.product.model 2>$null) | Out-File -Append $OUT -Encoding UTF8
"Brand:    " + (adb -s $REDMISERIAL shell getprop ro.product.brand 2>$null) | Out-File -Append $OUT -Encoding UTF8
"HyperOS:  " + (adb -s $REDMISERIAL shell getprop ro.miui.ui.version.name 2>$null) | Out-File -Append $OUT -Encoding UTF8
"Android:  " + (adb -s $REDMISERIAL shell getprop ro.build.version.release 2>$null) | Out-File -Append $OUT -Encoding UTF8
"SDK:      " + (adb -s $REDMISERIAL shell getprop ro.build.version.sdk 2>$null) | Out-File -Append $OUT -Encoding UTF8
"Battery:  " + ((adb -s $REDMISERIAL shell dumpsys battery 2>$null | Select-String 'level:' | Select-Object -First 1) -replace '.*level:\s*') | Out-File -Append $OUT -Encoding UTF8

Section "Process"
adb -s $REDMISERIAL shell ps -A 2>$null | Select-String -Pattern "kaixuan|webview" | Out-File -Append $OUT -Encoding UTF8

Section "Foreground Service"
adb -s $REDMISERIAL shell dumpsys activity services 2>$null | Select-String -Pattern "AiStreamService|isForeground" | Out-File -Append $OUT -Encoding UTF8

Section "Notification"
adb -s $REDMISERIAL shell dumpsys notification --noredact 2>$null | Select-String -Pattern "AI 任务进行中|ai_stream_sync" | Out-File -Append $OUT -Encoding UTF8

Section "Whitelist"
adb -s $REDMISERIAL shell dumpsys deviceidle whitelist 2>$null | Select-String "kaixuan" | Out-File -Append $OUT -Encoding UTF8

Section "AppOps"
adb -s $REDMISERIAL shell cmd appops get com.kaixuan.opencode.pocket 2>$null | Select-String -Pattern "WAKE_LOCK|RUN_IN_BACKGROUND|RUN_ANY_IN_BACKGROUND|POST_NOTIFICATION" | Out-File -Append $OUT -Encoding UTF8

Section "OEM Kill Events (should be 0)"
adb -s $REDMISERIAL logcat -d 2>$null | Select-String -Pattern "Killed.*kaixuan|kill.*com.kaixuan.opencode.pocket" | Out-File -Append $OUT -Encoding UTF8

Section "Watchdog Hits (should be 0)"
adb -s $REDMISERIAL logcat -d 2>$null | Select-String -Pattern "Watchdog triggered|triggerWatchdog" | Out-File -Append $OUT -Encoding UTF8

Section "AiStreamService logs"
adb -s $REDMISERIAL logcat -d 2>$null | Select-String -Pattern "AiStreamService|AiStreamKeepalive" | Out-File -Append $OUT -Encoding UTF8

Section "Memory"
adb -s $REDMISERIAL shell dumpsys meminfo com.kaixuan.opencode.pocket 2>$null | Select-Object -First 30 | Out-File -Append $OUT -Encoding UTF8

Get-Content $OUT -Encoding UTF8