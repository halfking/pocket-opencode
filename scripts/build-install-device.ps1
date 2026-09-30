# 重建 dev APK 并装到真机（含 scheme / API base 注入）。
# 关键约束（踩过的坑）：
#  - CAP_ANDROID_SCHEME 与 VITE_API_BASE 必须在同一次 shell 调用里设，否则 build-mobile 守卫会拦。
#  - cap sync 会改写 capacitor.build.gradle / capacitor.settings.gradle，提交前必须 git checkout 还原。
#  - 装完必须回读 assets/capacitor.config.json 确认 scheme 真的写进去了（cap sync 偶发静默 exit=null）。
$ErrorActionPreference = 'Continue'
$repo = 'C:\workspace\openpocket\wt3'
$adb = 'C:\Users\86133\AppData\Local\Android\platform-tools\adb.exe'
$serial = '192.168.31.19:5555'

Set-Location "$repo\frontend"
$env:CAP_ANDROID_SCHEME = 'https'
$env:VITE_API_BASE = 'http://127.0.0.1:8088'

Write-Output '=== [1/5] build-mobile ==='
node scripts/build-mobile.mjs android dev
if ($LASTEXITCODE -ne 0) { Write-Output "BUILD_FAILED exit=$LASTEXITCODE"; exit 1 }

Write-Output '=== [2/5] cap sync ==='
cmd /c "npx cap sync android"

Write-Output '=== [3/5] readback capacitor.config.json ==='
$cfg = "$repo\frontend\android\app\src\main\assets\capacitor.config.json"
if (Test-Path $cfg) { Get-Content $cfg -Raw } else { Write-Output 'CONFIG_MISSING' }

Write-Output '=== [4/5] gradlew assembleDebug ==='
Set-Location "$repo\frontend\android"
$env:JAVA_HOME = 'C:\Program Files\Eclipse Adoptium\jdk-21.0.12.101-hotspot'
cmd /c "gradlew.bat assembleDebug"
Write-Output "gradle exit=$LASTEXITCODE"

Write-Output '=== [5/5] adb install ==='
$apk = Get-ChildItem "$repo\frontend\android\app\build\outputs\apk\debug" -Filter '*.apk' | Select-Object -First 1
Write-Output "apk=$($apk.FullName)"
& $adb -s $serial install -r -g $apk.FullName
Write-Output "install exit=$LASTEXITCODE"

Set-Location $repo
git checkout -- frontend/android/app/capacitor.build.gradle frontend/android/capacitor.settings.gradle
Write-Output 'DONE'
