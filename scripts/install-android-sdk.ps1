$env:JAVA_HOME='C:\Program Files\AdoptOpenJDK\jdk-17.0.0.20-hotspot'
$src = 'C:\workspace\openpocket\downloads\cmdline-tools.zip'
$androidRoot = "$env:LOCALAPPDATA\Android"
$cmdlineRoot = "$androidRoot\cmdline-tools"
$latest = "$cmdlineRoot\latest"

"--- step 1: ensure dirs ---"
New-Item -ItemType Directory -Force -Path $androidRoot | Out-Null
New-Item -ItemType Directory -Force -Path $cmdlineRoot | Out-Null

"--- step 2: extract ---"
Add-Type -AssemblyName System.IO.Compression.FileSystem
[System.IO.Compression.ZipFile]::ExtractToDirectory($src, $cmdlineRoot)

"--- step 3: rename cmdline-tools subfolder to 'latest' ---"
$subdirs = Get-ChildItem -Path $cmdlineRoot -Directory
foreach ($d in $subdirs) {
  if ($d.Name -like 'cmdline-tools*' -and $d.FullName -ne $latest) {
    "Renaming: $($d.Name) -> latest"
    $temp = "$androidRoot\__extract_tmp"
    Get-ChildItem -Path $d.FullName -Force | Move-Item -Destination $latest -Force
    Remove-Item $d.FullName -Recurse -Force
  }
}

"--- step 4: find sdkmanager.exe ---"
$sdkMgr = Get-ChildItem "$latest\bin" -Filter 'sdkmanager*' -ErrorAction SilentlyContinue | Select-Object -First 1 -ExpandProperty FullName
"sdkmanager: $sdkMgr"

"--- step 5: persist ANDROID_HOME / PATH ---"
[Environment]::SetEnvironmentVariable('ANDROID_HOME', $androidRoot, 'User')
[Environment]::SetEnvironmentVariable('ANDROID_SDK_ROOT', $androidRoot, 'User')
$userPath = [Environment]::GetEnvironmentVariable('PATH', 'User')
$parts = @($userPath -split ';') | Where-Object { $_ }
$needed = @("$latest\bin", "$androidRoot\platform-tools", "$androidRoot\emulator")
foreach ($n in $needed) { if ($parts -notcontains $n) { $parts += $n } }
[Environment]::SetEnvironmentVariable('PATH', ($parts -join ';'), 'User')

"--- final ---"
"ANDROID_HOME = $androidRoot"
"sdkmanager = $sdkMgr"
Get-Content 'function:Prompt' 2>$null
