# webview-eval.ps1 — Use Chrome DevTools Protocol via WebSocket to evaluate JS in the WebView
# Usage: powershell -ExecutionPolicy Bypass -File scripts\webview-eval.ps1 -Expression "window.location.hash = '/ai-chat'"
param(
    [Parameter(Mandatory=$true)][string]$Expression
)
$env:PATH = "$env:LOCALAPPDATA\Android\platform-tools;$env:PATH"

# Find the WebView page
$pagesJson = (Invoke-WebRequest -Uri "http://localhost:9222/json" -UseBasicParsing -TimeoutSec 5).Content
$pages = $pagesJson | ConvertFrom-Json
$page = $pages | Where-Object { $_.type -eq "page" } | Select-Object -First 1
if (-not $page) {
    Write-Error "No WebView page found"
    exit 1
}
$wsUrl = $page.webSocketDebuggerUrl
Write-Output "Page: $($page.title) ($($page.url))"
Write-Output "WS: $wsUrl"

# Use .NET WebSocket client
Add-Type -AssemblyName System.Net.WebSockets
Add-Type -AssemblyName System.Net
$client = New-Object System.Net.WebSockets.ClientWebSocket
$uri = [Uri]$wsUrl
$connectTask = $client.ConnectAsync($uri, [System.Threading.CancellationToken]::None)
$connectTask.Wait() | Out-Null
Write-Output "Connected"

# Send Runtime.evaluate
$req = @{
    id = 1
    method = "Runtime.evaluate"
    params = @{
        expression = $Expression
        returnByValue = $true
        awaitPromise = $true
    }
} | ConvertTo-Json -Depth 5
$bytes = [System.Text.Encoding]::UTF8.GetBytes($req)
$sendTask = $client.SendAsync([System.Array]::CreateInstance([byte], $bytes.Length), [System.Net.WebSockets.WebSocketMessageType]::Text, $true, [System.Threading.CancellationToken]::None)
$sendTask.Wait() | Out-Null

# Read response
$buffer = New-Object byte[] 65536
$result = $client.ReceiveAsync([System.Array]::CreateInstance([byte], $buffer.Length), [System.Threading.CancellationToken]::None)
$result.Wait() | Out-Null
$count = $result.Result.Count
$response = [System.Text.Encoding]::UTF8.GetString($buffer, 0, $count)
Write-Output "Response:"
Write-Output $response

$client.CloseAsync([System.Net.WebSockets.WebSocketCloseStatus]::NormalClosure, "bye", [System.Threading.CancellationToken]::None).Wait() | Out-Null