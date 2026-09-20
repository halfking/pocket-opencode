$env:JAVA_HOME = 'C:\Program Files\Eclipse Adoptium\jdk-21.0.12.101-hotspot'
$env:ANDROID_HOME = "$env:LOCALAPPDATA\Android"
$env:ANDROID_SDK_ROOT = $env:ANDROID_HOME
$env:PATH = "$env:JAVA_HOME\bin;$env:ANDROID_HOME\emulator;$env:ANDROID_HOME\platform-tools;$env:PATH"

$avd = 'pocket-test'
$log = 'C:\workspace\openpocket\logs\emulator-launch-2026-09-20.log'

"" | Out-File $log
Add-Content $log "=== emulator launch @ $(Get-Date -Format 'yyyy-MM-dd HH:mm:ss') ==="
Add-Content $log "AVD: $avd"
Add-Content $log "Flags: -no-snapshot -no-window -no-audio -no-boot-anim -gpu swiftshader_indirect -accel off -no-snapshot-save"

# kill any old emulator.exe processes
Get-Process -Name emulator -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep -Seconds 2

# launch detached
$args = @('-avd', $avd,
          '-no-snapshot',
          '-no-window',
          '-no-audio',
          '-no-boot-anim',
          '-gpu', 'swiftshader_indirect',
          '-accel', 'off',
          '-no-snapshot-save',
          '-verbose')

Add-Content $log "cmd: emulator.exe $($args -join ' ')"
$proc = Start-Process -FilePath "$env:ANDROID_HOME\emulator\emulator.exe" `
                       -ArgumentList $args `
                       -RedirectStandardOutput $log `
                       -RedirectStandardError "$log.err" `
                       -PassThru -WindowStyle Hidden

Add-Content $log "PID: $($proc.Id)"
Add-Content $log "----"

# Wait up to 90s for adb to see the device
$ready = $false
for ($i = 0; $i -lt 18; $i++) {
  Start-Sleep -Seconds 5
  $d = & adb devices
  Add-Content $log ("[t+{0,3}s] {1}" -f ((5*($i+1)), ($d -join ' | ')))
  $offlineCount = (& adb devices | Select-String -Pattern 'offline|unauthorized' | Measure-Object).Count
  $deviceCount  = (& adb devices | Select-String -Pattern '\bdevice\b' | Measure-Object).Count
  if ($deviceCount -ge 1 -and $offlineCount -eq 0) {
    $ready = $true
    break
  }
}

if ($ready) {
  Add-Content $log "[+] adb device visible after $(5*($i+1))s"
  # Try to wait for boot complete up to 15 min
  for ($b = 0; $b -lt 180; $b++) {
    $boot = & adb shell getprop sys.boot_completed 2>$null
    $boot = ($boot -replace '\s','').Trim()
    Add-Content $log ("[t+{0,3}s boot] sys.boot_completed='{1}'" -f ((5*($b+1)), $boot))
    if ($boot -eq '1') {
      Add-Content $log "[+] BOOT COMPLETED at $([math]::Round(((5*($b+1))/60),2)) min"
      break
    }
    Start-Sleep -Seconds 5
  }
} else {
  Add-Content $log "[-] adb never saw device"
}

Add-Content $log "===="
Add-Content $log "ALIVE: $(if($proc.HasExited){'exited code '+$proc.ExitCode}else{'running'})"
Add-Content $log "===="
