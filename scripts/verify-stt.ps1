# verify-stt.ps1 — 语音转写功能的黑盒验证（2026-10-01）
#
# 为什么是黑盒而不是只跑单测：STT 的关键风险全在**真实上游行为**上——
# 网关列了模型但没有 provider、网关收下音频却丢掉它并返回幻觉文本。
# 这些只能对着真的 llm.kxpms.cn 打一次才算数。
#
# 覆盖：
#   1. GET  /api/stt/config    两组推荐模型（网关 3 + 外部 3）都在，且 key 不回显
#   2. POST /api/stt/discover  真实扫描网关，逐个候选给出真实探测结论
#   3. POST /api/stt/probe     用真实中文语音试转：不能返回幻觉文本当转写
#   4. POST /api/stt/transcribe 同上，且错误码能被前端 extractErrorCode 提取
#   5. PUT  /api/stt/config    保存外部服务设置 → auto 通道回退到外部
#   6. 危险地址被拒（SSRF）
#
# 用法：powershell -ExecutionPolicy Bypass -File scripts/verify-stt.ps1
param(
  [string]$Port = "18099",
  [string]$WavPath = "tmp-speech.wav",
  [int]$DiscoverTimeoutSec = 180,
  [switch]$SkipBuild
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

$pass = 0; $fail = 0
function Ok($msg) { $script:pass++; Write-Host "  [PASS] $msg" -ForegroundColor Green }
function Bad($msg) { $script:fail++; Write-Host "  [FAIL] $msg" -ForegroundColor Red }
function Section($msg) { Write-Host "`n== $msg ==" -ForegroundColor Cyan }
# 错误原因可能很长，截断再打，否则一行刷屏
function truncateForLog($s, $n = 200) {
  if ($null -eq $s) { return '' }
  $t = "$s"
  if ($t.Length -le $n) { return $t }
  return $t.Substring(0, $n) + '…'
}

# ---------- 0. 前置：真实中文语音样本 ----------
if (-not (Test-Path $WavPath)) {
  Write-Host "缺少语音样本 $WavPath，正在用 Windows SAPI 合成…"
  Add-Type -AssemblyName System.Speech
  $sp = New-Object System.Speech.Synthesis.SpeechSynthesizer
  $zh = $sp.GetInstalledVoices() | Where-Object { $_.VoiceInfo.Culture.Name -like 'zh*' } | Select-Object -First 1
  if ($zh) { $sp.SelectVoice($zh.VoiceInfo.Name) }
  $sp.SetOutputToWaveFile((Join-Path $root $WavPath))
  $sp.Speak('今天下午三点开项目评审会，请准备进度报告和预算表。')
  $sp.SetOutputToNull(); $sp.Dispose()
}
$b64 = [Convert]::ToBase64String([IO.File]::ReadAllBytes((Join-Path $root $WavPath)))
Write-Host "语音样本: $WavPath ($([int]($b64.Length/1024)) KB base64)"

# ---------- 1. 起 pocketd ----------
Section "启动 pocketd :$Port"
$bin = Join-Path $root 'backend\.verify-bin\pocketd.exe'
# 每次都重建：复用旧二进制会让「改了代码但验证的还是老逻辑」，
# 这种假绿比红更危险（2026-10-01 就踩过一次）。
if (-not $SkipBuild) {
  Write-Host "构建 pocketd…"
  Push-Location (Join-Path $root 'backend')
  & go build -o .verify-bin\pocketd.exe ./cmd/pocketd
  if ($LASTEXITCODE -ne 0) { Pop-Location; throw "pocketd 构建失败（先看 go build 报错）" }
  Pop-Location
  Write-Host ("构建完成: {0}" -f (Get-Item $bin).LastWriteTime)
} elseif (-not (Test-Path $bin)) {
  throw "-SkipBuild 指定但 $bin 不存在"
}

$dataDir = Join-Path $root ".verify-stt-data"
New-Item -ItemType Directory -Force -Path $dataDir | Out-Null
$env:POCKET_HTTP_PORT = $Port
$env:POCKET_DEV_AUTH = "true"
$env:POCKET_ENV = "development"
$env:POCKET_DATA_DIR = $dataDir
$env:POCKET_DB_PATH = (Join-Path $dataDir 'pocket.sqlite')
# pocketd 启动硬要求：要么配 RedClaw Admin，要么显式走本地 legacy 旁路。
# 这里选 legacy —— 验证脚本只需要一个能签 token 的本地身份，不需要企业后端。
$env:POCKET_AUTH_LEGACY_ONLY = "true"
$env:POCKET_AUTH_USER = "admin"
$env:POCKET_AUTH_PASS = "Veritrans&9527"
# 网关密钥来源优先级：已存在的环境变量 > $root/logs/.gateway-key。
# 加这一层是因为脚本经常在另一个 worktree 里跑（比如主工作区有密钥、
# 验证在 wt 里做），此时不该为了跑一次验证就把凭据复制一份出去。
if (-not $env:POCKET_LLM_GATEWAY_API_KEY) {
  $keyFile = Join-Path $root 'logs\.gateway-key'
  if (-not (Test-Path $keyFile)) {
    throw "找不到网关密钥：环境变量 POCKET_LLM_GATEWAY_API_KEY 未设，且 $keyFile 不存在"
  }
  $env:POCKET_LLM_GATEWAY_API_KEY = (Get-Content $keyFile -Raw).Trim()
}
if (-not $env:POCKET_LLM_GATEWAY_API_KEY) { throw "网关密钥为空" }

$proc = Start-Process -FilePath $bin -WorkingDirectory (Join-Path $root 'backend') `
  -RedirectStandardOutput (Join-Path $dataDir 'pocketd.out.log') `
  -RedirectStandardError (Join-Path $dataDir 'pocketd.err.log') `
  -PassThru -WindowStyle Hidden
$base = "http://127.0.0.1:$Port"

$ready = $false
for ($i = 0; $i -lt 40; $i++) {
  Start-Sleep -Milliseconds 500
  try {
    Invoke-RestMethod "$base/healthz" -TimeoutSec 2 | Out-Null
    $ready = $true; break
  } catch { }
}
if (-not $ready) {
  Write-Host "pocketd 未就绪，stderr 尾部："
  Get-Content (Join-Path $dataDir 'pocketd.err.log') -Tail 20 -ErrorAction SilentlyContinue
  Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
  throw "启动失败"
}
Ok "pocketd 就绪 ($base)"

function Get-Token {
  $body = @{ username = 'admin'; password = 'Veritrans&9527' } | ConvertTo-Json
  (Invoke-RestMethod "$base/api/auth/login" -Method Post -Body $body -ContentType 'application/json').token
}
$token = Get-Token
$hdr = @{ Authorization = "Bearer $token" }
Ok "登录取到 token"

try {
  # ---------- 2. GET /api/stt/config ----------
  Section "GET /api/stt/config"
  $cfg = Invoke-RestMethod "$base/api/stt/config" -Headers $hdr
  $gw = @($cfg.recommended | Where-Object { $_.group -eq 'gateway' })
  $ext = @($cfg.recommended | Where-Object { $_.group -eq 'external' })
  if ($gw.Count -eq 3) { Ok "网关组预置 3 个: $(($gw.model) -join ', ')" } else { Bad "网关组应为 3 个，实际 $($gw.Count)" }
  # 数量只守区间，不写死：外部候选表会随调研增补（2026-10-01 从 3 个扩到 7 个，
  # 依据是 10 月的 ASR 成本/精度对比），写死上限会把正常新增判成失败。
  # 上限仍设，防止有人把整张 OpenRouter 模型表灌进来。
  if ($ext.Count -ge 7 -and $ext.Count -le 20) { Ok "外部组预置 $($ext.Count) 个（区间 7..20）: $(($ext.model) -join ', ')" } else { Bad "外部组应落在 7..20，实际 $($ext.Count)" }
  $noPrice = @($ext | Where-Object { -not $_.usdPerHour })
  if ($noPrice.Count -eq 0) { Ok "外部组均带每小时成本" } else { Bad "外部组有模型缺成本: $(($noPrice.model) -join ', ')" }
  if ($cfg.raw -match 'sk-' -or ($cfg | ConvertTo-Json -Depth 6) -match '"apiKey":"[^"]+"') {
    Bad "响应里出现了疑似明文 key"
  } else { Ok "响应未回显任何 key" }
  if ($cfg.channelHints.auto -and $cfg.channelHints.gateway -and $cfg.channelHints.external) {
    Ok "三个通道都有中文说明"
  } else { Bad "通道说明缺失" }

  # ---------- 3. POST /api/stt/discover ----------
  Section "POST /api/stt/discover （真实打网关，限流 12 次/分钟，最多 $DiscoverTimeoutSec 秒）"
  $sw = [Diagnostics.Stopwatch]::StartNew()
  $disc = Invoke-RestMethod "$base/api/stt/discover" -Method Post -Headers $hdr -TimeoutSec $DiscoverTimeoutSec
  $sw.Stop()
  Ok ("扫描完成，用时 {0:N1}s；网关共 {1} 个模型" -f $sw.Elapsed.TotalSeconds, $disc.totalModels)
  $cands = @($disc.candidates)
  if ($cands.Count -eq 0) { Bad "候选为空（modality=audio 的模型应被识别）" }
  foreach ($c in $cands) {
    Write-Host ("    - {0,-42} {1}" -f $c.model, $c.status) -ForegroundColor DarkGray
  }
  $usable = @($cands | Where-Object { $_.status -eq 'ok' })
  $noProv = @($cands | Where-Object { $_.status -eq 'no_provider' })
  if ($usable.Count -eq 0 -and $noProv.Count -gt 0) {
    Ok "如实报告：$($noProv.Count) 个候选网关无上游 provider（未被误标为可用）"
  } elseif ($usable.Count -gt 0) {
    Ok "发现 $($usable.Count) 个真正可用的网关 ASR 模型: $(($usable.model) -join ', ')"
  } else {
    Bad "候选既非 ok 也非 no_provider，需人工判读"
  }

  # ---------- 4. POST /api/stt/probe（真实中文语音）----------
  Section "POST /api/stt/probe （真实中文语音试转）"
  $probe = Invoke-RestMethod "$base/api/stt/probe" -Method Post -Headers $hdr -ContentType 'application/json' `
    -Body (@{ audioBase64 = $b64; filename = 'speech.wav' } | ConvertTo-Json -Compress) -TimeoutSec 180
  if ($probe.ok) {
    Ok "试转成功：$($probe.model) / $($probe.transport) → 「$($probe.text)」"
  } else {
    Write-Host "    试转失败原因：$(truncateForLog $probe.error 300)" -ForegroundColor Yellow
    if ($probe.error -match '没有附上录音|未收到音频|no audio|dropped the audio') {
      Ok "正确识别出「上游丢音频」并判失败（没有把幻觉文本当转写）"
    } elseif ($probe.error -match '语音转写|stt_unavailable|不可用|未配置') {
      Ok "给出了可行动的中文原因"
    } else {
      Bad "失败原因不可行动：$($probe.error)"
    }
  }

  # ---------- 5. POST /api/stt/transcribe ----------
  Section "POST /api/stt/transcribe"
  try {
    $tr = Invoke-RestMethod "$base/api/stt/transcribe" -Method Post -Headers $hdr -ContentType 'application/json' `
      -Body (@{ audioBase64 = $b64; filename = 'speech.wav' } | ConvertTo-Json -Compress) -TimeoutSec 180
    if ($tr.text) {
      if ($tr.text -match '没有附上录音|未收到音频') { Bad "把幻觉文本当成了转写结果：$($tr.text)" }
      else { Ok "转写成功 via $($tr.channel)/$($tr.model)：「$($tr.text)」" }
    }
  } catch {
    # $_.ErrorDetails.Message 是响应体 {"error":"stt_unavailable: …"}；
    # 前端 http.ts 取的是 parsedBody.error，所以这里必须先解包再判前缀。
    $msg = $_.ErrorDetails.Message
    Write-Host "    502 响应：$msg" -ForegroundColor Yellow
    $inner = $msg
    try { $inner = (ConvertFrom-Json $msg).error } catch { }
    # 前端 extractErrorCode 取第一个冒号前的 [a-z0-9_]+，前缀错了用户就看不到原因
    $head = ($inner -split ':')[0].Trim()
    if ($head -match '^[a-z0-9_]+$' -and $head -eq 'stt_unavailable') {
      Ok "错误码在首位（'$head'），前端能提取到"
    } else {
      Bad "错误码不在首位（'$head'），前端会退到通用兜底文案"
    }
    if ($inner.Length -le 400) { Ok "原因长度可控（$($inner.Length) 字符）" }
    else { Bad "原因过长（$($inner.Length) 字符），会撑爆设置页/toast" }
  }

  # ---------- 6. 保存外部服务设置 → auto 回退 ----------
  Section "PUT /api/stt/config （保存外部服务 → auto 通道回退）"
  $saved = Invoke-RestMethod "$base/api/stt/config" -Method Put -Headers $hdr -ContentType 'application/json' `
    -Body (@{ channel = 'external'; externalBaseURL = 'https://api.openai.com/v1';
              externalModel = 'gpt-4o-mini-transcribe'; externalApiKey = 'sk-verify-placeholder' } | ConvertTo-Json -Compress)
  if ($saved.hasExternalKey) { Ok "外部 key 已保存且不回显（hasExternalKey=true）" } else { Bad "外部 key 未保存" }
  $cfg2 = Invoke-RestMethod "$base/api/stt/config" -Headers $hdr
  if ($cfg2.settings.effectiveModel -eq 'gpt-4o-mini-transcribe') { Ok "生效模型 = gpt-4o-mini-transcribe" }
  else { Bad "生效模型不对：$($cfg2.settings.effectiveModel)" }
  # probe 是「测试」端点：失败也返回 200 + {ok:false,error}，因为失败原因本身
  # 就是要展示给用户的数据。所以这里必须断言 ok 字段，只看 HTTP 状态会误判。
  $probe2 = $null
  try {
    $probe2 = Invoke-RestMethod "$base/api/stt/probe" -Method Post -Headers $hdr -ContentType 'application/json' `
      -Body (@{ audioBase64 = $b64; filename = 'speech.wav' } | ConvertTo-Json -Compress) -TimeoutSec 90
  } catch {
    # 传输层就失败（DNS/超时/SSRF 拦截）也算「占位 key 没能转写成功」
    Ok "占位 key 未通过 HTTP 层：$($_.Exception.Message)"
  }
  if ($probe2) {
    if ($probe2.ok) {
      if ($probe2.text -match '没有附上录音|未收到音频|no audio') {
        Bad "拿到了幻觉文本当转写：$($probe2.text)"
      } else {
        Bad "占位 key 转写成功（ok=true, text=$($probe2.text)）：需确认不是上游放行"
      }
    } else {
      Ok "占位 key 被拒（ok=false）：$(truncateForLog $probe2.error)"
    }
  }

  # ---------- 7. SSRF ----------
  Section "危险地址应被拒绝"
  foreach ($bad in @('http://127.0.0.1:8080/v1', 'http://169.254.169.254/v1', 'ftp://example.com/v1')) {
    try {
      $null = Invoke-RestMethod "$base/api/stt/config" -Method Put -Headers $hdr -ContentType 'application/json' `
        -Body (@{ channel = 'external'; externalBaseURL = $bad } | ConvertTo-Json -Compress)
      Bad "接受了危险地址 $bad"
    } catch { Ok "拒绝了 $bad" }
  }

  # ---------- 8. 恢复 auto ----------
  Section "恢复 channel=auto"
  $null = Invoke-RestMethod "$base/api/stt/config" -Method Put -Headers $hdr -ContentType 'application/json' `
    -Body (@{ channel = 'auto'; externalApiKey = '__clear__' } | ConvertTo-Json -Compress)
  Ok "已恢复 auto 并清空占位 key"
}
finally {
  Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
}

Write-Host "`n================ 结果 ================" -ForegroundColor Cyan
Write-Host "PASS=$pass FAIL=$fail"
if ($fail -gt 0) { exit 1 }
