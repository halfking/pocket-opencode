$x = $env:LOCALAPPDATA
"--- LocalAppData top dirs ---"
Get-ChildItem -Path $x -ErrorAction SilentlyContinue | Select-Object Name | Format-Table -AutoSize
"--- Program Files android ---"
Get-ChildItem -Path 'C:\Program Files' -ErrorAction SilentlyContinue | Select-Object Name | Format-Table -AutoSize
"--- android on PATH ---"
Get-Command android -ErrorAction SilentlyContinue | Select-Object Name, Source
"--- sdkmanager on PATH ---"
Get-Command sdkmanager -ErrorAction SilentlyContinue | Select-Object Name, Source
