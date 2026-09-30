$ErrorActionPreference = 'Stop'
$env:JAVA_HOME = 'C:\Program Files\Eclipse Adoptium\jdk-21.0.12.101-hotspot'
$env:ANDROID_HOME = 'C:\Users\86133\AppData\Local\Android'
# 真机 192.168.31.19 与工作站 192.168.31.20 同网段，直接指本机 pocketd。
# mode 用 android-dev：守卫 assertApiBaseForBuild 会读到 .env.android-dev 而放行，
# 而真正内联进 bundle 的是 process.env 里的 VITE_API_BASE（Vite 优先级 process.env 最高）。
$env:VITE_API_BASE = 'http://192.168.31.20:8088'
$node = 'C:\tools\node-v22.23.2-win-x64\node.exe'
$npx  = 'C:\tools\node-v22.23.2-win-x64\node_modules\npm\bin\npx-cli.js'

Set-Location C:\workspace\openpocket\frontend
Write-Host '=== vite build (mode=android-dev) ==='
& $node node_modules\vite\bin\vite.js build --mode android-dev
if ($LASTEXITCODE -ne 0) { throw "vite build failed" }

Write-Host '=== cap sync android ==='
& $node $npx cap sync android
if ($LASTEXITCODE -ne 0) { throw "cap sync failed" }

Write-Host '=== gradle assembleDebug ==='
Set-Location C:\workspace\openpocket\frontend\android
& .\gradlew.bat assembleDebug --console=plain
if ($LASTEXITCODE -ne 0) { throw "gradle failed" }

Write-Host '=== DONE ==='
Get-Item C:\workspace\openpocket\frontend\android\app\build\outputs\apk\debug\app-debug.apk |
  Select-Object FullName, Length, LastWriteTime | Format-List
