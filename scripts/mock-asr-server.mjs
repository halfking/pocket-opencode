// mock-asr-server.mjs — OpenAI 兼容转写端点的本地假上游（验证用，非生产代码）
//
// 为什么需要它：外部 ASR 的**成功路径**没法靠真实服务验证——
//  · api.openai.com 在本机 i/o timeout，填了真 key 也发不出去；
//  · 而只验失败路径会漏掉一整类问题：WAV 头写错、multipart 少字段、
//    语种没带（见 stt.Target.Language）、返回结构解析不对。
//    这些都要「上游真的收到了音频并回了文字」才算验过。
//
// 它返回的 text 里带**可核对的事实**（收到的语种、音频时长、字节数、模型名），
// 不是一句假的「转写成功」——这样真机上看到的那段文字能反过来证明
// 音频确实完整地到达了上游，而不是前端自己编的。
//
// 用法：
//   node scripts/mock-asr-server.mjs [--port 18400] [--host 0.0.0.0]
//
// 配合（让 SSRF 校验放行私网目标，仅本地验证用）：
//   POCKET_LLM_GATEWAY_ALLOW_PRIVATE=true
import { createServer } from 'node:http'

const argv = process.argv.slice(2)
const argOf = (name, dflt) => {
  const i = argv.indexOf(name)
  return i >= 0 && argv[i + 1] ? argv[i + 1] : dflt
}
const port = Number(argOf('--port', '18400'))
const host = argOf('--host', '0.0.0.0')

/** 从 multipart body 里取出某个普通字段的值。 */
function field(body, name) {
  const re = new RegExp(`name="${name}"\\r\\n\\r\\n([^\\r\\n]*)`)
  const m = body.toString('latin1').match(re)
  return m ? m[1] : ''
}

/** 取出 file 段（含 filename 之后到下一个 boundary 之前的原始字节）。 */
function filePart(body) {
  const buf = Buffer.isBuffer(body) ? body : Buffer.from(body, 'latin1')
  const head = buf.toString('latin1')
  const startTag = 'name="file"'
  const at = head.indexOf(startTag)
  if (at < 0) return null
  const headerEnd = buf.indexOf('\r\n\r\n', at)
  if (headerEnd < 0) return null
  const dataStart = headerEnd + 4
  const next = buf.indexOf('\r\n--', dataStart)
  return buf.subarray(dataStart, next < 0 ? buf.length : next)
}

/** 读 WAV 的 fmt/data 块，算出真实时长（秒）。读不出就返回 null。 */
function wavSeconds(buf) {
  if (!buf || buf.length < 44 || buf.toString('latin1', 0, 4) !== 'RIFF') return null
  if (buf.toString('latin1', 8, 12) !== 'WAVE') return null
  let pos = 12
  let byteRate = 0
  while (pos + 8 <= buf.length) {
    const id = buf.toString('latin1', pos, pos + 4)
    const size = buf.readUInt32LE(pos + 4)
    if (id === 'fmt ') byteRate = buf.readUInt32LE(pos + 16) // fmt 负载偏移 8（RIFF 头 8 + 块头 8）
    if (id === 'data') return byteRate ? size / byteRate : null
    pos += 8 + size + (size % 2)
  }
  return null
}

let n = 0
const server = createServer((req, res) => {
  const chunks = []
  req.on('data', (c) => chunks.push(c))
  req.on('end', () => {
    const body = Buffer.concat(chunks)
    if (!req.url.includes('/audio/transcriptions')) {
      res.writeHead(404, { 'content-type': 'application/json' })
      res.end(JSON.stringify({ error: { message: 'not found' } }))
      return
    }
    n += 1
    const audio = filePart(body)
    const secs = wavSeconds(audio)
    const model = field(body, 'model')
    const language = field(body, 'language')
    const auth = req.headers.authorization || ''

    console.log(
      `[mock-asr #${n}] ${req.method} ${req.url} ` +
        `bytes=${body.length} audio=${audio ? audio.length : 0} ` +
        `wav=${secs === null ? 'BAD' : secs.toFixed(2) + 's'} ` +
        `model=${model || '-'} language=${language || '(缺失)'} auth=${auth ? 'yes' : 'no'}`,
    )

    // 故意对「音频不是合法 WAV」和「没带 language」报错：
    // 这两个正是本仓库真实踩过的坑，用假上游守死它们。
    if (!audio || secs === null) {
      res.writeHead(400, { 'content-type': 'application/json' })
      res.end(JSON.stringify({ error: { message: 'mock-asr: 收到的不是合法 WAV' } }))
      return
    }
    if (!language) {
      res.writeHead(400, { 'content-type': 'application/json' })
      res.end(JSON.stringify({ error: { message: 'mock-asr: 请求缺少 language 字段' } }))
      return
    }

    const text =
      `【mock-asr 已收到音频】模型 ${model}，语种 ${language}，` +
      `音频 ${secs.toFixed(1)} 秒 / ${audio.length} 字节，授权头${auth ? '已带' : '缺失'}。`
    res.writeHead(200, { 'content-type': 'application/json' })
    res.end(JSON.stringify({ text }))
  })
})

server.listen(port, host, () => {
  console.log(`[mock-asr] listening on http://${host}:${port}/v1`)
})
