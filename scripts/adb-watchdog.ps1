# adb-watchdog.ps1 — Keep adb alive on MIUI/HyperOS which aggressively disconnects
# Usage: powershell -ExecutionPolicy Bypass -File scripts\adb-watchdog.ps1
$env:PATH = "$env:LOCALAPPDATA\Android\platform-tools;$env:PATH"
$REDMISERIAL = "4c308e2e"
$WATCHDOG_INTERVAL = 15  # seconds

Write-Output "=== adb watchdog started @ $(Get-Date -Format 'HH:mm:ss') for $REDMISERIAL ==="

function Invoke-Adb {
    param([string]$Cmd)
    $attempt = 0
    while ($attempt -lt 3) {
        try {
            $result = Invoke-Expression "adb -s $REDMISERIAL $Cmd 2>&1" 2>&1
            if ($LASTEXITCODE -ne 0) {
                throw "Exit code $LASTEXITCODE"
            }
            return $result
        } catch {
            $attempt++
            Write-Warning "adb $Cmd failed (attempt $attempt): $_"
            adb kill-server | Out-Null
            Start-Sleep -Seconds 1
            adb start-server | Out-Null
            Start-Sleep -Seconds 3
        }
    }
    return $null
}

while ($true) {
    $devices = adb devices 2>&1
    if ($devices -match "$REDMISERIAL\tdevice") {
        # Heartbeat
        $alive = Invoke-Adb "shell echo ping_$(Get-Date -Format 'HHmmss')"
        if ($alive) {
            Write-Output "[$(Get-Date -Format 'HH:mm:ss')] heartbeat: $alive"
        }
    } else {
        Write-Warning "[$(Get-Date -Format 'HH:mm:ss')] device offline, restarting adb"
        adb kill-server | Out-Null
        Start-Sleep -Seconds 2
        adb start-server | Out-Null
        Start-Sleep -Seconds 4
    }
    Start-Sleep -Seconds $WATCHDOG_INTERVAL
}