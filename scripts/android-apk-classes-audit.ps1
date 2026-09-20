$env:JAVA_HOME = 'C:\Program Files\Eclipse Adoptium\jdk-21.0.12.101-hotspot'
$env:ANDROID_HOME = "$env:LOCALAPPDATA\Android"
$env:PATH = "$env:JAVA_HOME\bin;$env:ANDROID_HOME\cmdline-tools\latest\bin;$env:ANDROID_HOME\build-tools\34.0.0;$env:PATH"

$apk = "C:\workspace\openpocket\frontend\android\app\build\outputs\apk\debug\app-debug.apk"
$out = 'C:\workspace\openpocket\logs\apk-classes-audit.txt'

# Extract dex files from APK
$work = Join-Path $env:TEMP "apk-dex-extract"
Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path $work | Out-Null

# Use unzip via System.IO.Compression (PowerShell)
Add-Type -AssemblyName System.IO.Compression.FileSystem
$zip = [System.IO.Compression.ZipFile]::OpenRead($apk)
foreach ($entry in $zip.Entries) {
  if ($entry.FullName -match '\.dex$') {
    $outPath = Join-Path $work $entry.FullName
    $dir = Split-Path $outPath
    if (!(Test-Path $dir)) { New-Item -ItemType Directory -Force -Path $dir | Out-Null }
    $stream = [System.IO.File]::OpenWrite($outPath)
    $entry.Open().CopyTo($stream)
    $stream.Close()
  }
}
$zip.Dispose()

"" | Out-File $out
Add-Content $out "=== DEX files inside app-debug.apk ==="
Get-ChildItem $work -Recurse -Filter '*.dex' | ForEach-Object {
  Add-Content $out ("  {0} ({1} bytes)" -f $_.Name, $_.Length)
}

# Look for our key plugin classes and FGS/services
Add-Content $out ""
Add-Content $out "=== Plugin / FGS / Service classes audit ==="

$keywords = @(
  'AiStreamKeepalivePlugin',
  'AiStreamService',
  'BackgroundMicPlugin',
  'EmailFetchPlugin',
  'EmailFetchReceiver',
  'EmailFetchRunner',
  'BiometricAuthPlugin',
  'SherpaPlugin',
  'AppSettingsPlugin',
  'AudioDeviceRank',
  'PermissionSettingsLauncher',
  'MainActivity',
  'MainApplication',
  'Capacitor',
  'Plugin'
)

foreach ($kw in $keywords) {
  $hits = @()
  foreach ($dex in (Get-ChildItem $work -Recurse -Filter '*.dex')) {
    $out2 = & "$env:ANDROID_HOME\build-tools\34.0.0\dexdump.exe" $dex.FullName 2>&1
    foreach ($line in $out2) {
      if ($line -match $kw -and $line -match 'Class descriptor') {
        $hits += $line.Trim()
      }
    }
  }
  if ($hits.Count -gt 0) {
    Add-Content $out ("[+ $kw] $($hits.Count) class(es):")
    $hits | Select-Object -First 3 | ForEach-Object { Add-Content $out "    $_" }
  } else {
    Add-Content $out "[- $kw] NOT FOUND"
  }
}

Add-Content $out ""
Add-Content $out "=== Summary: 8 native plugins expected ==="
Add-Content $out "AiStreamKeepalivePlugin / AiStreamService"
Add-Content $out "BackgroundMicPlugin"
Add-Content $out "EmailFetchPlugin / EmailFetchReceiver / EmailFetchRunner"
Add-Content $out "BiometricAuthPlugin"
Add-Content $out "SherpaPlugin"
Add-Content $out "AppSettingsPlugin / AudioDeviceRank"
Add-Content $out "PermissionSettingsLauncher"

Write-Host "DONE - $out"
