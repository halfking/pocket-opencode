// 探测已配置的 LLM 网关是否提供 OpenAI 兼容的音频转写端点。
// 语音转文字目前只认 POCKET_GROQ_API_KEY（另一个第三方 key，用户没给）。
// 如果网关自己带 /audio/transcriptions，就能像邮件分类那样用现有配置兜底。
import { readFileSync } from 'node:fs'

const KEY = readFileSync('logs/.gateway-key', 'utf8').trim()
const BASE = process.env.GW_BASE || 'https://llm.kxpms.cn/v1'

async function probe(path, init = {}) {
  try {
    const r = await fetch(BASE + path, init)
    const t = await r.text()
    return { path, status: r.status, body: t.slice(0, 300) }
  } catch (e) {
    return { path, status: 'ERR', body: String(e && e.message || e) }
  }
}

console.log('网关 =', BASE)
console.log('')

// 1) 模型目录：看有没有转写类模型
const models = await probe('/models', { headers: { Authorization: 'Bearer ' + KEY } })
console.log('[1] GET /models ->', models.status)
let ids = []
try {
  const j = JSON.parse(models.body)
  ids = (j.data || []).map((m) => m.id)
  console.log('    模型数 =', ids.length)
  const audioish = ids.filter((x) => /whisper|audio|transcri|asr|voice|speech/i.test(x))
  console.log('    转写类模型 =', audioish.length ? audioish.join(', ') : '（无）')
} catch {
  console.log('    响应不是 JSON:', models.body.slice(0, 160))
}

// 2) 音频转写端点是否存在：用一段 1 秒静音 WAV 打过去
//    端点存在但模型错 -> 4xx 且带 model 相关信息；端点不存在 -> 404
const wav = Buffer.alloc(44 + 8000)
wav.write('RIFF', 0); wav.writeUInt32LE(36 + 8000, 4); wav.write('WAVE', 8)
wav.write('fmt ', 12); wav.writeUInt32LE(16, 16); wav.writeUInt16LE(1, 20)
wav.writeUInt16LE(1, 22); wav.writeUInt32LE(8000, 24); wav.writeUInt32LE(8000, 28)
wav.writeUInt16LE(1, 32); wav.writeUInt16LE(8, 34)
wav.write('data', 36); wav.writeUInt32LE(8000, 40)

const form = new FormData()
form.append('file', new Blob([wav], { type: 'audio/wav' }), 'probe.wav')
form.append('model', ids.find((x) => /whisper|transcri|asr/i.test(x)) || 'whisper-1')

const tr = await probe('/audio/transcriptions', {
  method: 'POST',
  headers: { Authorization: 'Bearer ' + KEY },
  body: form,
})
console.log('')
console.log('[2] POST /audio/transcriptions ->', tr.status)
console.log('    响应 =', tr.body.replace(/\n/g, ' ').slice(0, 260))
console.log('')
console.log('判读：')
console.log('  404            -> 网关没有这个端点，语音转写必须另配服务')
console.log('  400/422 且提模型 -> 端点在，但需要正确的转写模型名')
console.log('  200            -> 网关自带转写能力，可以拿来兜底')
