$env:JAVA_HOME = 'C:\Program Files\Eclipse Adoptium\jdk-21.0.12.101-hotspot'
$env:ANDROID_HOME = "$env:LOCALAPPDATA\Android"
$env:PATH = "$env:JAVA_HOME\bin;$env:ANDROID_HOME\build-tools\34.0.0;$env:PATH"

$apk = "C:\workspace\openpocket\frontend\android\app\build\outputs\apk\debug\app-debug.apk"
$out = 'C:\workspace\openpocket\logs\apk-so-audit.txt'

"" | Out-File $out

Add-Content $out "=== APK .so libraries (ABI coverage) ==="

# Open APK as ZIP, list .so per ABI
Add-Type -AssemblyName System.IO.Compression.FileSystem
$zip = [System.IO.Compression.ZipFile]::OpenRead($apk)
$sos = @{}
foreach ($entry in $zip.Entries) {
  if ($entry.FullName -match '^lib/([^/]+)/') {
    $abi = $matches[1]
    if (-not $sos.ContainsKey($abi)) { $sos[$abi] = @() }
    $sos[$abi] += [pscustomobject]@{ Path = $entry.FullName; Size = $entry.Length }
  }
}
$zip.Dispose()

# Print summary
foreach ($abi in ($sos.Keys | Sort-Object)) {
  $total = ($sos[$abi] | Measure-Object -Sum Size).Sum
  Add-Content $out ("  [{0}] {1} libraries, {2} bytes total" -f $abi, $sos[$abi].Count, $total)
  foreach ($lib in ($sos[$abi] | Sort-Object Path)) {
    Add-Content $out ("    {0} ({1} bytes)" -f ($lib.Path.Substring(("lib/{0}/" -f $abi).Length)), $lib.Size)
  }
}

Add-Content $out ""
Add-Content $out "=== ABI coverage matrix ==="
$expectedAbis = @('arm64-v8a','armeabi-v7a','x86','x86_64')
foreach ($abi in $expectedAbis) {
  if ($sos.ContainsKey($abi)) {
    Add-Content $out ("[+] ${abi}: $($sos[$abi].Count) libs")
  } else {
    Add-Content $out "[-] ${abi}: MISSING"
  }
}

Add-Content $out ""
Add-Content $out "=== Host architecture cross-check ==="
# What can our emulator target?
$expectedEmulatorAbi = 'x86_64'
if ($sos.ContainsKey($expectedEmulatorAbi)) {
  $count = $sos[$expectedEmulatorAbi].Count
  Add-Content $out ("[+] emulator ABI $expectedEmulatorAbi available ($count libs) — Android x86_64 emulator will work with this APK once emulator can boot")
} else {
  Add-Content $out "[-] emulator ABI $expectedEmulatorAbi MISSING — emulator wouldn't be able to load native libs"
}

Write-Host "DONE - $out"
