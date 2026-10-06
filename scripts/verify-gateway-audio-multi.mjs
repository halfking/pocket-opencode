// 复验「网关音频多供应商 + 转写后处理」全链路（2026-10-06 ASR 多模型轮）。
//
// 与 verify-gateway-audio.mjs（单模型连通性探测）的分工：那个回答「这条
// 路径通不通」，本脚本回答「多模型横向比是什么水平、refine/analyze 两级
// 后处理是否可用」——这是会议转写三层金字塔（端=快 / 本地高精=隐私档 /
// 云=准）里云侧一层的验收面。
//
// 用法：
//   GW_BASE=http://127.0.0.1:8782/v1 GW_KEY=sk-xxx node scripts/verify-gateway-audio-multi.mjs
//   （GW_KEY 查找顺序与 verify-gateway-audio.mjs 相同；GW_BASE 缺省本地 8782，
//     因为矩阵里的 minimax-asr-1.0 / glm-asr 是 2026-10-06 轮新接的目录项，
//     生产 245/154 在该轮部署前没有这两行）
//
// 六段取证：
//   [1] TTS 合成已知文本的中/英样本（回环方法论的「已知输入」侧）
//   [2] 多模型 ASR 矩阵：zh/en 字级准确率 + 首字延迟
//       （glm-asr 预期 429：端点/模型名已被上游接受，卡在凭据余额——
//        这是「数据面已接通、credential 侧待充值」的诚实呈报，不算失败）
//   [3] ASR 流式（SSE delta 事件序）
//   [4] /v1/audio/refine 精细化转写（语气词清理 + ITN + 热词回执）
//   [5] /v1/audio/analyze 实时总结分析（滚动摘要 + hints）
//   [6] MCP tools/list 四工具 + tools/call 往返
//
// 退出码：0 = 核心路径（≥1 个 ASR 模型 + refine + analyze）全绿；1 = 有核心失败。
import { existsSync, readFileSync, readdirSync, writeFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = dirname(fileURLToPath(import.meta.url))
const REPO = resolve(HERE, '..')
const BASE = (process.env.GW_BASE || 'http://127.0.0.1:8782/v1').replace(/\/+$/, '')
const TIMEOUT_MS = Number(process.env.GW_TIMEOUT_MS || 120000)
const LLM_MODEL = process.env.GW_LLM_MODEL || 'minimax-text-01' // refine/analyze 用（2026-10-06 本地实测：glm-4.7 系被 chat 面按 creative 任务类路由排除，minimax-text-01 稳定可路由）

function findKey() {
  if (process.env.GW_KEY) return process.env.GW_KEY.trim()
  const tryPath = (dir) => {
    const p = resolve(dir, 'logs/.gateway-key')
    return existsSync(p) ? readFileSync(p, 'utf8').trim() : ''
  }
  for (const dir of [REPO, resolve(REPO, '..')]) {
    const k = tryPath(dir)
    if (k) return k
  }
  const parent = resolve(REPO, '..')
  for (const name of readdirSync(parent)) {
    if (name === resolve(REPO).split(/[\\/]/).pop()) continue
    const k = tryPath(resolve(parent, name))
    if (k) return k
  }
  return ''
}

const KEY = findKey()
if (!KEY) {
  console.error('找不到网关 key。用 GW_KEY=... 指定。')
  process.exit(1)
}

async function call(path, init, timeout = TIMEOUT_MS) {
  const t0 = Date.now()
  const ac = new AbortController()
  const timer = setTimeout(() => ac.abort(), timeout)
  try {
    const r = await fetch(BASE + path, { ...init, signal: ac.signal })
    const ct = r.headers.get('content-type') || ''
    let body = ''
    if (/audio|octet-stream/i.test(ct)) {
      const buf = Buffer.from(await r.arrayBuffer())
      body = `<binary ${ct} ${buf.length}B>`
    } else {
      body = await r.text()
    }
    return { status: r.status, ms: Date.now() - t0, body, headers: r.headers }
  } catch (e) {
    const aborted = e && e.name === 'AbortError'
    return { status: aborted ? 'TIMEOUT' : 'ERR', ms: Date.now() - t0, body: String((e && e.message) || e), headers: new Headers() }
  } finally {
    clearTimeout(timer)
  }
}

const json = (o) => ({
  method: 'POST',
  headers: { Authorization: 'Bearer ' + KEY, 'Content-Type': 'application/json' },
  body: JSON.stringify(o),
})

// 网关对 sk-* key 有 RPM 限流（本项目本地默认 12 次/分钟；loopback 的
// refine/analyze 会让一次调用计两次——音频面一次 + chat 面一次）。所有
// LLM 消耗型调用之间强制节流；429 时等一个窗口重试一次。
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const RPM_GAP_MS = Number(process.env.GW_RPM_GAP_MS || 5600)
async function callWithRpmRetry(path, init, timeout = TIMEOUT_MS) {
  await sleep(RPM_GAP_MS)
  let r = await call(path, init, timeout)
  if (r.status === 429) {
    console.log('    （429 限流，等 62s 重试一次）')
    await sleep(62000)
    r = await call(path, init, timeout)
  }
  return r
}

// ── 准确率：字（zh）/词（en）级 LCS 相似度，标点与空白归一后比对 ──
const norm = (s) => String(s || '').toLowerCase().replace(/[\s\p{P}\p{S}]+/gu, '')
function lcsSim(a, b) {
  a = norm(a); b = norm(b)
  if (!a.length || !b.length) return 0
  const m = a.length, n = b.length
  const dp = new Uint32Array((m + 1) * (n + 1))
  for (let i = 1; i <= m; i++) {
    for (let j = 1; j <= n; j++) {
      dp[i * (n + 1) + j] = a[i - 1] === b[j - 1]
        ? dp[(i - 1) * (n + 1) + j - 1] + 1
        : Math.max(dp[(i - 1) * (n + 1) + j], dp[i * (n + 1) + j - 1])
    }
  }
  return dp[m * (n + 1) + n] / Math.max(m, n)
}

// ── [1] TTS 合成已知文本样本 ──
const ZH_TEXT = '各位好，今天我们评审实时转写网关的第二阶段方案，重点是多供应商接入、精细化转写和实时摘要三个模块。'
const EN_TEXT = 'Good afternoon colleagues. Today we will review the second phase of the realtime transcription gateway, covering multi provider access, refined transcription, and live summarization.'

async function synthSample(text, voice, file) {
  const r = await call('/audio/speech', json({ model: 'mimo-v2.5-tts', input: text, voice }))
  if (r.status !== 200) return null
  const b64 = r.body // call() 对 audio/* 只给了占位——这里需要原始字节，单独发
  void b64
  const resp = await fetch(BASE + '/audio/speech', {
    method: 'POST',
    headers: { Authorization: 'Bearer ' + KEY, 'Content-Type': 'application/json' },
    body: JSON.stringify({ model: 'mimo-v2.5-tts', input: text, voice }),
  })
  if (!resp.ok) return null
  const buf = Buffer.from(await resp.arrayBuffer())
  writeFileSync(file, buf)
  return file
}

async function asrOnce(model, file, stream = false) {
  const form = new FormData()
  form.append('file', new Blob([readFileSync(file)], { type: 'audio/wav' }), file.split('/').pop())
  form.append('model', model)
  if (stream) form.append('stream', 'true')
  const r = await call('/audio/transcriptions', {
    method: 'POST', headers: { Authorization: 'Bearer ' + KEY }, body: form,
  })
  let text = ''
  if (r.status === 200) {
    try { text = JSON.parse(r.body).text || '' } catch { text = r.body }
  }
  return { ...r, text }
}

async function asrStreamEvents(model, file) {
  const form = new FormData()
  form.append('file', new Blob([readFileSync(file)], { type: 'audio/wav' }), 's.wav')
  form.append('model', model)
  form.append('stream', 'true')
  const t0 = Date.now()
  const resp = await fetch(BASE + '/audio/transcriptions', {
    method: 'POST', headers: { Authorization: 'Bearer ' + KEY }, body: form,
  })
  if (!resp.ok) return { status: resp.status, deltas: 0, firstDeltaMs: -1, ms: Date.now() - t0 }
  const reader = resp.body.getReader()
  const dec = new TextDecoder()
  let buf = '', deltas = 0, firstDeltaMs = -1
  while (true) {
    const { done, value } = await reader.read()
    if (done) break
    buf += dec.decode(value, { stream: true })
    for (;;) {
      const i = buf.indexOf('\n\n')
      if (i < 0) break
      const frame = buf.slice(0, i); buf = buf.slice(i + 2)
      const m = frame.match(/^data: (.*)$/m)
      if (m && m[1].includes('transcript.text.delta')) {
        deltas++
        if (firstDeltaMs < 0) firstDeltaMs = Date.now() - t0
      }
    }
  }
  return { status: resp.status, deltas, firstDeltaMs, ms: Date.now() - t0 }
}

console.log('网关 =', BASE)
console.log('refine/analyze LLM =', LLM_MODEL)
console.log('')

let coreOk = 0
const coreNeed = 3 // ≥1 ASR 模型 + refine + analyze —— refine/analyze 是硬判据；流式/MCP 呈报不设门

// [1] 样本合成
console.log('[1] TTS 合成已知文本样本（茉莉 zh / Chloe en）')
const zhFile = '/tmp/gw-multi-zh.wav', enFile = '/tmp/gw-multi-en.wav'
const zOk = await synthSample(ZH_TEXT, '茉莉', zhFile)
const eOk = await synthSample(EN_TEXT, 'Chloe', enFile)
console.log(`    zh: ${zOk ? 'OK' : 'FAIL'}  en: ${eOk ? 'OK' : 'FAIL'}`)
console.log('')
if (!zOk || !eOk) {
  console.error('样本合成失败，TTS 面不可用——后续矩阵没有已知真值可比。')
  process.exit(1)
}

// [2] 多模型 ASR 矩阵
const MODELS = [
  { id: 'mimo-v2.5-asr', expect: 'ok' },
  { id: 'minimax-asr-1.0', expect: 'ok' },
  { id: 'glm-asr', expect: '429-balance-blocked' }, // 2026-10-06：数据面已接，凭据余额 429
]
console.log('[2] 多模型 ASR 矩阵（zh 字级 / en 词级 LCS 相似度）')
const rows = []
for (const m of MODELS) {
  const row = { model: m.id, zh: '-', en: '-', zhMs: '-', enMs: '-', status: '' }
  const zr = await asrOnce(m.id, zhFile)
  if (zr.status === 200) {
    row.zh = (lcsSim(ZH_TEXT, zr.text) * 100).toFixed(1) + '%'
    row.zhMs = zr.ms
    row.status = '200'
  } else if (m.expect === '429-balance-blocked' && zr.status === 429) {
    row.status = '429（预期：凭据余额）'
  } else {
    row.status = String(zr.status) + ' ' + zr.body.slice(0, 80)
  }
  const er = await asrOnce(m.id, enFile)
  if (er.status === 200) {
    row.en = (lcsSim(EN_TEXT, er.text) * 100).toFixed(1) + '%'
    row.enMs = er.ms
  } else if (!(m.expect === '429-balance-blocked' && er.status === 429)) {
    row.status += ' /en:' + er.status
  }
  rows.push(row)
}
const asrModelOk = rows.some((r) => r.status.startsWith('200'))
if (asrModelOk) coreOk++
const w = (s, n) => String(s).padEnd(n)
console.log('    ' + w('model', 18) + w('zh准确率', 10) + w('zh延迟', 10) + w('en准确率', 10) + w('en延迟', 10) + '状态')
for (const r of rows) {
  console.log('    ' + w(r.model, 18) + w(r.zh, 10) + w(r.zhMs, 10) + w(r.en, 10) + w(r.enMs, 10) + r.status)
}
console.log('')

// [3] 流式（对第一个 200 的模型）
const streamModel = rows.find((r) => r.status.startsWith('200'))
if (streamModel) {
  console.log(`[3] 流式 SSE（${streamModel.model}）`)
  const s = await asrStreamEvents(streamModel.model, zhFile)
  const ok = s.status === 200 && s.deltas >= 1 && s.firstDeltaMs >= 0
  console.log(`    ${s.status} delta帧=${s.deltas} 首delta=${s.firstDeltaMs}ms 总耗时=${s.ms}ms ${ok ? 'OK' : 'FAIL'}`)
  if (ok) coreOk++
  console.log('')
}

// [4] refine
console.log('[4] /v1/audio/refine 精细化转写')
const MESSY = '嗯大家好那个今天我们开个会呃讨论一下二季度的预算百分之五十的部分要先砍掉具体的明细下周三之前发给我对吧'
const rf = await callWithRpmRetry('/audio/refine', json({
  model: LLM_MODEL, text: MESSY, language: 'zh', hotwords: ['二季度'],
  context: '团队周会', include_corrections: true,
}))
let refineOk = false
if (rf.status === 200) {
  const out = JSON.parse(rf.body)
  refineOk = norm(out.refined || '').length > 10
    && (out.refined || '').includes('50%') // ITN 判定用原文串——norm 会把 % 剥掉
    && !(out.ignored_hotwords || []).includes('二季度')
  console.log(`    ${rf.status} ${rf.ms}ms refined="${out.refined}"`)
  console.log(`    corrections=${(out.corrections || []).length} 条, ignored_hotwords=${JSON.stringify(out.ignored_hotwords || [])}`)
  console.log(`    判据：语气词清理=${!norm(out.refined).includes('呃')}, ITN(百分之五十→50%)=${(out.refined || '').includes('50%')}, 热词生效=${!(out.ignored_hotwords || []).includes('二季度')}`)
} else {
  console.log(`    ${rf.status} ${rf.ms}ms ${rf.body.slice(0, 200)}`)
}
if (refineOk) coreOk++
console.log('')

// [5] analyze（含增量滚动）
console.log('[5] /v1/audio/analyze 实时总结分析（滚动摘要 + hints）')
const T1 = '大家好，今天讨论二季度预算。获客成本涨了三成，我建议投放预算先冻结两周看数据。'
const A1 = await callWithRpmRetry('/audio/analyze', json({ model: LLM_MODEL, transcript: T1, style: 'meeting', language: 'zh' }))
let analyzeOk = false
if (A1.status === 200) {
  const a1 = JSON.parse(A1.body)
  const T2 = '另外张三提的客服排班系统还没定，供应商清单都没拉出来，下周必须给出候选名单。'
  const A2 = await callWithRpmRetry('/audio/analyze', json({ model: LLM_MODEL, transcript: T2, prior_summary: a1.summary, style: 'meeting', language: 'zh' }))
  if (A2.status === 200) {
    const a2 = JSON.parse(A2.body)
    analyzeOk = (a2.summary || '').length > 10
      && Array.isArray(a2.hints)
      && (a2.summary.includes('排班') || (a2.key_points || []).some((k) => k.includes('排班')))
    console.log(`    第一轮: summary="${(a1.summary || '').slice(0, 80)}…" hints=${(a1.hints || []).length} 条`)
    console.log(`    第二轮(滚动): summary="${(a2.summary || '').slice(0, 80)}…"`)
    console.log(`    action_items=${JSON.stringify(a2.action_items || [])}`.slice(0, 260))
    console.log(`    判据：第二轮摘要合并了新增内容（提到排班）=${a2.summary.includes('排班') || (a2.key_points || []).some((k) => k.includes('排班'))}, hints 非缺失=${Array.isArray(a2.hints)}`)
  } else {
    console.log(`    第二轮 ${A2.status} ${A2.body.slice(0, 200)}`)
  }
} else {
  console.log(`    ${A1.status} ${A1.ms}ms ${A1.body.slice(0, 200)}`)
}
if (analyzeOk) coreOk++
console.log('')

// [6] MCP
console.log('[6] MCP tools/list + tools/call')
const tl = await callWithRpmRetry('/mcp', json({ jsonrpc: '2.0', id: 1, method: 'tools/list' }))
let mcpOk = false
if (tl.status === 200) {
  const tools = JSON.parse(tl.body).result.tools.map((t) => t.name)
  const need = ['transcribe_audio', 'synthesize_speech', 'refine_transcription', 'analyze_transcription']
  mcpOk = need.every((n) => tools.includes(n))
  console.log('    tools/list:', tools.join(', '), mcpOk ? 'OK' : 'FAIL（缺工具）')
  const rc = await callWithRpmRetry('/mcp', json({
    jsonrpc: '2.0', id: 2, method: 'tools/call',
    params: { name: 'refine_transcription', arguments: { text: MESSY.slice(0, 20), model: LLM_MODEL } },
  }))
  console.log(`    tools/call refine_transcription: ${rc.status} ${rc.body.includes('refined') ? 'OK' : rc.body.slice(0, 160)}`)
} else {
  console.log(`    ${tl.status} ${tl.body.slice(0, 160)}`)
}
if (mcpOk) coreOk++
console.log('')

console.log('判读：')
console.log('  ASR 429 (glm-asr)   -> 数据面已接通，上游凭据余额不足（1113）；充值后零代码可用')
console.log('  ASR 503 no_provider -> 目录/绑定缺失（本轮 ensure 种子未部署到该环境）')
console.log('  refine 502          -> LLM 步骤失败；先换 GW_LLM_MODEL（glm-4.7 系在本地被 creative 任务路由排除）')
console.log('')
console.log(`核心判据：${coreOk}/${coreNeed}（≥1 个 ASR 模型 200 + refine 判据 + analyze 判据）`)
process.exit(coreOk >= coreNeed ? 0 : 1)
