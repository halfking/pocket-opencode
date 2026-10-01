# 合成「已知内容」的中文测试音频 —— 让识别准确率变成可计算的问题。
#
# 为什么需要这个：此前所有 ASR 验证用的都是纯音调（New-ToneWav）或假上游
# 写死的文本，只能证明「音频到了上游、请求形状对、返回被正确解析」，
# **证明不了「识别得准」**。真实中文录音（tmp-speech.wav）没有 ground truth，
# 也没法量化。
#
# 这里用 Windows 自带的 SAPI 中文语音（Microsoft Huihui Desktop, zh-CN）
# 合成 —— 免费、离线、不需要任何 key、不安装任何第三方 TTS。
# 文本是自己写的，所以每个 wav 的正确转写是**已知**的。
#
# 语料覆盖四类真实难点（ASR 出错的高发区）：
#   1. 会议书面语：长句、逗号分句
#   2. 口语输入：短句、无标点
#   3. 数字与日期：ASR 最容易把「三」写成「3」或听错
#   4. 中英混排：产品名/术语，最容易触发幻觉或音译
#
# 输出：
#   <outDir>/*.wav          16kHz 单声道 PCM
#   <outDir>/groundtruth.json  [{id, file, text, category}]
#
# 用法：powershell -ExecutionPolicy Bypass -File scripts\make-asr-groundtruth.ps1 -OutDir <dir>
param(
  [string]$OutDir = "$PSScriptRoot\..\.verify-stt-data\asr-corpus"
)

$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Speech

# 语料：id / 分类 / 文本
$corpus = @(
  @{ id = 'meeting-01'; cat = 'meeting'; text = '今天的会议主要讨论三个议题，第一是预算审核，第二是产品排期，第三是团队招聘。' },
  @{ id = 'meeting-02'; cat = 'meeting'; text = '我们需要在下个季度之前完成这次重构，否则技术债会越积越多，影响后续的开发效率。' },
  @{ id = 'dictation-01'; cat = 'dictation'; text = '帮我记一下明天要买牛奶和面包' },
  @{ id = 'dictation-02'; cat = 'dictation'; text = '这个方案我觉得挺好的但是成本有点高' },
  @{ id = 'numbers-01'; cat = 'numbers'; text = '会议定在二零二六年十月十五号上午十点，地点在三号会议室。' },
  @{ id = 'numbers-02'; cat = 'numbers'; text = '这次的预算是十二万三千四百五十元，比上个季度增加了百分之十八。' },
  @{ id = 'mixed-01'; cat = 'mixed'; text = '把 API 的限流阈值调高一点，顺便看看 Redis 的内存占用。' },
  @{ id = 'mixed-02'; cat = 'mixed'; text = '部署完成了吗 Docker 镜像推到仓库了没有' }
)

New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
$synth = New-Object System.Speech.Synthesis.SpeechSynthesizer
# 明确指定中文语音：机器上可能同时装了英文 TTS（Zira），不指定会随机挑
$zh = $synth.GetInstalledVoices() | Where-Object { $_.VoiceInfo.Culture.Name -like 'zh*' } | Select-Object -First 1
if (-not $zh) { throw '没有找到中文 TTS 语音（zh-CN），无法生成 ground truth' }
$synth.SelectVoice($zh.VoiceInfo.Name)
Write-Host "TTS 语音: $($zh.VoiceInfo.Name) [$($zh.VoiceInfo.Culture)]"
$synth.Rate = -1   # 稍慢一点，更接近真实会议录音的语速

$results = @()
foreach ($item in $corpus) {
  $wav = Join-Path $OutDir "$($item.id).wav"
  # SAPI 输出的是 PCM WAV；faster-whisper 用 PyAV 读取时会重采样到 16k，
  # 这里不依赖它原生就是 16k —— 读取端负责重采样（真实链路也一样）。
  $synth.SetOutputToWaveFile($wav)
  $synth.Speak($item.text)
  $synth.SetOutputToNull()
  $len = (Get-Item $wav).Length
  $results += [pscustomobject]@{
    id       = $item.id
    category = $item.cat
    file     = "$($item.id).wav"
    text     = $item.text
    bytes    = $len
  }
  Write-Host ("  [{0,-11}] {1,7} bytes  {2}" -f $item.cat, $len, $item.text)
}
$synth.Dispose()

$gt = Join-Path $OutDir 'groundtruth.json'
# 不转义中文，保证文件可读、便于人工核对
$json = $results | ConvertTo-Json -Depth 4
[System.IO.File]::WriteAllText($gt, $json, (New-Object System.Text.UTF8Encoding($false)))

Write-Host ""
Write-Host "ground truth: $gt"
Write-Host "音频 $((Get-ChildItem $OutDir -Filter *.wav).Count) 段，合计 $([math]::Round((($results | Measure-Object bytes -Sum).Sum)/1KB,1)) KB"
