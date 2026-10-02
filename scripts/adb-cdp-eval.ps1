# adb-cdp-eval.ps1 -- read or run JS in a device WebView over CDP
#
# 为什么需要它（2026-10-02 真机实测）：
#   1. MIUI/HyperOS 默认关闭「开发者选项 → USB 安装」，Maestro 装不上驱动
#      App（dev.mobile.maestro），`adb install` 一律 INSTALL_FAILED_USER_RESTRICTED。
#   2. 退一步用 uiautomator dump 也不行：这个 WebView 的 a11y 树未暴露，
#      dump 只吐 8 个空节点，一个字都读不到，文本断言无从下手。
#   3. CDP 通道反而是通的（WebView 126 实测 /json/version 正常），
#      Runtime.evaluate 能读到真实渲染文本 —— 对「用户看到什么」这类
#      断言，它比 uiautomator 更贴近。
#
# 坐标换算（实测校准过）：CSS 像素 × devicePixelRatio = 设备像素。
#   本机 360×820 CSS / dpr 2 / 720×1640，无状态栏偏移。
#
# 用法（PowerShell 5.1 下 ExecutionPolicy 会拦 .ps1，请用调用运算符在
# 已加载脚本的会话里跑，或先 Set-ExecutionPolicy）：
#   $code = [IO.File]::ReadAllText('scripts/adb-cdp-eval.ps1')
#   $cdp  = [ScriptBlock]::Create($code)
#   & $cdp -Expression "document.body.innerText"
#
# 刻意不加 Add-Type -AssemblyName System.Net.WebSockets：该程序集只存在于
# .NET Core。5.1 上 ClientWebSocket 本来就在 System.dll 里，而这条 Add-Type
# 是 non-terminating error —— 脚本会继续往下跑并打印空结果，制造假绿。
param(
  [string]$Expression,
  [int]$Port = 9333,
  [switch]$Raw
)
$ErrorActionPreference = 'Stop'

$json = Invoke-RestMethod -Uri "http://127.0.0.1:$Port/json" -TimeoutSec 10
$page = $json | Where-Object { $_.type -eq 'page' } | Select-Object -First 1
if (-not $page) { throw "no page target on port $Port (is 'adb forward tcp:$Port localabstract:webview_devtools_remote_<pid>' set up?)" }
$url = [Uri]$page.webSocketDebuggerUrl

$ws = New-Object System.Net.WebSockets.ClientWebSocket
$cts = New-Object System.Threading.CancellationTokenSource
[void]$ws.ConnectAsync($url, $cts.Token).GetAwaiter().GetResult()

function Send-Text([string]$text) {
  $bytes = [Text.Encoding]::UTF8.GetBytes($text)
  $seg = New-Object System.ArraySegment[byte] (,$bytes)
  $task = $ws.SendAsync($seg, [System.Net.WebSockets.WebSocketMessageType]::Text, $true, $cts.Token)
  [void]$task.GetAwaiter().GetResult()
  return
}
function Recv-Text {
  # A single ReceiveAsync can return a fragment; loop until EndOfMessage.
  $buf = New-Object byte[] 65536
  $ms = New-Object IO.MemoryStream
  while ($true) {
    $seg = New-Object System.ArraySegment[byte] (,$buf)
    $r = $ws.ReceiveAsync($seg, $cts.Token).GetAwaiter().GetResult()
    $ms.Write($buf, 0, $r.Count)
    if ($r.EndOfMessage) { break }
  }
  [Text.Encoding]::UTF8.GetString($ms.ToArray())
}

$payload = @{ id = 1; method = 'Runtime.evaluate'; params = @{ expression = $Expression; returnByValue = $true; awaitPromise = $true } } | ConvertTo-Json -Depth 8 -Compress
Send-Text $payload
while ($true) {
  $resp = Recv-Text
  if ($resp -match '"id":1') { break }
}
$ws.Dispose()

if ($Raw) { $resp }
else {
  # Print only the value so callers do not have to strip the CDP envelope.
  # Leaving the envelope in place is how an empty result reads as a pass.
  $m = [regex]::Match($resp, '"result":\{"result":\{.*?"value":(".*?")\}\}?\s*\}\s*$')
  if (-not $m.Success) { $resp }
  else {
    # NB: must not be called $raw -- PowerShell is case-insensitive, so that
    # would collide with the -Raw switch parameter and fail the cast.
    $literal = $m.Groups[1].Value
    if ($literal.StartsWith('"')) {
      $s = $literal.Substring(1, $literal.Length - 2)
      # -replace with a scriptblock is a PS7 feature; on 5.1 it emits the
      # scriptblock source verbatim. Use the MatchEvaluator overload.
      $s = [regex]::Replace($s, '\\u([0-9a-fA-F]{4})', [System.Text.RegularExpressions.MatchEvaluator]{ param($mm) [char][Convert]::ToInt32($mm.Groups[1].Value, 16) })
      $s = $s -replace '\\n', "`n" -replace '\\"','"'
      $s
    } else {
      $literal
    }
  }
}
