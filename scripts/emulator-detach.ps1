$env:JAVA_HOME = 'C:\Program Files\Eclipse Adoptium\jdk-21.0.12.101-hotspot'
$env:ANDROID_HOME = "$env:LOCALAPPDATA\Android"
$env:PATH = "$env:JAVA_HOME\bin;$env:ANDROID_HOME\cmdline-tools\latest\bin;$env:ANDROID_HOME\platform-tools;$env:ANDROID_HOME\emulator;$env:PATH"

# kill any prior
Get-Process emulator -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep -Seconds 2

# Start emulator detached
$log = 'C:\workspace\openpocket\logs\emulator-run.log'
$err = 'C:\workspace\openpocket\logs\emulator-err.log'
""
"# flags: swiftshader_indirect + gpu off fallback attempt"
$args = @('-avd','pocket-test','-no-snapshot','-no-window','-no-audio','-no-boot-anim','-gpu','off','-accel','off','-verbose')
$p = Start-Process `
  -FilePath "$env:ANDROID_HOME\emulator\emulator.exe" `
  -ArgumentList $args `
  -RedirectStandardOutput $log `
  -RedirectStandardError $err `
  -PassThru -NoNewWindow
"emulator PID: $($p.Id)"
"logs: $log / $err"
