$loc = $env:LOCALAPPDATA
"LocalAppData: $loc"
Get-ChildItem -Path "$loc" -Directory -ErrorAction SilentlyContinue | ForEach-Object {
  if ($_.Name -match "android" -or $_.Name -match "cmdline") { $_.FullName }
}
Get-ChildItem -Path "C:\Program Files" -Directory -ErrorAction SilentlyContinue | ForEach-Object {
  if ($_.Name -match "android" -or $_.Name -match "cmdline") { $_.FullName }
}
Get-ChildItem -Path "C:\Program Files (x86)" -Directory -ErrorAction SilentlyContinue | ForEach-Object {
  if ($_.Name -match "android" -or $_.Name -match "cmdline") { $_.FullName }
}
$cmd = (Get-Command android -ErrorAction SilentlyContinue).Source
"android command: $cmd"
$cmd2 = (Get-Command sdkmanager -ErrorAction SilentlyContinue).Source
"sdkmanager command: $cmd2"
