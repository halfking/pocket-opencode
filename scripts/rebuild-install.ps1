$ErrorActionPreference = 'Continue'
$repo = 'C:\workspace\openpocket\wt3'
$adb = 'C:\Users\86133\AppData\Local\Android\platform-tools\adb.exe'
$serial = '192.168.31.19:5555'

Set-Location "$repo\frontend"
$env:CAP_ANDROID_SCHEME = 'https'
$env:VITE_API_BASE = 'http://127.0.0.1:8088'

Write-Output '=== [1/4] vite build (android-dev) ==='
cmd /c "npx vite build --mode android-dev" 2>&1 | Select-String -Pattern "built in|error|ERROR" | Select-Object -Last 5
Write-Output "vite exit=$LASTEXITCODE"

Write-Output '=== [2/4] cap sync（重试 3 次：已知偶发静默 exit=null）==='
$ok = $false
for ($i = 1; $i -le 3; $i++) {
  cmd /c "npx cap sync android" 2>&1 | Select-String -Pattern "Sync finished|failed|error" | Select-Object -Last 2
  if ($LASTEXITCODE -eq 0) { $ok = $true; break }
  Write-Output "  cap sync 第 $i 次失败，重试"
  Start-Sleep -Seconds 3
}
Write-Output "cap sync ok=$ok"

$cfg = "$repo\frontend\android\app\src\main\assets\capacitor.config.json"
if (Test-Path $cfg) { Write-Output '--- capacitor.config.json scheme 回读 ---'; (Get-Content $cfg -Raw) -replace '\s+', ' ' }

Write-Output '=== [3/4] gradlew assembleDebug ==='
Set-Location "$repo\frontend\android"
$env:JAVA_HOME = 'C:\Program Files\Eclipse Adoptium\jdk-21.0.12.101-hotspot'
cmd /c "gradlew.bat assembleDebug" 2>&1 | Select-String -Pattern "BUILD|error|FAILED" | Select-Object -Last 5
Write-Output "gradle exit=$LASTEXITCODE"

Write-Output '=== [4/4] adb install ==='
$apk = Get-ChildItem "$repo\frontend\android\app\build\outputs\apk\debug" -Filter '*.apk' | Select-Object -First 1
Write-Output "apk=$($apk.FullName) $($apk.LastWriteTime)"
& $adb -s $serial install -r -g $apk.FullName 2>&1 | Select-Object -Last 2
Write-Output "install exit=$LASTEXITCODE"
& $adb -s $serial reverse tcp:8088 tcp:8088 | Out-Null
Set-Location $repo
git checkout -- frontend/android/app/capacitor.build.gradle frontend/android/capacitor.settings.gradle 2>&1 | Out-Null
Write-Output 'DONE'
