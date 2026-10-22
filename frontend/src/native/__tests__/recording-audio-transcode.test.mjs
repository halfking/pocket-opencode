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
 *
 * ── §953：解码从「累计」改成「逐簇」之后，本文件换了三样东西 ──────────────
 *
 * ① **夹具从「任意字节」换成「结构正确的 webm」**：
 *    旧夹具是 `new Blob([new Uint8Array(8)])`（8 个零字节）——
 *    它在旧的累计解码下够用（解码器是假的，不看字节），
 *    但增量解码要**按 Cluster 切**，零字节里根本没有 Cluster ⇒ 必须换成
 *    真结构的合成 webm（见下方 `makeWebm`）。
 *    ⚠ 合成的是**结构**（EBML 头 / Segment / Info / Tracks / Cluster），
 *      载荷是填充字节 —— 真容器的载荷是 opus 帧，本仓不造那个。
 *
 * ② **假解码器的语义从「累计」改成「逐簇」**：
 *    旧假解码器按「调用次数」返回**越来越长**的音频，那正是**旧实现**的形状；
 *    新实现每次只解一簇 ⇒ 假解码器必须返回**一簇**的音频。
 *    ⇒ 假解码器同时**计量喂给它的字节数** ⇒ 于是「O(时长²) 还是 O(时长)」
 *      这件事从「源码里读出来的承诺」变成了**可测量的行为**。
 *
 * ③ **契约门换了指向**：旧门钉的是「必须把全部分片拼起来」（workaround 的形态），
 *    新门钉的是「**解码路径不得**再拼整场容器」+「必须用初始化前缀逐簇解」。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  pcmToWavBytes, sliceToWavBytes, arrayBufferToBase64, RollingWebmDecoder,
} from '../recording-audio-transcode.ts'
import {
  extractWebmInitPrefix, findWebmClusterOffsets, readWebmClusterTimecode,
  composeWebmWithInit, WebmClusterSplitter,
} from '../../utils/webm-incremental.ts'
import { encodeWav16kMono } from '../../utils/wav-encode.ts'

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
// §953：合成一个**结构正确**的 webm（EBML 头 / Segment / Info / Tracks / Cluster）
// ---------------------------------------------------------------------------

const ID_EBML = [0x1a, 0x45, 0xdf, 0xa3]
const ID_SEGMENT = [0x18, 0x53, 0x80, 0x67]
const ID_INFO = [0x15, 0x49, 0xa9, 0x66]
const ID_TRACKS = [0x16, 0x54, 0xae, 0x6b]
const ID_CLUSTER = [0x1f, 0x43, 0xb6, 0x75]
const ID_TIMECODE = [0xe7]
/** unknown size：1 字节 0xFF（Cluster 用）/ 8 字节（Segment 用）。 */
const UNKNOWN_1 = [0xff]
const UNKNOWN_8 = [0x01, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff]

function element(id, content) {
  assert.ok(content.length < 127, '本夹具只支持 1 字节长度')
  // ⚠ EBML 的 size 是**变长整数**：长度写在**前导位**里（1 字节长度 = 0x80 | len）。
  //   写成裸字节（0x07）会被读成「长度 7、但占 0 字节」⇒ 整棵树解析错位。
  return [...id, 0x80 | content.length, ...content]
}

/** 一段 Cluster：ID + unknown size + Timecode + 载荷。 */
function cluster(timecodeMs, payloadLen, fill = 0x5a) {
  // ⚠ Timecode 的值用**最少字节**写（EBML 允许 1..8 字节，size 说的是字节数）。
  //   固定写 2 字节 ⇒ 超过 65535 就会**回绕** ⇒ 时间戳不再单调 ⇒
  //   生产代码的「Timecode 必须严格递增」校验会正确地拒掉它（夹具的锅，不是实现的锅）。
  const tc = []
  let v = timecodeMs
  do { tc.unshift(v & 0xff); v = Math.floor(v / 256) } while (v > 0)
  return [...ID_CLUSTER, ...UNKNOWN_1, ...ID_TIMECODE, 0x80 | tc.length, ...tc,
    ...new Array(payloadLen).fill(fill)]
}

/**
 * 造一个结构正确的 webm。
 * @param specs 每段簇的 { tc: Timecode(ms), len: 载荷字节数 }
 */
function makeWebm(specs) {
  const headerBytes = element(ID_EBML, [0x42, 0x82, 0x84, 0x77, 0x65, 0x62, 0x6d])  // DocType "webm"
  const segmentHeader = [...ID_SEGMENT, ...UNKNOWN_8]
  const info = element(ID_INFO, [0x2a, 0xd7, 0xb1, 0x0f, 0x42, 0x40, 0x20, 0x40, 0x20, 0x40])
  const tracks = element(ID_TRACKS, [0x01, 0x02, 0x03, 0x04])
  const clusters = specs.map((s) => cluster(s.tc, s.len))
  return {
    bytes: Uint8Array.from([...headerBytes, ...segmentHeader, ...info, ...tracks, ...clusters.flat()]),
    /** 初始化前缀的长度（= 第一个 Cluster 的起点）——独立算，不依赖生产实现。 */
    initLength: headerBytes.length + segmentHeader.length + info.length + tracks.length,
  }
}

test('§953 extractWebmInitPrefix：前缀正好停在第一个 Cluster 之前', () => {
  const w = makeWebm([{ tc: 0, len: 100 }, { tc: 4020, len: 100 }])
  const init = extractWebmInitPrefix(w.bytes)
  assert.ok(init, '必须认出这是 webm')
  assert.equal(init.length, w.initLength, '前缀长度 = 第一个 Cluster 的偏移')
  // 前缀里必须含 EBML 头与 Segment 头，且**不含** Cluster ID。
  assert.deepEqual([...init.slice(0, 4)], ID_EBML, '前缀应以 EBML 头开始')
  assert.ok(!init.some((b, i) => b === 0x1f && init[i + 1] === 0x43 && init[i + 2] === 0xb6 && init[i + 3] === 0x75),
    '前缀里不该出现 Cluster ID')
  // 从前缀末尾接上第一段簇 ⇒ 合成体必须以 Cluster ID 开头。
  const composed = composeWebmWithInit(init, w.bytes.slice(w.initLength))
  assert.deepEqual([...composed.slice(init.length, init.length + 4)], ID_CLUSTER)
})

test('§953 extractWebmInitPrefix：不是 webm 时返回 null（不猜）', () => {
  assert.equal(extractWebmInitPrefix(new Uint8Array(0)), null)
  assert.equal(extractWebmInitPrefix(new Uint8Array([1, 2, 3, 4, 5, 6, 7, 8])), null)
  // EBML 头对了但后面没有 Segment ⇒ 仍然不认。
  const fake = Uint8Array.from([...ID_EBML, 0x02, 0x00, 0x00])
  assert.equal(extractWebmInitPrefix(fake), null)
})

test('§953 findWebmClusterOffsets：找出全部簇起点并读出 Timecode', () => {
  const w = makeWebm([{ tc: 0, len: 100 }, { tc: 4020, len: 120 }, { tc: 8040, len: 140 }])
  const offsets = findWebmClusterOffsets(w.bytes, 0, -1)
  assert.equal(offsets.length, 3, '三段簇')
  assert.equal(offsets[0], w.initLength, '第一段簇紧跟初始化前缀')
  assert.deepEqual(offsets.map((o) => readWebmClusterTimecode(w.bytes, o)), [0, 4020, 8040])
})

test('§953 findWebmClusterOffsets：载荷里的假 Cluster ID 不得被当成簇起点', () => {
  // 在第一段簇的载荷里塞一个**没有合法 Timecode** 的 Cluster ID。
  // ⇒ 这是 opus 载荷偶然撞上 4 字节 ID 的真实形态 ⇒ 必须被 Timecode 校验挡掉。
  const w = makeWebm([{ tc: 0, len: 64 }, { tc: 4020, len: 64 }])
  const withFake = Uint8Array.from(w.bytes)
  const fakeAt = w.initLength + 10
  withFake.set(ID_CLUSTER, fakeAt)
  const offsets = findWebmClusterOffsets(withFake, 0, -1)
  assert.equal(offsets.length, 2, '假 ID 不该被算成簇起点（真簇只有 2 段）')
  assert.ok(!offsets.includes(fakeAt), `假 Cluster 起点 ${fakeAt} 被认成了真簇`)
})

test('§953 WebmClusterSplitter：逐片推进，末尾那簇不完整时留在缓冲里', () => {
  const w = makeWebm([{ tc: 0, len: 100 }, { tc: 4020, len: 120 }, { tc: 8040, len: 140 }])
  const sp = new WebmClusterSplitter()
  // 第一片：只有 init + 第一段簇的一半 ⇒ 取不到任何完整簇。
  sp.push(w.bytes.slice(0, w.initLength + 50))
  assert.equal(sp.takeCompleteClusters().length, 0, '第一片还不该有完整簇')
  assert.ok(sp.initPrefix, '初始化前缀必须已扫到')
  // 补齐第一段簇 + 第二段簇的一大半 ⇒ 第一段簇完整了。
  sp.push(w.bytes.slice(w.initLength + 50, w.initLength + 100 + 60))
  const got = sp.takeCompleteClusters()
  assert.equal(got.length, 1, '应当只拿到第一段簇（第二段还没结束）')
  assert.equal(got[0].timecodeMs, 0)
  // 收尾：缓冲里还压着第二、第三段簇 ⇒ 收尾必须**全部**取走。
  // ⚠ 只取第一段会把中间的音频丢掉（那正是这一版实现被单测抓出来的真缺陷）。
  sp.push(w.bytes.slice(w.initLength + 100 + 60))
  const tail = sp.flush()
  assert.equal(tail.length, 2, '收尾必须把缓冲里剩下的两段簇都取走（只取第一段 ⇒ 丢音频）')
  assert.deepEqual(tail.map((c) => c.timecodeMs), [4020, 8040])
})

test('§953 WebmClusterSplitter：缓冲不随录音时长增长（只留最后一段簇）', () => {
  // 100 段簇，每段 4 KB ⇒ 整场 400 KB。
  const specs = Array.from({ length: 100 }, (_, i) => ({ tc: i * 4020, len: 4096 }))
  const w = makeWebm(specs)
  const sp = new WebmClusterSplitter()
  let maxBuffered = 0
  // 每 8 KB 推一次（模拟 MediaRecorder 分片）。
  for (let off = 0; off < w.bytes.length; off += 8192) {
    sp.push(w.bytes.slice(off, off + 8192))
    sp.takeCompleteClusters()
    maxBuffered = Math.max(maxBuffered, sp.bufferedBytes)
  }
  assert.ok(maxBuffered < 64 * 1024,
    `缓冲必须稳定在 O(簇) 而不是 O(整场)：实测最大 ${maxBuffered} 字节（整场 ${w.bytes.length}）`)
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

/**
 * §953 的假解码器：**逐簇**语义 + 计量喂给它的字节数。
 * @param secPerCluster 每一簇解出来的秒数
 */
function makeFakeDecoder(secPerCluster = 3, sampleRate = SR) {
  const stats = { calls: 0, bytesFed: 0, payloads: [] }
  const decode = async (arrayBuffer) => {
    const bytes = new Uint8Array(arrayBuffer)
    stats.calls++
    stats.bytesFed += bytes.length
    stats.payloads.push(bytes)
    return fake(secPerCluster, sampleRate)
  }
  return { decode, stats }
}

/**
 * §953：把 webm 按**簇边界**切成几片（模拟 MediaRecorder 逐片到达）。
 * @returns 每片一个 Blob（第一片带初始化前缀）
 */
function splitWebmByCluster(w) {
  const offsets = findWebmClusterOffsets(w.bytes, 0, -1)
  assert.ok(offsets.length >= 1, '夹具里至少要有一段簇')
  const out = []
  for (let i = 0; i < offsets.length; i++) {
    const end = i + 1 < offsets.length ? offsets[i + 1] : w.bytes.length
    // ⚠ 第一片必须**带上初始化前缀** —— 真实 webm 的 init 段就在第一片里，
    //   少了它切分器认不出这是 webm（这正是本测试第一次跑就红的原因）。
    const start = i === 0 ? 0 : offsets[i]
    out.push(new Blob([w.bytes.slice(start, end)]))
  }
  return out
}

test('RollingWebmDecoder：无分片时返回 null（不发空请求）', async () => {
  const d = new RollingWebmDecoder(async () => fake(3))
  assert.equal(await d.takeNewWindow(), null)
  d.dispose()
})

test('RollingWebmDecoder：连续取用产出互不重叠的新增窗口', async () => {
  // §953：夹具是**结构正确的 webm**，否则切不出簇。
  const w = makeWebm([{ tc: 0, len: 64 }, { tc: 4020, len: 64 }, { tc: 8040, len: 64 }])
  const { decode } = makeFakeDecoder(3)
  const d = new RollingWebmDecoder(decode)
  // ⭐ 一片一片地推、推一片取一次 ⇒ 验的正是「增量」这件事。
  const ws_ = []
  for (const part of splitWebmByCluster(w)) {
    d.push(part)
    ws_.push(await d.takeNewWindow())
  }
  // ⚠ 最后一段簇永远不完整（收尾前拿不到）⇒ 第三次只有前两段。
  assert.equal(ws_[0], null, '第一片只有一段簇 ⇒ 还没有完整簇')
  assert.ok(ws_[1], '第二片之后应当有一段完整簇')
  assert.ok(ws_[2], '第三片之后应当又有一段完整簇')
  for (const w2 of ws_.slice(1)) assert.equal(header(w2).dataSize, 3 * TARGET_SR * 2, '每段簇 3 秒')
  d.dispose()
})

test('RollingWebmDecoder：窗口只含新增内容（不重发已发送部分）', async () => {
  const w = makeWebm([{ tc: 0, len: 64 }, { tc: 4020, len: 64 }, { tc: 8040, len: 64 }])
  const { decode } = makeFakeDecoder(3)
  const d = new RollingWebmDecoder(decode)
  const parts = splitWebmByCluster(w)
  d.push(parts[0])
  d.push(parts[1])
  const w1 = await d.takeNewWindow()
  d.push(parts[2])
  const w2 = await d.takeNewWindow()
  assert.equal(header(w1).dataSize, 3 * TARGET_SR * 2, '第一段簇 3 秒')
  assert.equal(header(w2).dataSize, 3 * TARGET_SR * 2,
    '第二段必须只含新增的 3 秒 —— 重发累计会得到 6 秒 / 192000 字节')
  d.dispose()
})

test('★ §953：喂给解码器的字节是 O(整场)，不是 O(整场²)', async () => {
  // 12 段簇、每段 4 KB ⇒ 整场约 50 KB。
  // ⭐ 必须**一片一片推、推一片取一次** —— 一次性推完再取一次，
  //   「解一次整场」和「逐簇解」喂进去的字节数是同一量级 ⇒ 这条判据会没有牙。
  //   一片一片推时：逐簇解 ⇒ 总量 ≈ 1× 整场；每次解整场 ⇒ 总量 ≈ 6× 整场。
  const specs = Array.from({ length: 12 }, (_, i) => ({ tc: i * 4020, len: 4096 }))
  const w = makeWebm(specs)
  const { decode, stats } = makeFakeDecoder(3)
  const d = new RollingWebmDecoder(decode)
  for (const part of splitWebmByCluster(w)) {
    d.push(part)
    await d.takeNewWindow()
  }
  assert.ok(stats.calls >= 1, '至少解过一簇')
  assert.ok(stats.bytesFed <= w.bytes.length * 2,
    `喂给解码器的字节必须是 O(整场)：实测 ${stats.bytesFed}，整场 ${w.bytes.length}` +
    '（累计解码会喂进前缀和 ⇒ 约 6 倍 ⇒ 本条会红）')
  d.dispose()
})

test('★ §953：每次解码都必须带上初始化前缀（否则单簇解不开）', async () => {
  const w = makeWebm([{ tc: 0, len: 64 }, { tc: 4020, len: 64 }])
  const { decode, stats } = makeFakeDecoder(3)
  const d = new RollingWebmDecoder(decode)
  d.push(new Blob([w.bytes]))
  await d.takeNewWindow()
  assert.ok(stats.payloads.length >= 1, '至少解过一簇')
  for (const p of stats.payloads) {
    assert.deepEqual([...p.slice(0, 4)], ID_EBML,
      '每次解码的载荷都必须以 EBML 头开始（即带上了初始化前缀）')
  }
  d.dispose()
})

test('RollingWebmDecoder：没有新内容时返回 null 而不是空 WAV', async () => {
  // 0 长度音频会让上游回 502 empty transcript，界面上凭空多一条错误。
  const w = makeWebm([{ tc: 0, len: 64 }])
  const { decode } = makeFakeDecoder(0.05)
  const d = new RollingWebmDecoder(decode)
  d.push(new Blob([w.bytes]))
  // 末尾那簇不完整 ⇒ 本来就没有可发的内容。
  assert.equal(await d.takeNewWindow(0.15), null)
  d.dispose()
})

test('§953：不足 minWindowSec 的尾巴**不许被丢掉**（旧实现会丢）', async () => {
  // 旧实现：凑不够 minWindowSec ⇒ 把游标推到末尾并返回 null ⇒ **尾巴被丢掉**。
  // §953 改成：尾巴留在 pending 里，下一次一起发出去。
  // ⚠ 需要**三段**簇：末尾那簇永远不完整 ⇒ 只有两段能被解出来。
  const w = makeWebm([{ tc: 0, len: 64 }, { tc: 4020, len: 64 }, { tc: 8040, len: 64 }])
  const { decode } = makeFakeDecoder(0.1)           // 每簇 100 ms < 150 ms
  const d = new RollingWebmDecoder(decode)
  const parts = splitWebmByCluster(w)
  d.push(parts[0])
  d.push(parts[1])
  assert.equal(await d.takeNewWindow(0.15), null, '只有一段簇完整（100 ms）⇒ 本轮不发')
  d.push(parts[2])
  const w2 = await d.takeNewWindow(0.15)
  assert.ok(w2, '★ 尾巴被丢掉了 —— 累计 200 ms 应当一次发出去')
  // 200 ms @16k = 3200 点 = 6400 字节（丢掉一段就只剩 3200 字节）
  assert.equal(header(w2).dataSize, 6400, '两段簇的尾巴必须都发出去（丢掉一段就只剩 3200 字节）')
  d.dispose()
})

test('RollingWebmDecoder：解码抛错时向上抛（由调用方降级）', async () => {
  const w = makeWebm([{ tc: 0, len: 64 }, { tc: 4020, len: 64 }])
  const d = new RollingWebmDecoder(async () => { throw new Error('UnsupportedError') })
  d.push(new Blob([w.bytes]))
  await assert.rejects(() => d.takeNewWindow())
  d.dispose()
})

/**
 * §571：上面那条只钉了「解码抛错会向上抛」，
 * **没钉 `recordingRuntime.ts:1052` 那句承诺的后一半** ——
 *   「下一片再试（累计音频还在，**解码器游标也没推进，不会丢内容**）」。
 *
 * ⚠ 这是**跨两个模块**的承诺：游标在 `recording-audio-transcode.ts`，
 *   降级与「跳过这片」在 `recordingRuntime.transcodeSlice`。
 *   ⇒ 静态断言抓不住，只能**用行为钉**：解码失败之后，decode 恢复正常时，
 *   下一次调用必须**拿得回同一段**。若游标在失败时被推进过，那段就永久丢了。
 */
test('★ §571：解码失败后游标未推进 ⇒ 下一次能拿回同一段（兑现「不会丢内容」）', async () => {
  const w = makeWebm([{ tc: 0, len: 64 }, { tc: 4020, len: 64 }])
  let failNext = true
  const d = new RollingWebmDecoder(async () => {
    if (failNext) { failNext = false; throw new Error('UnsupportedError') }
    return fake(3)
  })
  d.push(new Blob([w.bytes]))

  await assert.rejects(() => d.takeNewWindow(), '前提不成立：第一次调用应当抛错')
  const again = await d.takeNewWindow()

  assert.ok(again, '★ 游标在解码失败时被推进了 ⇒ 那一段音频永久丢失，' +
    '而 `recordingRuntime.ts:1052` 的「不会丢内容」是假的')
  assert.ok(again.byteLength > 0, '拿回的是空 WAV ⇒ 同样等于丢内容')
  d.dispose()
})

test('RollingWebmDecoder：dispose 后不再接受分片', async () => {
  const d = new RollingWebmDecoder(async () => fake(3))
  d.dispose()
  d.push(new Blob([new Uint8Array(10)]))
  assert.equal(await d.takeNewWindow(), null)
})

test('RollingWebmDecoder：takeFull 拿整段且不消耗游标', async () => {
  const w = makeWebm([{ tc: 0, len: 64 }, { tc: 4020, len: 64 }])
  const { decode } = makeFakeDecoder(3)
  const d = new RollingWebmDecoder(decode)
  d.push(new Blob([w.bytes]))
  const inc = await d.takeNewWindow()
  const full = await d.takeFull()
  assert.equal(header(inc).dataSize, 3 * TARGET_SR * 2)
  assert.equal(header(full).dataSize, 3 * TARGET_SR * 2,
    'takeFull 走的是「整场容器」那条路（§953 登记：它必须给出整段 ⇒ 天然 O(时长)）')
  d.dispose()
})

/**
 * §953：给 `encodeWav16kMono` 装一个**假 AudioContext**，
 * 于是可以在 node 里真跑「会议收尾那条转码路」，并**数它解了几次**。
 *
 * ⭐ 这条判据的价值在于它量的是**行为**而不是源码形状：
 *   整场解码 ⇒ 解 1 次、峰值 659 MB/小时；
 *   逐簇解码 ⇒ 解 N 次、峰值 ≈ 230 MB/小时。
 */
function withFakeAudioContext(run) {
  const calls = []
  const g = globalThis
  const prev = g.window
  g.window = {
    AudioContext: class {
      async decodeAudioData(ab) {
        calls.push(ab.byteLength)
        return {
          length: 192000, numberOfChannels: 1, sampleRate: SR,
          getChannelData: () => new Float32Array(192000),
        }
      }
      close() { /* 真实现是 void ctx.close() */ }
    },
  }
  return (async () => {
    try { return await run(calls) } finally { g.window = prev }
  })()
}

test('★ §953：会议收尾的整场转码改为**逐簇**解码（峰值 659 → 230 MB/小时）', async () => {
  const w = makeWebm([{ tc: 0, len: 64 }, { tc: 4020, len: 64 }, { tc: 8040, len: 64 }])
  const blob = new Blob([w.bytes], { type: 'audio/webm' })
  await withFakeAudioContext(async (calls) => {
    const out = await encodeWav16kMono(blob)
    assert.ok(out.size > 0, '必须产出 WAV')
    assert.equal(calls.length, 3,
      `必须逐簇解码（3 段簇 ⇒ 解 3 次）—— 退回整场解码会得到 1 次（峰值 659 MB/小时）`)
    assert.ok(calls.every((n) => n < w.bytes.length),
      '每次解码的输入都必须只是「一簇 + 初始化前缀」，不能是整场')
  })
})

test('★ §953：不是 webm 时退回整场解码（失败语义不变，不比现状差）', async () => {
  // 一个完全不是 webm 的容器（随机字节）⇒ 逐簇那条路走不通 ⇒ 必须退回整场。
  const junk = new Blob([new Uint8Array([1, 2, 3, 4, 5, 6, 7, 8])], { type: 'audio/webm' })
  await withFakeAudioContext(async (calls) => {
    const out = await encodeWav16kMono(junk)
    assert.ok(out.size > 0, '必须仍然产出 WAV（退回整场解码）')
    assert.equal(calls.length, 1, '退回整场解码 ⇒ 解 1 次')
  })
})

// ---------------------------------------------------------------------------
// 源码级契约（浏览器专属行为 / 生命周期顺序，无法在 Node 里执行）
// ---------------------------------------------------------------------------
const src = readFileSync(SRC, 'utf8')
const runtime = readFileSync(RUNTIME, 'utf8')

test('契约：§953 解码路径**不得**再把全部分片拼成整场容器', () => {
  // 旧门钉的是「必须拼全部分片」——那钉的是 **workaround 的形态**，
  // 而 §953 的实测证明那个 workaround 的前提（「只有全量拼接才解得开」）是**错的**：
  // 「初始化前缀 + 单个 Cluster」就能单独解（实测逐样本等价）。
  // ⇒ 门改成钉**目标**（解码路径不得再拼整场容器），并留负控（见变异批次）。
  const start = src.indexOf('async takeNewWindow')
  const end = src.indexOf('/** 把还没喂过的分片读成字节')
  assert.ok(start > 0 && end > start, `锚点坏了（start=${start} end=${end}）`)
  const body = src.slice(start, end)
  assert.doesNotMatch(body, /new Blob\(\s*this\.parts/,
    'takeNewWindow 不得再把全部分片拼成整场容器（那是 O(时长²) 的来源）')
  // 全文件：这种写法只允许出现在 takeFull / fullBlob 两处。
  assert.equal((src.match(/new Blob\(\s*this\.parts/g) || []).length, 2,
    '整场拼接只允许出现在 takeFull / fullBlob 两处')
  // 增量路径必须真的用上初始化前缀。
  assert.match(src, /composeWebmWithInit/, '增量解码必须走「初始化前缀 + 簇」')
  assert.match(src, /WebmClusterSplitter/, '增量解码必须按 Cluster 切')
  // ⛔ 旧的负向断言（「不能用最后一片单独解码」）作废：
  //   §953 实测「初始化前缀 + 最后一片」是**能**解开的。
  //   保留它就是把 workaround 当成需求。
  assert.doesNotMatch(src, /this\.parts\[this\.parts\.length - 1\]/,
    '不能用最后一片单独解码（这条在 §953 之后已作废：init 前缀 + 任何一簇都能解）')
})

test('契约：base64 分块转换（一次性展开会爆栈，12 秒 PCM 实测 RangeError）', () => {
  // ⚠⚠ §889：这条判据原来读的是 `recording-audio-transcode.ts` —— 那份
  //   `arrayBufferToBase64` 在 §889 被**收编到 `utils/base64.ts`** 了
  //   （本仓「bytes → base64」曾有 4 份同名不同名的实现）。
  //   ⇒ 实现搬走之后，这里读原文件只会读到一行 re-export，**必然假红**。
  //
  //   本节只改**判据的指向**，不改它的语义：「分块 + apply + 不一次性展开」
  //   三条要求原封不动，只是让它们检查实现的新家。
  //   同时补一条：原文件必须仍以 re-export 暴露它 —— 否则本文件顶部的
  //   `import { arrayBufferToBase64 } from '../recording-audio-transcode.ts'`
  //   会加载不到（那是 §881 判据与生产 `recordingRuntime.ts` 的共同入口）。
  const impl = readFileSync(resolve(HERE, '../../utils/base64.ts'), 'utf8')
  assert.match(impl, /CHUNK = 32768/,
    'base64 转换没有分块常量 CHUNK = 32768（0x8000 会被这条既有判据判红，故用十进制）')
  assert.match(impl, /String\.fromCharCode\.apply/, '必须分块 apply')
  assert.doesNotMatch(impl, /String\.fromCharCode\(\.\.\.bytes\)/, '不能一次性展开')
  assert.match(src, /export \{ arrayBufferToBase64 \} from '\.\.\/utils\/base64\.ts'/,
    'recording-audio-transcode.ts 必须仍以 re-export 暴露 arrayBufferToBase64（带 .ts 扩展名，§875）')
})

test('契约：模块不含 TS 参数属性（否则单测无法 import 生产代码）', () => {
  // 这条是本文件头那条返工教训的护栏：初版用 `constructor(private readonly x)`
  // 导致 node strip-only 报错，于是测试只好重抄实现 —— 那等于没测。
  //
  // ⚠ 必须**先剥注释**再匹配：模块头注释里就写着 `constructor(private readonly x)`
  // 这个反例原文，不剥的话本条恒红（第一次跑就踩了这个）。
  const code = src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
  assert.doesNotMatch(code, /constructor\s*\([^)]*\b(private|public|protected|readonly)\b/,
    '构造器里不得有参数属性，node --experimental-strip-types 不支持')
  // 同时保证纯函数确实被本测试 import 了（不是本地副本）。
  //
  // ⚠ 两个踩过的坑：
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

/**
 * §569：上面那条门**只验了会议侧那一个 `cleanupMedia`**。
 *
 * ⚠ 根因是 `indexOf('private cleanupMedia()')` —— 它匹配的是
 *   `recordingRuntime.ts:1350` 的**会议**侧定义；
 *   而**随手记侧**那个签名不同（`:215` 是 `private async cleanupMedia()`），
 *   `indexOf` **匹配不到** ⇒ 那一份完全没被验。
 *
 * ⭐ 而那句承诺恰恰写在**随手记侧**（`recordingRuntime.ts:957-958`：
 *   「旧解码器在上一场 stop() 的兜底全量转写里还会用到，所以不能在 cleanupMedia() 里 dispose」）。
 *
 * ⛔ 变异 M24 实测：往**随手记侧**的 `cleanupMedia` 里加一行
 *   `this.audioDecoder?.dispose()` ⇒ **这条门纹丝不动，全量 test:all 也全绿**。
 *
 * ⚠ 破坏后的后果是**静默**的：`RollingWebmDecoder.takeFull()`
 *   在 disposed 时 `return null`（不抛错），而随手记 `runStop` 写的是
 *   `payload = await this.audioDecoder?.takeFull() ?? null`
 *   ⇒ payload 为 null ⇒ **分片一条都没回来时，本该兜底的全量转写拿不到音频**
 *   ⇒ 而 `?? null` 让流程继续、不报错 ⇒ **最后一段录音静默丢失**。
 *
 * ⇒ 这正是这条注释要防的那件事。所以门必须**两份同形实现都验**。
 */
test('契约（§569）：**每一个** cleanupMedia 都不得 dispose 解码器（含随手记侧）', () => {
  // 两种签名都算：private cleanupMedia() / private async cleanupMedia()
  const defs = [...runtime.matchAll(/private (?:async )?cleanupMedia\(\)\s*\{/g)]
  assert.ok(defs.length >= 2,
    `只找到 ${defs.length} 个 cleanupMedia 定义（预期 ≥2）⇒ 改签名会让这条门退化成空跑`)

  const risky = /audioDecoder[\s\S]{0,120}?dispose|dispose[\s\S]{0,120}?audioDecoder/
  for (const d of defs) {
    // 从 `{` 起做花括号配平，避免固定窗口不够长而漏读
    let depth = 0, end = -1
    for (let i = d.index + d[0].length - 1; i < runtime.length; i++) {
      if (runtime[i] === '{') depth++
      else if (runtime[i] === '}') { depth--; if (depth === 0) { end = i; break } }
    }
    assert.ok(end > 0, 'cleanupMedia 的花括号配平失败，判据读数不可信')
    const body = runtime.slice(d.index, end + 1)
    assert.ok(!risky.test(body),
      '★ 门有洞：某一个 cleanupMedia 里 dispose 了 audioDecoder ⇒ ' +
      'cleanupMedia 早于兜底转写，会让「分片全丢时的兜底全量转写」静默拿不到音频')
  }
})
