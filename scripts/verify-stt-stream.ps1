# verify-stt-stream.ps1 — 即时/全量转写端点的黑盒验证（2026-10-01）
#
# 为什么单测不够：本轮新增的 /api/stt/transcribe-full 与
# /api/stt/transcribe-incremental 走的是**真实 HTTP 链路**——鉴权中间件、
# 请求体上限中间件、JSON 解码、目标解析、multipart 上行、会话状态机。
# httptest 单测只覆盖 handler 内部，一旦某个中间件把请求挡掉，handler 的
# 单测照样全绿。所以必须起一个真的 pocketd 进程打真的端口。
#
# 为什么用本地假 ASR：真正能用的外部 ASR key 在本机不可达
# （api.openai.com i/o timeout），网关侧 2026-10-01 实测一个 ASR 上游都没有。
# 假 ASR 让本脚本能验证「链路正确」——这正是本轮改动风险最大的部分。
# **注意：这不能证明识别质量**，设置页的「录 3 秒试转」才是干这个的。
#
# 用法：powershell -ExecutionPolicy Bypass -File scripts/verify-stt-stream.ps1
param(
  [string]$Port = "18101",
  [switch]$SkipBuild
)

$ErrorActionPreference = 'Stop'
$script:pass = 0
$script:fail = 0
function Ok($msg) { $script:pass++; Write-Host "  [PASS] $msg" -ForegroundColor Green }
function Bad($msg) { $script:fail++; Write-Host "  [FAIL] $msg" -ForegroundColor Red }
function Section($msg) { Write-Host "`n== $msg ==" -ForegroundColor Cyan }
function Check($cond, $okMsg, $badMsg) { if ($cond) { Ok $okMsg } else { Bad $badMsg } }

$root = Split-Path -Parent $PSScriptRoot
Write-Host "verify-stt-stream — 真实进程的即时/全量转写验证" -ForegroundColor Cyan

# ---------------------------------------------------------------------------
# 1. 起一个假 ASR 上游（OpenAI 兼容 /audio/transcriptions）
# ---------------------------------------------------------------------------
# 用独立 node 进程而不是 PowerShell 的 HttpListener/ThreadJob：
# PS 5.1 没有 Start-ThreadJob 模块，而 node 是本仓库既有工具链的一部分。
$fakePort = 18102
$fakeBase = "http://127.0.0.1:$fakePort/v1"
$fakeAsr = Start-Process -FilePath 'node' -ArgumentList @(
  (Join-Path $PSScriptRoot 'fake-asr.mjs'), "$fakePort"
) -PassThru -WindowStyle Hidden `
  -RedirectStandardOutput (Join-Path $env:TEMP 'fake-asr.out.log') `
  -RedirectStandardError (Join-Path $env:TEMP 'fake-asr.err.log')

$fakeReady = $false
for ($i = 0; $i -lt 20; $i++) {
  Start-Sleep -Milliseconds 300
  try { Invoke-RestMethod "http://127.0.0.1:$fakePort/__calls" -TimeoutSec 2 | Out-Null; $fakeReady = $true; break } catch { }
}
if (-not $fakeReady) {
  Stop-Process -Id $fakeAsr.Id -Force -ErrorAction SilentlyContinue
  Get-Content (Join-Path $env:TEMP 'fake-asr.err.log') -Tail 10 -ErrorAction SilentlyContinue
  throw "假 ASR 未就绪"
}
Ok "假 ASR 上游已起 ($fakeBase)"

# ---------------------------------------------------------------------------
# 2. 构建并启动 pocketd
# ---------------------------------------------------------------------------
$bin = Join-Path $root 'backend\pocketd.exe'
if (-not $SkipBuild) {
  Write-Host "构建 pocketd ..."
  Push-Location (Join-Path $root 'backend')
  go build -o pocketd.exe ./cmd/pocketd
  Pop-Location
}
if (-not (Test-Path $bin)) { Stop-Process -Id $fakeAsr.Id -Force -ErrorAction SilentlyContinue; throw "pocketd 未构建" }

$dataDir = Join-Path $root ".verify-stt-stream-data"
New-Item -ItemType Directory -Force -Path $dataDir | Out-Null
$env:POCKET_HTTP_PORT = $Port
$env:POCKET_DEV_AUTH = "true"
$env:POCKET_ENV = "development"
$env:POCKET_DATA_DIR = $dataDir
$env:POCKET_DB_PATH = (Join-Path $dataDir 'pocket.sqlite')
$env:POCKET_AUTH_LEGACY_ONLY = "true"
# 2026-10-02: no built-in default password. The old one was committed in clear
# text in this file, so anyone with the repo could log in as admin on a dev
# instance. Pass it in explicitly; abort loudly rather than silently skipping.
if (-not $env:POCKET_AUTH_PASS) {
  Write-Host "ABORT: set POCKET_AUTH_PASS before running this script." -ForegroundColor Red
  exit 2
}
$env:POCKET_AUTH_USER = "admin"
# 外部 ASR 指向 loopback 假上游：validateGatewayURL 默认拒私网（防 SSRF），
# 这里显式 opt-in。这正是设置页保存 loopback 地址时的行为。
$env:POCKET_LLM_GATEWAY_ALLOW_PRIVATE = "true"

$proc = Start-Process -FilePath $bin -WorkingDirectory (Join-Path $root 'backend') `
  -RedirectStandardOutput (Join-Path $dataDir 'pocketd.out.log') `
  -RedirectStandardError (Join-Path $dataDir 'pocketd.err.log') `
  -PassThru -WindowStyle Hidden
$base = "http://127.0.0.1:$Port"

try {
  $ready = $false
  for ($i = 0; $i -lt 40; $i++) {
    Start-Sleep -Milliseconds 500
    try { Invoke-RestMethod "$base/healthz" -TimeoutSec 2 | Out-Null; $ready = $true; break } catch { }
  }
  if (-not $ready) {
    Get-Content (Join-Path $dataDir 'pocketd.err.log') -Tail 20 -ErrorAction SilentlyContinue
    throw "pocketd 未就绪"
  }
  Ok "pocketd 就绪 ($base)"

  $loginBody = @{ username = 'admin'; password = $env:POCKET_AUTH_PASS } | ConvertTo-Json
  $token = (Invoke-RestMethod "$base/api/auth/login" -Method Post -Body $loginBody -ContentType 'application/json').token
  $hdr = @{ Authorization = "Bearer $token" }
  Ok "登录取到 token"

  # ---------- 3. 配置指向假上游 ----------
  Section "PUT /api/stt/config（指向假上游）"
  $put = @{
    channel = 'external'
    gatewayModel = ''
    externalBaseURL = $fakeBase
    externalModel = 'gpt-4o-mini-transcribe'
    externalTransport = 'transcriptions'
    externalApiKey = 'test-key'
  } | ConvertTo-Json
  # 出错时把响应体打出来：400 的原因在 body 里，PowerShell 默认只给状态码，
  # 不打出来就只能靠猜（本轮就在这上面卡过一次）。
  $saved = $null
  try {
    $saved = Invoke-RestMethod "$base/api/stt/config" -Method Put -Body $put -Headers $hdr -ContentType 'application/json'
  } catch {
    # ErrorDetails.Message 在 WebException 上带响应体，比自己读流可靠
    # （流可能被 PowerShell 消费掉，读出来是空 —— 本轮就在这上面空转过一次）。
    $detail = $_.ErrorDetails.Message
    if (-not $detail) { $detail = $_.Exception.Message }
    Bad "保存设置失败: $detail"
  }
  if ($saved) {
    Check ($saved.externalModel -eq 'gpt-4o-mini-transcribe') "外部模型已保存" "外部模型未保存: $($saved.externalModel)"
    # 密钥不得回显。
    #
    # 注意括号：PowerShell 里 `-not $x -match 'p'` 会被解析成
    # `(-not $x) -match 'p'`（一元运算符比比较运算符结合更紧），
    # 于是判断的其实是「JSON 串非空」——永远为真，于是**每次都误报泄露**。
    # 这个坑让本轮先误判了一次「响应泄露明文 key」，改成精确断言后才确认
    # 响应里只有 hasExternalKey: true。
    $json = $saved | ConvertTo-Json -Depth 5
    $leaks = [regex]::IsMatch($json, 'test-key')
    Check (-not $leaks) "key 未回显（响应只有 hasExternalKey）" "响应里泄露了明文 key: $json"
    Check ($saved.hasExternalKey -eq $true) "hasExternalKey 为 true" "hasExternalKey 未置位"
    # 显式断言：不存在任何承载明文 key 的字段
    $hasPlainField = ($saved.PSObject.Properties.Name | Where-Object { $_ -match 'key|secret|token' -and $_ -ne 'hasExternalKey' }).Count -gt 0
    Check (-not $hasPlainField) "响应无承载明文 key 的字段" "响应含疑似明文凭据字段"
  }

  # ---------- 4. 造一段 60 秒 16bit PCM WAV（超过 25 秒切分阈值）----------
  function New-ToneWav([int]$seconds, [int]$sampleRate = 16000) {
    $n = $sampleRate * $seconds
    $pcm = New-Object byte[] ($n * 2)
    for ($i = 0; $i -lt $n; $i++) {
      $v = [int16](3000 * [Math]::Sin($i * 0.05))
      $b = [BitConverter]::GetBytes($v)
      $pcm[$i * 2] = $b[0]; $pcm[$i * 2 + 1] = $b[1]
    }
    $ms = New-Object System.IO.MemoryStream
    $bw = New-Object System.IO.BinaryWriter($ms)
    $byteRate = $sampleRate * 2
    $bw.Write([Text.Encoding]::ASCII.GetBytes('RIFF'))
    $bw.Write([uint32](36 + $pcm.Length))
    $bw.Write([Text.Encoding]::ASCII.GetBytes('WAVE'))
    $bw.Write([Text.Encoding]::ASCII.GetBytes('fmt '))
    $bw.Write([uint32]16); $bw.Write([uint16]1); $bw.Write([uint16]1)
    $bw.Write([uint32]$sampleRate); $bw.Write([uint32]$byteRate)
    $bw.Write([uint16]2); $bw.Write([uint16]16)
    $bw.Write([Text.Encoding]::ASCII.GetBytes('data'))
    $bw.Write([uint32]$pcm.Length)
    $bw.Write($pcm)
    $bw.Flush()
    $ms.ToArray()
  }

  # ---------- 5. 全量转写 ----------
  Section "POST /api/stt/transcribe-full（60 秒长音频）"
  $longWav = [Convert]::ToBase64String((New-ToneWav 60))
  $fullBody = @{ audioBase64 = $longWav; filename = 'long.wav' } | ConvertTo-Json
  $full = Invoke-RestMethod "$base/api/stt/transcribe-full" -Method Post -Body $fullBody -Headers $hdr -ContentType 'application/json' -TimeoutSec 300
  Check ($full.ok -eq $true) "全量转写返回 ok=true（error=$($full.error)）" "全量转写失败: $($full.error)"
  Check ($full.succeeded -ge 2) "60 秒音频被切成多段并全部成功 (succeeded=$($full.succeeded))" "未切成多段: succeeded=$($full.succeeded)"
  Check (@($full.segments).Count -ge 2) "响应里含逐段明细" "响应缺少 segments 明细"
  # 逐段聚合：每段文本都应出现在聚合结果里，且顺序与 segments 一致。
  # 用段序号而不是中文字面量做断言，避免验证脚本自身的编码问题
  # 把「客户端解码失败」误报成「聚合丢字」。
  $segmentTexts = @($full.segments | Where-Object { $_.text } | ForEach-Object { $_.text })
  $allPresent = $true
  foreach ($t in $segmentTexts) { if ($full.text -notlike "*$t*") { $allPresent = $false } }
  Check ($allPresent) "聚合文本含每一段的转写结果" "聚合文本缺少某些段"
  Check ($segmentTexts.Count -ge 2) "多数段都有转写文本 ($($segmentTexts.Count) 段)" "有段没转写出文本"
  $ordered = $true
  for ($i = 1; $i -lt @($full.segments).Count; $i++) {
    if ($full.segments[$i].startSec -lt $full.segments[$i - 1].startSec) { $ordered = $false }
  }
  Check $ordered "各段按时间顺序" "各段顺序错乱"
  # 每段发给上游的音频都必须在最紧的上游限制（30 秒）内
  $tooLong = @($full.segments | Where-Object { ($_.endSec - $_.startSec) -gt 30.5 })
  Check ($tooLong.Count -eq 0) "没有超过 30 秒的段（不会撞上游上限）" "$($tooLong.Count) 段超过 30 秒"

  # ---------- 6. 即时转写：跨片累积 ----------
  Section "POST /api/stt/transcribe-incremental（分段增量 + 跨片去重）"
  $chunkB64 = [Convert]::ToBase64String((New-ToneWav 5))
  $sess = "verify-$([DateTimeOffset]::UtcNow.ToUnixTimeSeconds())"
  $i1 = Invoke-RestMethod "$base/api/stt/transcribe-incremental" -Method Post -Headers $hdr -ContentType 'application/json' `
    -Body (@{ audioBase64 = $chunkB64; sessionId = $sess; filename = 'c.wav'; startSec = 0; endSec = 5; silenceCut = $false } | ConvertTo-Json)
  Check ($i1.ok -eq $true) "第 1 片成功" "第 1 片失败: $($i1.error)"
  Check ([bool]$i1.text) "第 1 片返回文本" "第 1 片文本为空"

  $i2 = Invoke-RestMethod "$base/api/stt/transcribe-incremental" -Method Post -Headers $hdr -ContentType 'application/json' `
    -Body (@{ audioBase64 = $chunkB64; sessionId = $sess; filename = 'c.wav'; startSec = 5; endSec = 10; silenceCut = $false; isFinal = $true } | ConvertTo-Json)
  Check ($i2.text -match [regex]::Escape($i1.text)) "第 2 片保留第 1 片内容（累积而非替换）" "第 2 片丢失了第 1 片内容"
  Check ($i2.isFinal -eq $true) "IsFinal 透传" "IsFinal 未透传"

  # 会话隔离：新 sessionId 必须是独立累积
  $sess2 = "$sess-b"
  $i3 = Invoke-RestMethod "$base/api/stt/transcribe-incremental" -Method Post -Headers $hdr -ContentType 'application/json' `
    -Body (@{ audioBase64 = $chunkB64; sessionId = $sess2; filename = 'c.wav'; startSec = 0; endSec = 5; silenceCut = $false; isFinal = $true } | ConvertTo-Json)
  Check ($i3.text -ne $i2.text) "新会话从零开始（不串台）" "新会话沿用了旧会话的文本"

  # 缺 sessionId 必须被拒
  $noSess = $null
  try {
    Invoke-RestMethod "$base/api/stt/transcribe-incremental" -Method Post -Headers $hdr -ContentType 'application/json' `
      -Body (@{ audioBase64 = $chunkB64 } | ConvertTo-Json) -ErrorAction Stop | Out-Null
  } catch { $noSess = $true }
  Check $noSess "缺 sessionId 被拒（否则去重失效）" "缺 sessionId 未被拒"

  # ---------- 7. 未认证必须被拒 ----------
  Section "鉴权"
  $unauth = $null
  try {
    Invoke-RestMethod "$base/api/stt/transcribe-full" -Method Post -Body $fullBody -ContentType 'application/json' -ErrorAction Stop | Out-Null
  } catch { $unauth = $true }
  Check $unauth "/api/stt/transcribe-full 未认证被拒" "/api/stt/transcribe-full 未认证竟然放行"

  $unauth2 = $null
  try {
    Invoke-RestMethod "$base/api/stt/transcribe-incremental" -Method Post -ContentType 'application/json' -Body '{}' -ErrorAction Stop | Out-Null
  } catch { $unauth2 = $true }
  Check $unauth2 "/api/stt/transcribe-incremental 未认证被拒" "/api/stt/transcribe-incremental 未认证竟然放行"

} catch {
  Bad "脚本异常: $($_.Exception.Message)"
} finally {
  Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
  Stop-Process -Id $fakeAsr.Id -Force -ErrorAction SilentlyContinue
}

Write-Host "`n================================"
Write-Host "  PASS: $script:pass    FAIL: $script:fail"
Write-Host "================================"
if ($script:fail -gt 0) { exit 1 }
