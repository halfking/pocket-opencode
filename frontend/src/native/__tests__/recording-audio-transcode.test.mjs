/**
 * recording-audio-transcode 单测 —— **直接 import 生产代码**。
 *
 * 为什么必须直接 import（本轮返工的原因）：
 *   初版把 `pcmToWavBytes` / `sliceToWavBytes` / `arrayBufferToBase64`
 *   **在测试里重抄了一遍**。后果是：改动真实实现（比如把 16k 写成 8k）
 *   这 7 条**依然全绿** —— 它们测的是测试自己，不是被测对象。
 *   「一个只会验证自己的测试」比没有测试更坏：它让人以为覆盖到了。
 *
 *   当时给自己找的理由是「node 的 type-strip 不支持参数属性」——
 *   那是**自找的**：去掉参数属性（改写构造器赋值）后 import 立刻可用，
 *   而本仓本来就有 8 处 mjs 测试直接 `import ... from '../x.ts'` 的先例
 *   （如 src/native/__tests__/outboxDrain.test.mjs）。
 *
 *   负对照已做：把生产 `TARGET_SAMPLE_RATE` 从 16000 改成 8000 后，
 *   「WAV 头自洽」等 7 条**全部变红** —— 这才是能证明它们在守东西。
 *
 * 仍需源码级断言的部分：浏览器专属行为（decodeAudioData 对 webm 分片
 * 能否解码、AudioContext 的存在性），那些在 Node 里无法执行，
 * 真机结论见 recording-audio-transcode.ts 头注释。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  pcmToWavBytes, sliceToWavBytes, arrayBufferToBase64, RollingWebmDecoder,
} from '../recording-audio-transcode.ts'

const HERE = dirname(fileURLToPath(import.meta.url))
const SRC = resolve(HERE, '../recording-audio-transcode.ts')
const RUNTIME = resolve(HERE, '../recordingRuntime.ts')

const SR = 48000
const TARGET_SR = 16000        // 与生产常量一致；下面的断言会顺带校验它确实是这个值

function header(buf) {
  const dv = new DataView(buf)
  const asc = (o, n) => String.fromCharCode(...new Uint8Array(buf, o, n))
  return {
    riff: asc(0, 4), wave: asc(8, 4), fmt: asc(12, 4), data: asc(36, 4),
    riffSize: dv.getUint32(4, true), channels: dv.getUint16(22, true),
    sampleRate: dv.getUint32(24, true), byteRate: dv.getUint32(28, true),
    blockAlign: dv.getUint16(32, true), bits: dv.getUint16(34, true),
    dataSize: dv.getUint32(40, true),
  }
}

// ---------------------------------------------------------------------------
// 纯函数：直接打生产实现
// ---------------------------------------------------------------------------

test('pcmToWavBytes：WAV 头自洽（1 秒 48k → 16k 单声道 16bit）', () => {
  const h = header(pcmToWavBytes(new Float32Array(SR), SR))
  assert.equal(h.riff, 'RIFF'); assert.equal(h.wave, 'WAVE')
  assert.equal(h.fmt, 'fmt '); assert.equal(h.data, 'data')
  assert.equal(h.sampleRate, TARGET_SR, '必须重采样到 16k')
  assert.equal(h.channels, 1, '单声道')
  assert.equal(h.bits, 16)
  assert.equal(h.blockAlign, 2)
  assert.equal(h.byteRate, TARGET_SR * 2)
  assert.equal(h.dataSize, TARGET_SR * 2, '1s @48k → 16000 点 → 32000 字节')
  assert.equal(h.riffSize, 36 + h.dataSize, 'RIFF 长度 = 36 + dataSize')
})

test('pcmToWavBytes：空输入产出合法空 WAV，不抛错', () => {
  const buf = pcmToWavBytes(new Float32Array(0), SR)
  assert.equal(buf.byteLength, 44)
  assert.equal(header(buf).dataSize, 0)
})

test('pcmToWavBytes：样本值钳位在 int16 范围内（含 NaN）', () => {
  // 夹具要够长到重采样后仍有输出：48k→16k 是 3:1，太短会被 floor() 吃掉。
  const input = new Float32Array([1, -1, 2, -2, 0.5, NaN, 1, -1, 0.25, -0.25, 0, 1])
  const buf = pcmToWavBytes(input, SR)
  const outLen = header(buf).dataSize / 2
  assert.ok(outLen > 0, '夹具太短，重采样后没有输出点')
  const dv = new DataView(buf)
  for (let i = 0; i < outLen; i++) {
    const v = dv.getInt16(44 + i * 2, true)
    assert.ok(Number.isFinite(v) && v >= -32768 && v <= 32767, `样本 ${i} 越界: ${v}`)
  }
})

test('pcmToWavBytes：真彩信号振幅保真（不是全 0）', () => {
  const n = SR                       // 1 秒
  const s = new Float32Array(n)
  for (let i = 0; i < n; i++) s[i] = Math.sin((2 * Math.PI * 440 * i) / SR)
  const buf = pcmToWavBytes(s, SR)
  const dv = new DataView(buf)
  const outLen = header(buf).dataSize / 2
  let peak = 0
  for (let i = 0; i < outLen; i++) peak = Math.max(peak, Math.abs(dv.getInt16(44 + i * 2, true)))
  assert.ok(peak > 30000, `440Hz 正弦的峰值应接近满量程，实得 ${peak}`)
})

test('sliceToWavBytes：时间窗只取指定区间', () => {
  const samples = new Float32Array(SR * 4)
  for (let i = 0; i < samples.length; i++) samples[i] = Math.floor(i / SR)
  const buf = sliceToWavBytes(samples, SR, 1, 2)
  assert.equal(header(buf).dataSize, TARGET_SR * 2, '[1,2) 秒 = 16000 点 @16k')
  const first = new DataView(buf).getInt16(44, true)
  assert.ok(first > 32000, `窗口起点应约等于 1.0，实得 ${first}`)
})

test('sliceToWavBytes：窗口越界收敛而不是抛错', () => {
  const samples = new Float32Array(SR)
  assert.equal(sliceToWavBytes(samples, SR, 5, 8).byteLength, 44, '起点越界 → 空')
  assert.equal(header(sliceToWavBytes(samples, SR, 0, 10)).dataSize, TARGET_SR * 2, '终点越界 → 截断')
  assert.equal(sliceToWavBytes(samples, SR, 2, 1).byteLength, 44, '倒序 → 空')
})

test('sliceToWavBytes：连续窗口不重叠不遗漏', () => {
  const samples = new Float32Array(SR * 3)
  const total = [0, 1, 2].reduce((n, i) => n + header(sliceToWavBytes(samples, SR, i, i + 1)).dataSize / 2, 0)
  assert.equal(total, TARGET_SR * 3)
})

test('arrayBufferToBase64：分块结果与一次性转换逐字节相同', () => {
  // 200KB > 32768，确保真的走了分块路径（一次性展开 12 秒 PCM 实测会爆栈）
  const size = 200000
  const buf = new ArrayBuffer(size)
  const dv = new DataView(buf)
  for (let i = 0; i < size; i++) dv.setUint8(i, i % 256)
  assert.equal(arrayBufferToBase64(buf), Buffer.from(new Uint8Array(buf)).toString('base64'))
})

test('arrayBufferToBase64：空 buffer', () => {
  assert.equal(arrayBufferToBase64(new ArrayBuffer(0)), '')
})

// ---------------------------------------------------------------------------
// RollingWebmDecoder：游标语义（注入假解码器，Node 里造不出真 WebM）
// ---------------------------------------------------------------------------

const fake = (sec, sampleRate = SR) => {
  const n = Math.round(sec * sampleRate)
  const samples = new Float32Array(n)
  for (let i = 0; i < n; i++) samples[i] = Math.sin(i / 100) * 0.5
  return { samples, sampleRate, durationSec: sec }
}

test('RollingWebmDecoder：无分片时返回 null（不发空请求）', async () => {
  const d = new RollingWebmDecoder(async () => fake(3))
  assert.equal(await d.takeNewWindow(), null)
  d.dispose()
})

test('RollingWebmDecoder：连续取用产出互不重叠的新增窗口', async () => {
  let total = 0
  const d = new RollingWebmDecoder(async () => { total += 3; return fake(total) })
  // 必须先 push：生产实现在 `!this.parts.length` 时直接返回 null（不发空请求）。
  // 漏掉这步的版本会得到「三次都应有新增内容」这种和实现无关的红。
  d.push(new Blob([new Uint8Array(8)]))
  const ws_ = [await d.takeNewWindow(), await d.takeNewWindow(), await d.takeNewWindow()]
  assert.ok(ws_.every(Boolean), '三次都应有新增内容')
  for (const w of ws_) assert.equal(header(w).dataSize, 3 * TARGET_SR * 2, '每片 3 秒')
  d.dispose()
})

test('RollingWebmDecoder：窗口只含新增内容（不重发已发送部分）', async () => {
  // 假解码器按「调用次数」推进总时长：第 1 次 3s、第 2 次 6s。
  // 负对照已做：把生产实现的窗口起点从游标改成 0（重发累计音频）后本条变红
  // （第二片会变成 6 秒 = 192000 字节）。
  let calls = 0
  const d = new RollingWebmDecoder(async () => fake(++calls * 3))
  d.push(new Blob([new Uint8Array(8)]))
  const w1 = await d.takeNewWindow()
  const w2 = await d.takeNewWindow()
  assert.equal(header(w1).dataSize, 3 * TARGET_SR * 2, '第一片 3 秒')
  assert.equal(header(w2).dataSize, 3 * TARGET_SR * 2,
    '第二片必须只含新增的 3 秒 —— 重发累计会得到 6 秒 / 192000 字节')
  d.dispose()
})

test('RollingWebmDecoder：没有新内容时返回 null 而不是空 WAV', async () => {
  // 0 长度音频会让上游回 502 empty transcript，界面上凭空多一条错误。
  const d = new RollingWebmDecoder(async () => fake(0.05))
  d.push(new Blob([new Uint8Array(10)]))
  assert.equal(await d.takeNewWindow(0.15), null)
  d.dispose()
})

test('RollingWebmDecoder：解码抛错时向上抛（由调用方降级）', async () => {
  const d = new RollingWebmDecoder(async () => { throw new Error('UnsupportedError') })
  d.push(new Blob([new Uint8Array(10)]))
  await assert.rejects(() => d.takeNewWindow())
  d.dispose()
})

test('RollingWebmDecoder：dispose 后不再接受分片', async () => {
  const d = new RollingWebmDecoder(async () => fake(3))
  d.dispose()
  d.push(new Blob([new Uint8Array(10)]))
  assert.equal(await d.takeNewWindow(), null)
})

test('RollingWebmDecoder：takeFull 拿整段且不消耗游标', async () => {
  let total = 3
  const d = new RollingWebmDecoder(async () => fake(total))
  d.push(new Blob([new Uint8Array(10)]))
  const inc = await d.takeNewWindow()
  total = 6
  const full = await d.takeFull()
  assert.equal(header(inc).dataSize, 3 * TARGET_SR * 2)
  assert.equal(header(full).dataSize, 6 * TARGET_SR * 2, 'takeFull 应拿当下全部')
  d.dispose()
})

// ---------------------------------------------------------------------------
// 源码级契约（浏览器专属行为 / 生命周期顺序，无法在 Node 里执行）
// ---------------------------------------------------------------------------

const src = readFileSync(SRC, 'utf8')
const runtime = readFileSync(RUNTIME, 'utf8')

test('契约：解码器是「滚动累计」而非逐片独立解码', () => {
  // 真机实测：片0 22330B 可解，片1 700B / 片3 111B 全部 UnsupportedError
  // （WebM 初始化段只在第一片出现）。逐片解码在第 2 片必崩。
  //
  // 用**计数式硬约束**而不是负向文本匹配：初版的 doesNotMatch 是恒真的，
  // 变异后仍全绿（详见本文件头）。改成「恰好 3 处全量拼接」后才真咬住。
  assert.match(src.slice(src.indexOf('async takeNewWindow'), src.indexOf('async takeFull')),
    /new Blob\(this\.parts/, 'takeNewWindow 必须拼接全部分片')
  assert.match(src.slice(src.indexOf('async takeFull'), src.indexOf('dispose()')),
    /new Blob\(this\.parts/, 'takeFull 也必须拼接全部分片')
  assert.equal((src.match(/new Blob\(this\.parts/g) || []).length, 3,
    '应恰好 3 处累计拼接（takeNewWindow / takeFull / fullBlob）')
  assert.doesNotMatch(src, /this\.parts\[this\.parts\.length - 1\]/,
    '不能用最后一片单独解码')
})

test('契约：base64 分块转换（一次性展开会爆栈，12 秒 PCM 实测 RangeError）', () => {
  assert.match(src, /CHUNK = 32768/)
  assert.match(src, /String\.fromCharCode\.apply/, '必须分块 apply')
  assert.doesNotMatch(src, /String\.fromCharCode\(\.\.\.bytes\)/, '不能一次性展开')
})

test('契约：模块不含 TS 参数属性（否则单测无法 import 生产代码）', () => {
  // 这条是本文件头那条返工教训的护栏：初版用 `constructor(private readonly x)`
  // 导致 node strip-only 报错，于是测试只好重抄实现 —— 那等于没测。
  //
  // ⚠️ 必须**先剥注释**再匹配：模块头注释里就写着 `constructor(private readonly x)`
  // 这个反例原文，不剥的话本条恒红（第一次跑就踩了这个）。
  const code = src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
  assert.doesNotMatch(code, /constructor\s*\([^)]*\b(private|public|protected|readonly)\b/,
    '构造器里不得有参数属性，node --experimental-strip-types 不支持')
  // 同时保证纯函数确实被本测试 import 了（不是本地副本）。
  //
  // ⚠️ 两个踩过的坑：
  //   ① 不能用 slice(0, 2000)：文件头注释很长，import 在第 29 行、偏移 >2000，
  //      截断会漏掉它而恒红。
  //   ② 正则不能要求整行：多行 import 的最后一行是 `} from '...'`，以 `}` 结尾。
  const self = readFileSync(resolve(HERE, 'recording-audio-transcode.test.mjs'), 'utf8')
  assert.match(self, /from '\.\.\/recording-audio-transcode\.ts'/, '测试必须直接 import 生产模块')
  assert.doesNotMatch(self.slice(0, self.indexOf('const SR =')),
    /function pcmToWavBytes|function sliceToWavBytes|function arrayBufferToBase64/,
    '不得在测试里重抄生产实现 —— 那样改真实代码测试也不会红')
})

test('契约：runtime 的即时转写走 WAV 通道（不是原始 webm）', () => {
  assert.match(runtime, /transcribeIncremental\(\{/, '必须走 transcribeIncremental')
  assert.match(runtime, /audioBase64: arrayBufferToBase64\(wav\)/, '必须传已转好的 base64')
  assert.match(runtime, /filename: 'chunk\.wav'/, '文件名必须声明 wav')
  assert.doesNotMatch(runtime, /filename: `chunk\$\{filenameForMimeType\(blob\.type/,
    '不能把原始 blob 的扩展名发给上游（真机上就是 .webm，网关 400）')
})

test('契约：兜底全量转写也转码（否则停止录音时仍失败）', () => {
  assert.match(runtime, /takeFull\(\)/, 'stop() 的兜底必须用解码器取整段')
  assert.match(runtime, /new Blob\(\[payload\], \{ type: 'audio\/wav' \}\)/, '兜底上传必须是 wav')
})

test('契约：出字即清陈旧 error（判据是「有没有字」而非「有没有报错」）', () => {
  // ⚠️ 这条断言被真机实测推翻过一次，形状值得留档：
  //
  // 错法 1：把清理写在 `if (res.error) return` 之后 ⇒ **永远走不到**，
  //        因为出问题那些片确实带 error。
  // 错法 2：按「本片无 error」清 ⇒ 也走不到（空 error 的片多半也没字）。
  // 对的判据：**本片产出了文字**（res.text 非空）⇒ 错误描述的是上一段，
  //          已经过去，应当清除。
  //
  // 证据：服务端 internal/stt/incremental.go 在某片失败时返回
  // `{text: <已累积>, error: <本片错误>}`，两者**同时存在**是常态
  // （16 片实测 8 片如此），失败片多为纯静音 empty transcript。
  const sendIdx = runtime.indexOf('private async sendSlice')
  const slice = runtime.slice(sendIdx, runtime.indexOf('  async stop()', sendIdx))
  assert.ok(sendIdx > 0 && slice.length > 500, 'sendSlice 切片范围异常')

  const textIdx = slice.indexOf('if (res.text.trim())')
  const clearIdx = slice.indexOf("this.error.value = ''", textIdx)
  const errSetIdx = slice.indexOf('sttFailureText(res.error')

  assert.ok(textIdx > 0, '必须有「本片有文本」分支')
  assert.ok(clearIdx > textIdx && clearIdx - textIdx < 400,
    '清 error 必须紧跟在「有文本」判断之后（不是放在 res.error 分支里）')
  assert.ok(errSetIdx > 0, '必须有设置 error 的分支')
  // 反向锁：清理不得位于 error 设置之前（那样错误一设就被自己抹掉）
  assert.ok(clearIdx < errSetIdx, '清理必须发生在「无字+有错」设置之后，否则错误永远显示不出来')
})

test('契约：解码器不释放太早（cleanupMedia 早于兜底转写）', () => {
  const cleanupIdx = runtime.indexOf('this.cleanupMedia()', runtime.indexOf('private async runStop'))
  const fallbackIdx = runtime.indexOf('transcribeFull(', runtime.indexOf('private async runStop'))
  assert.ok(cleanupIdx > 0 && fallbackIdx > 0)
  assert.ok(cleanupIdx < fallbackIdx, '调用顺序前提变了，需重新确认 decoder 生命周期')
  const body = runtime.slice(runtime.indexOf('private cleanupMedia()'),
    runtime.indexOf('private cleanupMedia()') + 400)
  assert.doesNotMatch(body, /audioDecoder[\s\S]{0,80}dispose/, 'cleanupMedia 里不能 dispose decoder')
  assert.match(runtime, /this\.audioDecoder\?\.dispose\(\)\s*\n\s*this\.audioDecoder = new RollingWebmDecoder\(\)/,
    '必须在 start() 里释放上一场实例')
})
