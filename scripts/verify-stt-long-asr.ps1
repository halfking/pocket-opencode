# 会议长录音全链路真 ASR 验证：79 秒合成语音 → /api/stt/transcribe-full → 切段 → 真引擎 → 拼回全文。
#
# 与既有脚本的分工：
#   verify-stt.ps1           结构性（错误码/回退/SSRF/候选），上游是假网关
#   verify-stt-stream.ps1    长音频切分逻辑，用 **纯音调**（New-ToneWav）
#   verify-stt-real-asr.ps1  逐段真转写质量，但每段都是**独立**的短音频
#   本脚本                   长音频**切分之后**每一段是不是真能转出话
#
# 纯音调验不出「切完之后能不能识出中文」——切分错了（比如漏句、重复段、
# 静音被当语音）那类缺陷在音调上完全看不出来。会议上有人说话、停顿、
# 再说，这是本功能最主要的场景，必须有一遍真语音走完整条链路。
#
# 前置：
#   1. venv 装好 faster-whisper，模型在 .verify-stt-data/models/
#   2. make-asr-groundtruth.ps1 生成 8 段语料
#   3. make-meeting-wav.py 拼出 meeting-long.wav（含 1 段纯静音）
#   4. local-asr-server.py --port 18900 已在跑
#
# 用法：
#   powershell -ExecutionPolicy Bypass -File scripts\verify-stt-long-asr.ps1 -AsrPort 18900 -Port 18241
param(
  [int]$Port = 18241,          # 验证用 pocketd 端口
  [int]$AsrPort = 18900,       # 本地 ASR 服务端口
  # 要测的长录音。默认是 make-meeting-wav.py 产出的 meeting-long.wav（句间有停顿，
  # 走静音切分）。传 meeting-nogap.wav 则走**连续讲话**路径，只能 25 秒硬切 ——
  # 那条分支用真语音从未被验过。
  [string]$Wav = '',
  [switch]$SkipBuild
)

$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent

# PowerShell 5.1 的 Invoke-RestMethod 遇到 Content-Type 不带 charset 的 JSON
# 会按 Latin-1 解码，中文全乱码。这里显式按 UTF-8 读原始字节（2026-10-01 踩过）。
# 变量名不能叫 $args —— 那是未绑定参数的自动变量，splat 会让解析器错乱。
function Invoke-Json([string]$Uri, [string]$Method = 'Get', $Body = $null, $Headers = $null, [int]$TimeoutSec = 1800) {
  $params = @{
    Uri             = $Uri
    Method          = $Method
    TimeoutSec      = $TimeoutSec
    UseBasicParsing = $true
  }
  if ($Headers) { $params.Headers = $Headers }
  if ($null -ne $Body) {
    $params.ContentType = 'application/json; charset=utf-8'
    $params.Body = [System.Text.Encoding]::UTF8.GetBytes($Body)
  }
  $resp = Invoke-WebRequest @params
  $text = [System.Text.Encoding]::UTF8.GetString($resp.RawContentStream.ToArray())
  if (-not $text) { return $null }
  return $text | ConvertFrom-Json
}

$dataDir = Join-Path $root '.verify-stt-data'
if (-not $Wav) { $Wav = 'meeting-long.wav' }
if (-not [System.IO.Path]::IsPathRooted($Wav)) { $Wav = Join-Path $dataDir $Wav }
$wav = $Wav
$timeline = "$wav.json"
if (-not (Test-Path $wav)) { throw "缺少长录音：$wav（先跑 make-meeting-wav.py）" }
if (-not (Test-Path $timeline)) { throw "缺少时间轴：$timeline" }

# ---------- 0. ASR 服务 ----------
Write-Host "== 0. 本地 ASR 服务 =="
try {
  $health = Invoke-Json "http://127.0.0.1:$AsrPort/" -TimeoutSec 8
} catch {
  throw "本地 ASR 服务没起来（http://127.0.0.1:$AsrPort/）：$($_.Exception.Message)"
}
Write-Host ("  服务 OK：model={0} device={1}" -f $health.model, $health.device)

# ---------- 1. 构建 pocketd ----------
# 每次黑盒都重建二进制：不重建就等于在测一个可能早就过期的产物，
# 测出来的结论不能代表工作区里的代码。
$bin = Join-Path $root 'backend\.verify-bin\pocketd.exe'
if (-not $SkipBuild) {
  Write-Host "== 1. 构建 pocketd =="
  Push-Location (Join-Path $root 'backend')
  & go build -o .verify-bin\pocketd.exe ./cmd/pocketd
  if ($LASTEXITCODE -ne 0) { Pop-Location; throw "pocketd 构建失败" }
  Pop-Location
} elseif (-not (Test-Path $bin)) { throw "-SkipBuild 指定但 $bin 不存在" }

$env:POCKET_HTTP_PORT = "$Port"
$env:POCKET_DEV_AUTH = "true"
$env:POCKET_ENV = "development"
$env:POCKET_DATA_DIR = $dataDir
$env:POCKET_DB_PATH = (Join-Path $dataDir 'pocket.sqlite')
$env:POCKET_AUTH_LEGACY_ONLY = "true"
$env:POCKET_AUTH_USER = "admin"
$env:POCKET_AUTH_PASS = "Veritrans&9527"
# 允许 STT 指向 127.0.0.1（自建 ASR）。这是独立于网关的那个开关，不能混用。
$env:POCKET_STT_ALLOW_PRIVATE = "true"
# 本脚本不测网关通道，显式清空，避免继承父进程环境造成断言漂移
$env:POCKET_LLM_GATEWAY_ALLOW_PRIVATE = ""

$proc = $null
try {
  Write-Host "== 2. 启动 pocketd :$Port =="
  $proc = Start-Process -FilePath $bin -WorkingDirectory (Join-Path $root 'backend') `
    -RedirectStandardOutput (Join-Path $dataDir 'longasr.out.log') `
    -RedirectStandardError  (Join-Path $dataDir 'longasr.err.log') `
    -WindowStyle Hidden -PassThru
  $base = "http://127.0.0.1:$Port"

  $ready = $false
  foreach ($i in 1..40) {
    Start-Sleep -Milliseconds 500
    try { $null = Invoke-RestMethod "$base/healthz" -TimeoutSec 2; $ready = $true; break } catch { }
  }
  if (-not $ready) { throw "pocketd 40 次探测仍未就绪，日志：$(Join-Path $dataDir 'longasr.err.log')" }
  Write-Host "  就绪"

  $login = Invoke-Json "$base/api/auth/login" -Method Post `
    -Body (@{ username = 'admin'; password = 'Veritrans&9527' } | ConvertTo-Json)
  $hdr = @{ Authorization = "Bearer $($login.token)" }
  Write-Host "  已登录"

  Write-Host "== 3. 配置外部通道指向本地 ASR =="
  $cfg = @{
    channel          = 'external'
    externalBaseURL  = "http://127.0.0.1:$AsrPort/v1"
    externalModel    = $health.model
    externalApiKey   = 'local-asr-no-key-needed'
    language         = 'zh'
  }
  $saved = Invoke-Json "$base/api/stt/config" -Method Put -Headers $hdr -Body ($cfg | ConvertTo-Json -Compress)
  if (-not $saved.hasExternalKey) { throw "外部 key 未保存" }
  Write-Host "  已保存：$($saved.externalModel) @ $($saved.externalBaseURL)"

  # ---------- 4. 长录音全量转写 ----------
  # 这一步**必须用 curl.exe**，不能用 Invoke-WebRequest。
  #
  # 2026-10-01 实测：PowerShell 5.1 的 Invoke-WebRequest 在这个请求上会抛
  # 「The underlying connection was closed: ... closed by the server」，
  # 而同时后端日志里明明白白写着
  #     [SLOW] POST /api/stt/transcribe-full - 200 (30.1958247s)
  # —— 服务端**成功**了 200，是客户端在长响应上断了 keep-alive 连接。
  # 一次 3.4MB base64 的 POST 要等 30 秒以上，正好落在这个坑里。
  # 用它做长录音验证会把「服务端成功」误报成「链路失败」。
  $dur = [math]::Round((Get-Item $wav).Length / 2 / 16000, 1)
  Write-Host ""
  Write-Host "== 4. POST /api/stt/transcribe-full（${dur}s 合成语音）=="
  $b64 = [Convert]::ToBase64String([IO.File]::ReadAllBytes($wav))
  $tag = [System.IO.Path]::GetFileNameWithoutExtension($wav)
  $bodyFile = Join-Path $dataDir "$tag-request.json"
  $out = Join-Path $dataDir "$tag-result.json"
  # 显式写 UTF-8 无 BOM：带 BOM 时 Go 的 JSON 解码会直接失败。
  [System.IO.File]::WriteAllText($bodyFile,
    (@{ audioBase64 = $b64; filename = (Split-Path $wav -Leaf); language = 'zh' } | ConvertTo-Json -Compress),
    (New-Object System.Text.UTF8Encoding($false)))
  Write-Host "  请求体 $([math]::Round((Get-Item $bodyFile).Length / 1MB, 1)) MB，等待切分+逐段转写（大头是本机 CPU 推理）…"

  $sw = [System.Diagnostics.Stopwatch]::StartNew()
  $code = & curl.exe -s -o $out -w '%{http_code}' -m 1800 `
    -X POST "$base/api/stt/transcribe-full" `
    -H "Authorization: Bearer $($login.token)" `
    -H 'Content-Type: application/json; charset=utf-8' `
    --data-binary "@$bodyFile"
  $sw.Stop()
  if ($LASTEXITCODE -ne 0) { throw "curl 失败，exit=$LASTEXITCODE" }
  if ($code -ne '200') {
    $raw = [System.IO.File]::ReadAllText($out)
    throw "transcribe-full 返回 HTTP $code：$($raw.Substring(0, [Math]::Min(400, $raw.Length)))"
  }
  $res = [System.IO.File]::ReadAllText($out) | ConvertFrom-Json
  if (-not $res.ok) { throw "transcribe-full 返回 ok=false：$($res.error)" }

  Write-Host ("  完成 {0:N1}s（{1} 段，failed={2}）" -f ($sw.ElapsedMilliseconds / 1000), $res.segments.Count, $res.failed)
  Write-Host "  全文：$($res.text)"
  Write-Host "  明细: $out"

  # ---------- 5. 纯静音：不得凭空产出文本 ----------
  # 单独一步，不能靠会议语料里那 2 秒静音：它被夹在两句之间，切分出来的段
  # 横跨静音与语音，整段文本当然非空，拿它判幻觉只会得到假阳性。
  Write-Host ""
  Write-Host "== 5. 纯静音 12s：必须不产生任何文本 =="
  $sil = Join-Path $dataDir 'meeting-long-silence.wav'
  $halluc = $false
  if (Test-Path $sil) {
    $silB64 = [Convert]::ToBase64String([IO.File]::ReadAllBytes($sil))
    $silBody = @{ audioBase64 = $silB64; filename = 'silence.wav'; language = 'zh' } | ConvertTo-Json -Compress
    try {
      $silRes = Invoke-Json "$base/api/stt/transcribe-full" -Method Post -Headers $hdr -Body $silBody -TimeoutSec 600
      $txt = ''
      foreach ($sg in $silRes.segments) { $txt += $sg.text }
      $txt = $txt + $silRes.text
      $clean = [string]::IsNullOrWhiteSpace($txt)
      Write-Host ("  返回 ok={0} 段数={1} 文本长度={2}" -f $silRes.ok, $silRes.segments.Count, $txt.Length)
      if (-not $clean) {
        Write-Host "  [FAIL] 纯静音产出了文本：$txt" -ForegroundColor Red
        $halluc = $true
      } else {
        Write-Host "  [OK] 纯静音未产出任何文本（无幻觉）"
      }
      $silRes | ConvertTo-Json -Depth 6 | Set-Content -Path (Join-Path $dataDir 'silence-asr-result.json') -Encoding UTF8
    } catch {
      # 全部段都空时 TranscribeFull 会返回「没有任何有效文本」的 502 —— 那是
      # **正确**行为（不能把空当成功），不是失败。
      $detail = ''
      try { $detail = $_.ErrorDetails.Message } catch { $detail = $_.Exception.Message }
      if ($detail -match '没有任何有效文本') {
        Write-Host "  [OK] 纯静音被正确判为无内容（后端返回「没有任何有效文本」，未落库任何文本）"
      } else {
        Write-Host "  [FAIL] 纯静音请求异常：$detail" -ForegroundColor Red
        $halluc = $true
      }
    }
  } else {
    Write-Host "  [未验证] 缺少 $sil，先跑 make-meeting-wav.py" -ForegroundColor Yellow
  }

  # ---------- 6. 对齐评估 ----------
  Write-Host ""
  Write-Host "== 6. 逐句对齐 ground truth =="
  $py = 'C:\workspace\asr-venv\Scripts\python.exe'
  if (-not (Test-Path $py)) { $py = 'python' }
  & $py (Join-Path $PSScriptRoot 'eval-long-asr.py') --timeline $timeline --result $out `
    --out (Join-Path $dataDir 'long-asr-eval.json')
  $code = $LASTEXITCODE
  if ($halluc) { $code = 2 }
  Write-Host ""
  if ($code -eq 0) { Write-Host "结论：长录音全链路通过（无幻觉、无整句丢失）" }
  else { Write-Host "结论：存在结构性问题，CER 数字不可引用（详见上面 FAIL 行）" -ForegroundColor Red }
  exit $code
} finally {
  if ($proc) { Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue }
}
