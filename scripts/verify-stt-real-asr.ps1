# 端到端真实 ASR 验证：音频 → Go 后端 → 真 ASR 引擎 → 文本。
#
# 与 scripts/verify-stt.ps1 的分工：
#   verify-stt.ps1        结构性验证（错误码、通道回退、SSRF、候选列表…），
#                         上游是真的网关，但**不要求识别成功**
#   本脚本               质量验证：把 ground truth 语料真的送进整条链路，
#                         要求**识别出正确的中文文本**
#
# 用的是 scripts/local-asr-server.py（faster-whisper 本地 CPU，零成本零 key）。
# 之所以能在产品代码零改动的前提下接上，是因为本地 ASR 走的是**外部通道**，
# 而上一轮刚加的 POCKET_STT_ALLOW_PRIVATE=true 正是「允许指向自建 ASR」的开关。
#
# 前置：
#   1. python venv 装好 faster-whisper，模型已下载到 .verify-stt-data/models/
#   2. python scripts/make-asr-groundtruth.ps1 生成语料
#   3. python scripts/local-asr-server.py --port 18900 --model <目录> 已在跑
#
# 用法：
#   powershell -ExecutionPolicy Bypass -File scripts\verify-stt-real-asr.ps1 `
#       -AsrPort 18900 -Port 18240
param(
  [int]$Port = 18240,          # 验证用 pocketd 端口
  [int]$AsrPort = 18900,       # 本地 ASR 服务端口
  [string]$ModelDir = '',     # faster-whisper 模型目录（空则用默认缓存名）
  [switch]$SkipBuild
)

$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent

# 按 UTF-8 显式解码的 JSON 调用（见下面登录处的说明）。
# 注意：内部变量**不能**叫 $args —— 那是 PowerShell 的自动变量（未绑定参数），
# 在函数里赋值再 splat 会让解析器错乱，实测报「意外的标记 }」（2026-10-01）。
function Invoke-Json([string]$Uri, [string]$Method = 'Get', $Body = $null, $Headers = $null, [int]$TimeoutSec = 900) {
  $params = @{
    Uri             = $Uri
    Method          = $Method
    TimeoutSec      = $TimeoutSec
    UseBasicParsing = $true
  }
  if ($Headers) { $params.Headers = $Headers }
  if ($null -ne $Body) {
    $params.ContentType = 'application/json; charset=utf-8'
    # Body 本身也要是 UTF-8 字节，否则中文参数会在发送前就坏掉
    $params.Body = [System.Text.Encoding]::UTF8.GetBytes($Body)
  }
  $resp = Invoke-WebRequest @params
  $text = [System.Text.Encoding]::UTF8.GetString($resp.RawContentStream.ToArray())
  if (-not $text) { return $null }
  return $text | ConvertFrom-Json
}

$corpus = Join-Path $root '.verify-stt-data\asr-corpus'
$gtPath = Join-Path $corpus 'groundtruth.json'
if (-not (Test-Path $gtPath)) { throw "缺少语料：$gtPath（先跑 make-asr-groundtruth.ps1）" }

# ---------- 0. ASR 服务健康检查 ----------
Write-Host "== 0. 本地 ASR 服务 =="
try {
  $health = Invoke-Json "http://127.0.0.1:$AsrPort/" -TimeoutSec 8
} catch {
  throw "本地 ASR 服务没起来（http://127.0.0.1:$AsrPort/）：$($_.Exception.Message)"
}
Write-Host ("  服务 OK：model={0} device={1}" -f $health.model, $health.device)
if ($health.device -ne 'cpu') { Write-Host "  note 非 CPU 推理，速度数据仅供参考" }

# ---------- 1. 起 pocketd ----------
$bin = Join-Path $root 'backend\.verify-bin\pocketd.exe'
if (-not $SkipBuild) {
  Write-Host "== 1. 构建 pocketd =="
  Push-Location (Join-Path $root 'backend')
  & go build -o .verify-bin\pocketd.exe ./cmd/pocketd
  if ($LASTEXITCODE -ne 0) { Pop-Location; throw "pocketd 构建失败" }
  Pop-Location
} elseif (-not (Test-Path $bin)) { throw "-SkipBuild 指定但 $bin 不存在" }

$dataDir = Join-Path $root ".verify-stt-data"
$env:POCKET_HTTP_PORT = "$Port"
$env:POCKET_DEV_AUTH = "true"
$env:POCKET_ENV = "development"
$env:POCKET_DATA_DIR = $dataDir
$env:POCKET_DB_PATH = (Join-Path $dataDir 'pocket.sqlite')
$env:POCKET_AUTH_LEGACY_ONLY = "true"
$env:POCKET_AUTH_USER = "admin"
$env:POCKET_AUTH_PASS = "Veritrans&9527"
# 关键：允许 STT 外部地址指向 127.0.0.1（自建 ASR）。这是本轮新加的独立开关，
# 与网关那个 POCKET_LLM_GATEWAY_ALLOW_PRIVATE 语义不同，不能混用。
$env:POCKET_STT_ALLOW_PRIVATE = "true"
# 网关开关显式清空：本脚本不测网关通道，避免继承父进程环境造成断言漂移
$env:POCKET_LLM_GATEWAY_ALLOW_PRIVATE = ""

$proc = $null
try {
  Write-Host "== 2. 启动 pocketd :$Port =="
  $proc = Start-Process -FilePath $bin -WorkingDirectory (Join-Path $root 'backend') `
    -RedirectStandardOutput (Join-Path $dataDir 'realasr.out.log') `
    -RedirectStandardError  (Join-Path $dataDir 'realasr.err.log') `
    -WindowStyle Hidden -PassThru
  $base = "http://127.0.0.1:$Port"

  # 健康检查端点是 /healthz（与 verify-stt.ps1 一致）。
  # 之前这里写成 /api/health —— 该路由不存在，于是永远探测不到就绪，
  # 白等 20 秒还报「未就绪」，而 pocketd 其实早已 listening。
  $ready = $false
  foreach ($i in 1..40) {
    Start-Sleep -Milliseconds 500
    try { $null = Invoke-RestMethod "$base/healthz" -TimeoutSec 2; $ready = $true; break } catch { }
  }
  if (-not $ready) { throw "pocketd 40 次探测仍未就绪，日志：$(Join-Path $dataDir 'realasr.err.log')" }
  Write-Host "  就绪"

  # ---------- 3. 登录 ----------
  # 统一走 Invoke-Json：PowerShell 5.1 的 Invoke-RestMethod 遇到
  # Content-Type 里没有 charset 的 JSON 会按 Latin-1 解码，中文全变乱码
  # （2026-10-01 实测：后端返回的「帮我记一下明天要买牛奶和面包」在脚本里
  # 变成「å¸å¸º...」。用 curl 直连后端验过，后端返回的中文**完全正确**，
  # 所以这是读取侧的问题，不是产品的问题 —— 但证据文件乱码同样没法用，
  # 必须在这里显式按 UTF-8 解码。）
  $login = Invoke-Json "$base/api/auth/login" -Method Post `
    -Body (@{ username = 'admin'; password = 'Veritrans&9527' } | ConvertTo-Json)
  $hdr = @{ Authorization = "Bearer $($login.token)" }
  Write-Host "  已登录"

  # ---------- 4. 指向本地 ASR ----------
  Write-Host "== 3. 配置外部通道指向本地 ASR =="
  $cfg = @{
    channel          = 'external'
    externalBaseURL  = "http://127.0.0.1:$AsrPort/v1"
    externalModel    = $health.model
    externalApiKey   = 'local-asr-no-key-needed'
    language         = 'zh'
  }
  $saved = Invoke-Json "$base/api/stt/config" -Method Put -Headers $hdr `
    -Body ($cfg | ConvertTo-Json -Compress)
  if (-not $saved.hasExternalKey) { throw "外部 key 未保存" }
  Write-Host "  已保存：$($saved.externalModel) @ $($saved.externalBaseURL)"

  # ---------- 5. 逐条真转写 ----------
  Write-Host "== 4. 真实转写（$gtPath）=="
  $items = Get-Content $gtPath -Raw -Encoding UTF8 | ConvertFrom-Json
  $rows = @()
  foreach ($it in $items) {
    $wav = Join-Path $corpus $it.file
    $sw = [System.Diagnostics.Stopwatch]::StartNew()
    $body = @{ audioBase64 = [Convert]::ToBase64String([IO.File]::ReadAllBytes($wav)); language = 'zh' } | ConvertTo-Json -Compress
    try {
      $r = Invoke-Json "$base/api/stt/transcribe" -Method Post -Headers $hdr `
        -Body $body -TimeoutSec 900
      $sw.Stop()
      $rows += [pscustomobject]@{
        id = $it.id; cat = $it.category; ref = $it.text; hyp = $r.text
        ok = [bool]$r.text; ms = [int]$sw.ElapsedMilliseconds
        model = $r.model; channel = $r.channel; cost = $r.costCents
        lang = $r.language
      }
      Write-Host ("  [{0,-12}] {1,6} ms  {2}" -f $it.id, $sw.ElapsedMilliseconds, $r.text)
    } catch {
      $sw.Stop()
      $msg = $_.ErrorDetails.Message
      $rows += [pscustomobject]@{ id = $it.id; cat = $it.category; ref = $it.text; hyp = ''; ok = $false; ms = [int]$sw.ElapsedMilliseconds; model = ''; channel = ''; cost = $null; lang = ''; error = $msg }
      Write-Host ("  [{0,-12}] {1,6} ms  FAILED: {2}" -f $it.id, $sw.ElapsedMilliseconds, $msg) -ForegroundColor Red
    }
  }

  # ---------- 6. 汇总 ----------
  $okRows = $rows | Where-Object { $_.ok }
  Write-Host ""
  Write-Host "== 5. 汇总 =="
  Write-Host "  成功 $($okRows.Count)/$($rows.Count) 段返回非空文本"
  if ($empty -gt 0) { Write-Host "  失败/空 $empty 段" -ForegroundColor Yellow }
  if ($okRows.Count -gt 0) {
    $avg = [math]::Round((($okRows | Measure-Object ms -Average).Average) / 1000, 2)
    Write-Host "  平均端到端耗时 ${avg}s（含本机 CPU 推理）"
    $ch = ($okRows | Select-Object -First 1).channel
    $md = ($okRows | Select-Object -First 1).model
    Write-Host "  通道=$ch  模型=$md  语种=$((($okRows | Select-Object -First 1).lang))"
  }
  $out = Join-Path $dataDir "real-asr-transcripts.json"
  $rows | ConvertTo-Json -Depth 4 | Set-Content -Path $out -Encoding UTF8
  Write-Host "  明细: $out"
  Write-Host ""
  Write-Host "下一步（算准确率，需要 ground truth 对照）："
  Write-Host "  C:\workspace\asr-venv\Scripts\python.exe scripts\eval-asr-accuracy.py --corpus .verify-stt-data\asr-corpus --endpoint http://127.0.0.1:$AsrPort"

  if ($okRows.Count -eq 0) { exit 2 }
} finally {
  if ($proc) { Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue }
}
