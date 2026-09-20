$latest = 'C:\Users\86133\AppData\Local\Android\cmdline-tools\latest'
$bin = "$latest\bin"
"Source: $latest"
"Target bin: $bin"

New-Item -ItemType Directory -Force -Path $bin | Out-Null
Get-ChildItem -Path $latest -File | Move-Item -Destination $bin -Force
if (Test-Path "$latest\lib") {
  Move-Item "$latest\lib" -Destination "$latest\lib" -Force 2>&1
}
"-- after --"
Get-ChildItem $latest | Select-Object Name
Get-ChildItem $bin | Where-Object { $_.Name -like 'sdkmanager*' -or $_.Name -like 'avdmanager*' } | Select-Object FullName
