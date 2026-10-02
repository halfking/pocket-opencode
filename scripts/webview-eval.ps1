# webview-eval.ps1 - evaluate JS inside the Android WebView via Chrome DevTools Protocol.
#
# Prerequisites:
#   adb -s <serial> forward tcp:9222 localabstract:webview_devtools_remote_<appPid>
#   (appPid = pidof the app package)
#
# Usage:
#   powershell -ExecutionPolicy Bypass -File scripts\webview-eval.ps1 -Expression "location.hash='#/email'"
#
# IMPORTANT - how to pass the expression:
#   `powershell -File` re-parses the child command line, so an -Expression value
#   containing spaces or newlines arrives split into several arguments (it lands
#   in -Port, -TimeoutSec, ...). To evaluate a multi-line or spaced expression,
#   either call this script in-process with the call operator
#       & .\scripts\webview-eval.ps1 -Expression $e
#   or keep the expression on one line with no spaces.
#   Note that in-process invocation means this script's `exit` terminates YOUR
#   shell on error; that is intended for the `powershell -File` form.
#
# Two bugs in the previous version of this script, both of which made it look
# like it worked while silently returning nothing:
#
#   1. `Add-Type -AssemblyName System.Net.WebSockets` does not exist on
#      .NET Framework. ClientWebSocket lives in System.dll, which is always
#      already loaded, so no Add-Type is needed at all. That name is the
#      .NET Core assembly. The Add-Type emitted a non-terminating error and
#      execution continued, so the script "succeeded" anyway.
#   2. A single ReceiveAsync is not a complete message. CDP replies can be
#      split across frames; reading once returned a partial (here: blank)
#      buffer and the script printed it as if it were the result.
param(
    [Parameter(Mandatory = $true)][string]$Expression,
    [int]$Port = 9222,
    [int]$TimeoutSec = 30
)
$ErrorActionPreference = 'Stop'

# --- locate the WebView page -------------------------------------------------
try {
    $pages = (Invoke-WebRequest -Uri "http://localhost:$Port/json" -UseBasicParsing -TimeoutSec 8).Content |
        ConvertFrom-Json
} catch {
    Write-Error "No CDP endpoint on port $Port. Run: adb -s <serial> forward tcp:$Port localabstract:webview_devtools_remote_<appPid>"
    exit 1
}
$page = $pages | Where-Object { $_.type -eq 'page' } | Select-Object -First 1
if (-not $page) {
    Write-Error "No WebView page found on port $Port. Is `adb forward` set up?"
    exit 1
}
Write-Output "Page: $($page.title) ($($page.url))"

# --- connect -----------------------------------------------------------------
# ClientWebSocket is in System.dll; deliberately no Add-Type here.
$ws = New-Object System.Net.WebSockets.ClientWebSocket
$cts = New-Object System.Threading.CancellationTokenSource
$cts.CancelAfter($TimeoutSec * 1000)
try {
    $ws.ConnectAsync([Uri]$page.webSocketDebuggerUrl, $cts.Token).Wait()
} catch {
    Write-Error "CDP WebSocket connect failed: $($_.Exception.Message)"
    exit 1
}

# --- send Runtime.evaluate ---------------------------------------------------
$request = @{
    id      = 1
    method  = 'Runtime.evaluate'
    params  = @{
        expression  = $Expression
        returnByValue = $true
        awaitPromise  = $true
    }
} | ConvertTo-Json -Depth 6 -Compress
$bytes = [System.Text.Encoding]::UTF8.GetBytes($request)
$segment = New-Object System.ArraySegment[byte] -ArgumentList @(,$bytes)
$ws.SendAsync($segment, [System.Net.WebSockets.WebSocketMessageType]::Text, $true, $cts.Token).Wait()

# --- read until EndOfMessage (a single receive is NOT the whole reply) -------
$buffer = New-Object byte[] 65536
$stream = New-Object System.IO.MemoryStream
$recvSegment = New-Object System.ArraySegment[byte] -ArgumentList @(,$buffer)
try {
    $receive = $ws.ReceiveAsync($recvSegment, $cts.Token)
    $receive.Wait()
    $result = $receive.Result
    if ($result.Count -gt 0) { $stream.Write($buffer, 0, $result.Count) }
    while (-not $result.EndOfMessage) {
        $receive = $ws.ReceiveAsync($recvSegment, $cts.Token)
        $receive.Wait()
        $result = $receive.Result
        if ($result.Count -gt 0) { $stream.Write($buffer, 0, $result.Count) }
    }
} catch {
    Write-Error "CDP receive failed: $($_.Exception.Message)"
    exit 1
}
$raw = [System.Text.Encoding]::UTF8.GetString($stream.ToArray())
if ([string]::IsNullOrWhiteSpace($raw)) {
    Write-Error "Empty CDP response - the WebSocket closed without answering."
    exit 1
}

# --- report ------------------------------------------------------------------
try {
    $message = $raw | ConvertFrom-Json
} catch {
    Write-Error "CDP response is not JSON: $raw"
    exit 1
}
if ($message.result -and $message.result.exceptionDetails) {
    Write-Error "EXCEPTION: $($message.result.exceptionDetails.text) $($message.result.exceptionDetails.exception.description)"
    exit 1
}
$value = $message.result.result.value
if ($null -eq $value) {
    # Not necessarily an error: Runtime.evaluate legitimately returns
    # undefined for statements. Say so instead of pretending we got a value.
    Write-Output "<undefined> (raw: $raw)"
    exit 0
}
if ($value -is [string]) { Write-Output $value } else { Write-Output ($value | ConvertTo-Json -Depth 12) }

$ws.Dispose()
exit 0
