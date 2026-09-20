$env:JAVA_HOME = 'C:\Program Files\Eclipse Adoptium\jdk-21.0.12.101-hotspot'
$env:ANDROID_HOME = "$env:LOCALAPPDATA\Android"
$env:PATH = "$env:JAVA_HOME\bin;$env:ANDROID_HOME\build-tools\34.0.0;$env:ANDROID_HOME\platform-tools;$env:PATH"

$apk = "C:\workspace\openpocket\frontend\android\app\build\outputs\apk\debug\app-debug.apk"
$meta = "C:\workspace\openpocket\frontend\android\app\build\outputs\apk\debug\output-metadata.json"
$out = 'C:\workspace\openpocket\logs\apk-fingerprint.txt'
$runbook = 'C:\workspace\openpocket\docs\audits\2026-09-20-real-device-emulator-runbook.md'

if (-not (Test-Path $apk)) {
  Write-Host "FAIL - APK missing at $apk (run gradle assembleDebug first)"
  exit 1
}

"" | Out-File $out

# 1. SHA256
$sha = (Get-FileHash -Algorithm SHA256 -Path $apk).Hash
$len = (Get-Item $apk).Length
$ts  = (Get-Item $apk).LastWriteTime.ToString('yyyy-MM-dd HH:mm:ss')
Add-Content $out "=== APK fingerprint ($(Get-Date -Format 'yyyy-MM-dd HH:mm:ss')) ==="
Add-Content $out ("Path        : {0}" -f $apk)
Add-Content $out ("Size        : {0} bytes ({1:N2} MB)" -f $len, ($len / 1MB))
Add-Content $out ("Modified    : {0}" -f $ts)
Add-Content $out ("SHA256      : {0}" -f $sha)
Add-Content $out ""

# 2. metadata.json
if (Test-Path $meta) {
  $j = Get-Content $meta -Raw | ConvertFrom-Json
  Add-Content $out "=== Build metadata ==="
  Add-Content $out ("applicationId        : {0}" -f $j.applicationId)
  Add-Content $out ("variantName          : {0}" -f $j.variantName)
  Add-Content $out ("versionCode          : {0}" -f ($j.elements[0].versionCode))
  Add-Content $out ("versionName          : {0}" -f ($j.elements[0].versionName))
  Add-Content $out ("minSdkVersionForDexing: {0}" -f $j.minSdkVersionForDexing)
  Add-Content $out ""
}

# 3. aapt2 badging
Add-Content $out "=== aapt2 dump badging ==="
$badging = & aapt2 dump badging $apk 2>&1 | Select-Object -First 5
foreach ($line in $badging) { Add-Content $out ("{0}" -f $line) }
Add-Content $out ""

# 4. Runbook drift check
Add-Content $out "=== Runbook drift check ==="
$runbookSHA = $null
if (Test-Path $runbook) {
  $runbookText = Get-Content $runbook -Raw
  if ($runbookText -match 'SHA256\s*\|\s*`([0-9A-F]{64})`') {
    $runbookSHA = $matches[1]
  }
  if ($null -ne $runbookSHA) {
    Add-Content $out ("runbook SHA256 : {0}" -f $runbookSHA)
    Add-Content $out ("current SHA256 : {0}" -f $sha)
    if ($runbookSHA -eq $sha) {
      Add-Content $out "[+] Match — runbook §0 is fresh"
    } else {
      Add-Content $out "[!] DRIFT — runbook §0 has stale SHA256; update before real-device test"
    }
  } else {
    Add-Content $out "[-] runbook has no SHA256 table row, skip drift check"
  }
} else {
  Add-Content $out "[-] runbook missing at $runbook"
}

# 5. verifier
Add-Content $out ""
Add-Content $out "=== Apksigner verifier ==="
$verify = & apksigner verify --print-certs $apk 2>&1 | Select-Object -First 8
foreach ($line in $verify) { Add-Content $out ("{0}" -f $line) }

Write-Host "DONE - $out"
