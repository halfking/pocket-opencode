$work = Join-Path $env:TEMP 'apk-dex-extract'

$needles = @('AppSettingsPlugin','AudioDeviceRank','PermissionSettingsLauncher','MainActivity','MainApplication','com/kaixuan/opencode/pocket/plugins/')

"--- fast scan all DEX files (raw byte read) ---"
foreach ($needle in $needles) {
  $found = $false
  foreach ($dex in (Get-ChildItem $work -Recurse -Filter '*.dex')) {
    $content = [System.IO.File]::ReadAllBytes($dex.FullName)
    $text = [System.Text.Encoding]::ASCII.GetString($content)
    if ($text -match [regex]::Escape($needle)) { $found = $true; break }
  }
  if ($found) { "[+] FOUND in some DEX: $needle" }
  else        { "[-] NOT FOUND: $needle" }
}
