$env:JAVA_HOME = 'C:\Program Files\AdoptOpenJDK\jdk-17.0.0.20-hotspot'
$env:ANDROID_HOME = "$env:LOCALAPPDATA\Android"
$env:PATH = "$env:JAVA_HOME\bin;$env:ANDROID_HOME\cmdline-tools\latest\bin;$env:PATH"

$sb = New-Object System.Text.StringBuilder
for ($i = 0; $i -lt 12; $i++) {
  [void]$sb.AppendLine("y")
}

$yes = $sb.ToString()
$yes | & "$env:ANDROID_HOME\cmdline-tools\latest\bin\sdkmanager.bat" --licenses 2>&1 | Select-Object -Last 10
