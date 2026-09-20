$env:JAVA_HOME = 'C:\Program Files\Eclipse Adoptium\jdk-21.0.12.101-hotspot'
$env:ANDROID_HOME = "$env:LOCALAPPDATA\Android"
$env:PATH = "$env:JAVA_HOME\bin;$env:ANDROID_HOME\cmdline-tools\latest\bin;$env:ANDROID_HOME\platform-tools;$env:ANDROID_HOME\build-tools\34.0.0;$env:PATH"

$apk = "C:\workspace\openpocket\frontend\android\app\build\outputs\apk\debug\app-debug.apk"
$out = 'C:\workspace\openpocket\logs\apk-verify.txt'
New-Item -ItemType Directory -Force -Path (Split-Path $out) | Out-Null
Set-Content -Path $out -Value ""

Add-Content -Path $out -Value "=== APK file info ==="
$sz = (Get-Item $apk).Length.ToString()
Add-Content -Path $out -Value "$sz bytes"
Add-Content -Path $out -Value ("[sha1]    " + (Get-FileHash -Algorithm SHA1 $apk).Hash)
Add-Content -Path $out -Value ("[sha256]  " + (Get-FileHash -Algorithm SHA256 $apk).Hash)

Add-Content -Path $out -Value ""
Add-Content -Path $out -Value "=== aapt2 dump badging ==="
& "$env:ANDROID_HOME\build-tools\34.0.0\aapt2.exe" dump badging $apk 2>&1 | Out-File -Append $out

Add-Content -Path $out -Value ""
Add-Content -Path $out -Value "=== aapt2 dump permissions ==="
& "$env:ANDROID_HOME\build-tools\34.0.0\aapt2.exe" dump permissions $apk 2>&1 | Out-File -Append $out

Add-Content -Path $out -Value ""
Add-Content -Path $out -Value "=== apksigner verify (verbose) ==="
& "$env:ANDROID_HOME\build-tools\34.0.0\apksigner.bat" verify --verbose $apk 2>&1 | Out-File -Append $out

Add-Content -Path $out -Value ""
Add-Content -Path $out -Value "=== AndroidManifest.xml (first 40 lines) ==="
& "$env:ANDROID_HOME\build-tools\34.0.0\aapt2.exe" dump xmltree --file AndroidManifest.xml $apk 2>&1 | Select-Object -First 40 | Out-File -Append $out

Write-Host "DONE - $out"
