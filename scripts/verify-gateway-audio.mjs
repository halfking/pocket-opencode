// 复验「网关是否真的打通了 TTS / ASR」。
//
// 为什么需要这个脚本：2026-10-01 三轮实测下来，「模型在 /models 目录里」
// 与「模型有可达的上游」是两件事。mimo-v2.5-tts / -voiceclone / -voicedesign
// 以及 mimo-v2.5-asr 全部在目录里，但全部 503 no_candidate。
// 目录里有 ≠ 能调用，所以每次都要用这个脚本重新取证，不要凭目录下结论。
//
// 用法：
//   node scripts/verify-gateway-audio.mjs                 # 自动找 key
//   node scripts/verify-gateway-audio.mjs mimo-v2.5-tts   # 指定模型
//   GW_BASE=http://127.0.0.1:8080/v1 node scripts/verify-gateway-audio.mjs
//   GW_KEY=sk-xxx node scripts/verify-gateway-audio.mjs
//
// 退出码：0 = 至少有一条音频路径可用；1 = 全都不可用。
import { existsSync, readFileSync, readdirSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = dirname(fileURLToPath(import.meta.url))
const REPO = resolve(HERE, '..')
const BASE = (process.env.GW_BASE || 'https://llm.kxpms.cn/v1').replace(/\/+$/, '')

// key 查找顺序：显式环境变量 → 仓库 logs/ → 同级工作区 logs/。
// 注意是**同级**而不是父目录：主工作区 C:\workspace\openpocket 与 worktree
// C:\workspace\openpocket-wt-stt 是兄弟关系，只往上找一层会得到 C:\workspace\logs，
// 永远找不到 key —— 表现为「key 缺失」的假故障。worktree 里也没有 logs/
// （未跟踪文件不进 worktree），所以这里必须扫同级目录。
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
const TARGET = process.argv[2] || 'mimo-v2.5-tts'
const TIMEOUT_MS = Number(process.env.GW_TIMEOUT_MS || 45000)

if (!KEY) {
  console.error('找不到网关 key。用 GW_KEY=... 指定，或确保 logs/.gateway-key 存在。')
  process.exit(1)
}

// 一段 1 秒 8kHz 单声道静音 WAV（44 字节头 + 8000 字节数据）。
// 端点存在但模型错会回 4xx 且带模型信息；端点不存在直接 404。
function silentWav() {
  const b = Buffer.alloc(44 + 8000)
  b.write('RIFF', 0); b.writeUInt32LE(36 + 8000, 4); b.write('WAVE', 8)
  b.write('fmt ', 12); b.writeUInt32LE(16, 16); b.writeUInt16LE(1, 20)
  b.writeUInt16LE(1, 22); b.writeUInt32LE(8000, 24); b.writeUInt32LE(8000, 28)
  b.writeUInt16LE(16, 32); b.writeUInt16LE(8, 34)
  b.write('data', 36); b.writeUInt32LE(8000, 40)
  return b
}

async function call(path, init) {
  const t0 = Date.now()
  const ac = new AbortController()
  const timer = setTimeout(() => ac.abort(), TIMEOUT_MS)
  try {
    const r = await fetch(BASE + path, { ...init, signal: ac.signal })
    // 二进制端点（/audio/speech）只要状态码；有响应体才读文本。
    const ct = r.headers.get('content-type') || ''
    let body = ''
    if (!/audio|octet-stream/i.test(ct)) body = (await r.text()).slice(0, 400)
    else body = `<binary ${ct}>`
    return { path, status: r.status, ms: Date.now() - t0, body }
  } catch (e) {
    const aborted = e && e.name === 'AbortError'
    return { path, status: aborted ? 'TIMEOUT' : 'ERR', ms: Date.now() - t0, body: String(e && e.message || e) }
  } finally {
    clearTimeout(timer)
  }
}

const json = (o) => ({
  method: 'POST',
  headers: { Authorization: 'Bearer ' + KEY, 'Content-Type': 'application/json' },
  body: JSON.stringify(o),
})

console.log('网关 =', BASE)
console.log('目标模型 =', TARGET)
console.log('')

const verdict = []
const show = (r, note) => {
  console.log(`[${r.path}] ${r.status} ${r.ms}ms${note ? '  ' + note : ''}`)
  if (r.body) console.log('    ' + r.body.replace(/\n/g, ' ').slice(0, 320))
  console.log('')
}

// 1) 目录：音频类模型在不在
const models = await call('/models', { headers: { Authorization: 'Bearer ' + KEY } })
const audioish = []
if (models.status === 200) {
  const j = JSON.parse(await (await fetch(BASE + '/models', { headers: { Authorization: 'Bearer ' + KEY } })).text())
  const ids = (j.data || []).map((m) => m.id)
  for (const id of ids) if (/whisper|audio|transcri|asr|voice|speech|tts/i.test(id)) audioish.push(id)
  console.log(`[1] GET /models -> 200，共 ${ids.length} 个模型，音频类 ${audioish.length} 个`)
  console.log('    ' + (audioish.join(', ') || '（无）'))
  console.log('')
  console.log('    注意：目录里有 ≠ 有上游。判据是下面第 2/3 条的状态码。')
  console.log('')
} else {
  show(models, '（模型目录不可读，后续探测仍会继续）')
}

// 2) OpenAI 兼容 TTS 端点
const speech = await call('/audio/speech', json({ model: TARGET, input: '你好，这是连通性测试。', voice: 'default' }))
show(speech, speech.status === 200 ? 'TTS 可用' : '')
if (speech.status === 200) verdict.push('/audio/speech 可用')

// 3) OpenAI 兼容 ASR 端点
const form = new FormData()
form.append('file', new Blob([silentWav()], { type: 'audio/wav' }), 'probe.wav')
form.append('model', TARGET)
const tr = await call('/audio/transcriptions', {
  method: 'POST', headers: { Authorization: 'Bearer ' + KEY }, body: form,
})
show(tr, tr.status === 200 ? 'ASR 可用' : '')
if (tr.status === 200) verdict.push('/audio/transcriptions 可用')

// 4) 退路：把模型当 chat 调（网关目录里有些音频模型只接 chat）
const chat = await call('/chat/completions', json({
  model: TARGET, messages: [{ role: 'user', content: 'hi' }], max_tokens: 8,
}))
show(chat)
if (chat.status === 200) verdict.push('chat/completions 可用')

console.log('判读：')
console.log('  200                 -> 这条路径真通了')
console.log('  404                 -> 网关没实现这个端点（不是模型的问题）')
console.log('  503 no_candidate    -> 模型在目录里，但网关没配它的上游')
console.log('  429                 -> 限流（本项目网关 12 次/分钟），隔几十秒重试')
console.log('')
console.log('可用路径：' + (verdict.length ? verdict.join('、') : '无'))
process.exit(verdict.length ? 0 : 1)
