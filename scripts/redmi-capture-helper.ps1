# redmi-capture-helper.ps1 — 简单的 adb screencap 帮助脚本
param([string]$Out = "C:\workspace\openpocket\logs\redmi-shot.png")
$env:PATH = "C:\Users\86133\AppData\Local\Android\platform-tools;$env:PATH"

# 用 Start-Process + 重定向把二进制 PNG 写到磁盘
$proc = Start-Process -FilePath "adb.exe" -ArgumentList @("-s","4c308e2e","exec-out","screencap","-p") `
                     -NoNewWindow -PassThru -RedirectStandardOutput $Out -Wait
Write-Host "Saved: $Out (exit $($proc.ExitCode))"
Get-Item $Out | Select-Object Name, Length | Format-Table -AutoSize
