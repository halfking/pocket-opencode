$UserPath = [Environment]::GetEnvironmentVariable('PATH', 'User')
$paths = $UserPath -split ';'
"--- User PATH entries ---"
foreach ($p in $paths) { if ($p -match 'android' -or $p -match 'sdk') { $p } }
"--- searching common locations ---"
$locs = @(
  'C:\Program Files\Android\Android Studio',
  'C:\Program Files\Android\Android Studio1',
  'C:\Program Files\Android\Commandline Tools',
  'C:\Program Files\AndroidSDK',
  'C:\Program Files\Android Cmdline Tools',
  'C:\Program Files (x86)\Android',
  'C:\Android',
  "$env:LOCALAPPDATA\Android",
  "$env:LOCALAPPDATA\AndroidSDK",
  "$env:LOCALAPPDATA\google\AndroidCLI"
)
foreach ($l in $locs) { if (Test-Path $l) { "EXISTS: $l" } }
"--- where.exe ---"
$res = & where.exe 'android.exe' 2>&1
$res
