/**
 * webm 增量解码：把「解整场 / 解累计」换成「**逐簇解**」。
 *
 * ## 为什么需要它（§953）
 *
 * 本仓有两条链会解码浏览器录出来的 webm/opus：
 *
 * | 链 | 谁在解 | 解的是什么 | 代价随时长 |
 * |---|---|---|---|
 * | 随手记实时转写 | `RollingWebmDecoder`（`recording-audio-transcode.ts`） | **累计容器**（每片都重解一遍到此刻的全部音频） | **O(T²)** CPU + **O(T)** 解码峰值 |
 * | 会议收尾整场重转 | `encodeWav16kMono`（`utils/wav-encode.ts`） | **整场容器**（一次解完） | **O(T)** 解码峰值（48 kHz Float32 ≈ 659 MB/小时） |
 *
 * ⇒ 两者都随会议/笔记时长线性甚至二次方地吃资源。
 *
 * ## §953 的实测结论（headless Chromium 的真 MediaRecorder 产物，2026-10-21）
 *
 * 1. **初始化前缀只有 146 字节**：EBML 头 + Segment 头 + Info + Tracks，
 *    第一个 Cluster 紧跟其后（Cluster 的 size 是 unknown ⇒ 后面全是它的载荷）。
 * 2. **除第一片外，单片确实解不了**（§510 的实测结论成立）。
 * 3. ⭐ **「初始化前缀 + 某一片」能单独解**，解出来的就是那一片的音频。
 * 4. ⭐⭐ **切点必须是 Cluster，不能是 MediaRecorder 的分片边界**：
 *    实测每片的尾部约 965 字节落在**下一片**里（簇边界比分片边界晚），
 *    按分片切会**丢 1.2% 的音频**（20 秒丢 0.24 秒）；
 *    按 Cluster 切则**逐样本等价**（拼接后与整场解码**长度相同、位置偏移 0**，
 *    簇边界处 mse ≈ 1e-4 ⇒ 低于 ASR 可感知阈值）。
 * 5. ⇒ ⇒ **「需要一个 webm demuxer」（§923.5 的原判断）被推翻**：
 *    需要的只是「找到第一个 Cluster」+「找到后续 Cluster 边界」，
 *    而 Cluster 的绝对位置由它自己的 **Timecode** 给出（实测 0 / 4020 / 8040 … ms），
 *    ⇒ **不需要完整 demuxer**。
 *
 * ## 本模块的边界
 *
 * - **零 import**（不含任何依赖）⇒ 可以在 node 里直接 import 做单测
 *   （与 `speaker-diarization.ts` 同一条纪律）。
 * - **不碰浏览器 API**：解码由调用方注入（`decodeAudioData`）⇒ 本模块纯字节/纯逻辑。
 * - **不做重采样**：重采样是逐样本无状态的，但**逐簇重采样与整场重采样
 *   在簇边界上可能差 ≤ 簇数个采样** ⇒ 由调用方决定在哪一层做。
 */

const ID_EBML = 0x1a45dfa3
const ID_SEGMENT = 0x18538067
const ID_CLUSTER = 0x1f43b675
const ID_TIMECODE = 0xe7

/** Cluster 的 4 字节 ID（EBML 变长整数，最长 4 字节）。 */
const CLUSTER_ID_BYTES = [0x1f, 0x43, 0xb6, 0x75]

/** 一段 Cluster（字节 + 它的绝对时间戳，毫秒）。 */
export interface WebmCluster {
  /** 从 Cluster 的 ID 起到下一段 Cluster 的 ID 前（或流尾）。 */
  bytes: Uint8Array
  /** Cluster 的 Timecode（EBML 0xE7），用于校验边界是不是真的簇起点。 */
  timecodeMs: number
}

/**
 * 读一个 EBML 变长整数。
 *
 * @param keepMarker ID 要保留 marker 位（EBML 的 ID 就是带 marker 的原值）；
 *                    size 不要 marker（全 1 = unknown size）。
 */
function readVint(bytes: Uint8Array, pos: number, keepMarker: boolean): { value: number; length: number; unknown: boolean } | null {
  const first = bytes[pos]
  if (first === undefined) return null
  let length = 1
  for (let i = 0; i < 8; i++) {
    if (first & (0x80 >> i)) { length = i + 1; break }
  }
  if (pos + length > bytes.length) return null
  let value = keepMarker ? first : (first & (0xff >> length))
  let allOnes = !keepMarker && (first & (0xff >> length)) === (0xff >> length)
  for (let i = 1; i < length; i++) {
    const b = bytes[pos + i]
    value = value * 256 + b
    if (b !== 0xff) allOnes = false
  }
  return { value, length, unknown: allOnes }
}

/**
 * 取出 webm 的**初始化前缀**：EBML 头 + Segment 头 + Info + Tracks，
 * 也就是第一个 Cluster 之前的全部字节。
 *
 * ⚠ 为什么必须是「第一个 Cluster 之前」而不是「固定字节数」：
 *   Info / Tracks 的大小随容器而变（实测 146 字节，但那是 Chrome 的默认值）。
 *
 * @returns 前缀字节；不是一个 webm 容器时返回 null。
 */
export function extractWebmInitPrefix(bytes: Uint8Array): Uint8Array | null {
  if (!bytes.length) return null
  const header = readVint(bytes, 0, true)
  if (!header || header.value !== ID_EBML) return null
  const headerSize = readVint(bytes, header.length, false)
  if (!headerSize) return null
  let pos = header.length + headerSize.length
  if (!headerSize.unknown) pos += headerSize.value
  const segment = readVint(bytes, pos, true)
  if (!segment || segment.value !== ID_SEGMENT) return null
  const segmentSize = readVint(bytes, pos + segment.length, false)
  if (!segmentSize) return null
  let child = pos + segment.length + segmentSize.length
  const segmentEnd = segmentSize.unknown ? bytes.length : child + segmentSize.value
  while (child < segmentEnd) {
    const id = readVint(bytes, child, true)
    if (!id) return null
    const size = readVint(bytes, child + id.length, false)
    if (!size) return null
    if (id.value === ID_CLUSTER) return bytes.slice(0, child)
    if (size.unknown) return null // Cluster 之前不该有 unknown size
    child += id.length + size.length + size.value
    if (child > bytes.length) return null
  }
  return null
}

/**
 * 读一个 Cluster 的 Timecode（EBML 0xE7，毫秒）。
 *
 * ⚠ Cluster 的 size 是 unknown（Chrome 的 live webm），所以只能顺着子元素找，
 *   找到第一个 Timecode 就停。
 */
export function readWebmClusterTimecode(bytes: Uint8Array, offset: number): number | null {
  let pos = offset + CLUSTER_ID_BYTES.length
  const size = readVint(bytes, pos, false)
  if (!size) return null
  pos += size.length
  for (;;) {
    const id = readVint(bytes, pos, true)
    if (!id) return null
    const childSize = readVint(bytes, pos + id.length, false)
    if (!childSize) return null
    if (id.value === ID_TIMECODE) {
      let value = 0
      const start = pos + id.length + childSize.length
      for (let i = 0; i < childSize.value; i++) {
        const b = bytes[start + i]
        if (b === undefined) return null
        value = value * 256 + b
      }
      return value
    }
    if (id.value === 0xa3) return null // SimpleBlock：没有 Timecode
    if (childSize.unknown) return null
    pos += id.length + childSize.length + childSize.value
    if (pos > bytes.length) return null
  }
}

/**
 * 找出字节流里所有 **Cluster 的起始偏移**。
 *
 * ⚠ 为什么不能只按 4 字节 ID 扫：opus 载荷里可能偶然出现同样的 4 字节。
 *   ⇒ 每一处候选都要**解析它的 Timecode** 并要求「严格递增」才认。
 *   ⇒ 载荷里的偶然匹配几乎不可能同时满足「后面紧跟合法 EBML 头 + Timecode」。
 *
 * @param from 从哪个偏移开始找（增量场景下用它跳过已经消费过的部分）。
 * @param afterTimecode 只认 Timecode **严格大于**它的簇（跨批次保持单调性）。
 */
export function findWebmClusterOffsets(bytes: Uint8Array, from = 0, afterTimecode = -1): number[] {
  const hits: number[] = []
  let lastTimecode = afterTimecode
  for (let i = Math.max(0, from); i + CLUSTER_ID_BYTES.length <= bytes.length; i++) {
    if (bytes[i] !== CLUSTER_ID_BYTES[0]) continue
    if (bytes[i + 1] !== CLUSTER_ID_BYTES[1]) continue
    if (bytes[i + 2] !== CLUSTER_ID_BYTES[2]) continue
    if (bytes[i + 3] !== CLUSTER_ID_BYTES[3]) continue
    const timecode = readWebmClusterTimecode(bytes, i)
    if (timecode === null) continue
    if (timecode <= lastTimecode) continue // 偶然匹配 / 非递增 ⇒ 不是簇起点
    lastTimecode = timecode
    hits.push(i)
  }
  return hits
}

/** 把初始化前缀与一段载荷拼起来（拼出来的东西可以被单独解码）。 */
export function composeWebmWithInit(init: Uint8Array, payload: Uint8Array): Uint8Array {
  const out = new Uint8Array(init.length + payload.length)
  out.set(init, 0)
  out.set(payload, init.length)
  return out
}

/**
 * 增量切分器：把**陆续到达**的字节流切成 Cluster。
 *
 * 用法：录音每来一片就 `push()`，然后 `takeCompleteClusters()` 取走已经完整的簇。
 * 末尾那簇**不完整**（可能跨分片边界）⇒ 留在缓冲里等下一片。
 * 录音结束时用 `flush()` 把它当作最后一簇取走。
 */
export class WebmClusterSplitter {
  /** 还没被消费的字节（已完成的簇会被裁掉 ⇒ 缓冲稳定在 O(簇) 而不是 O(整场)）。 */
  private buffer: Uint8Array = new Uint8Array(0)
  private init: Uint8Array | null = null
  private initScanned = false
  private lastTimecode = -1

  /** 追加字节。 */
  push(bytes: Uint8Array): void {
    if (!bytes.length) return
    const merged = new Uint8Array(this.buffer.length + bytes.length)
    merged.set(this.buffer, 0)
    merged.set(bytes, this.buffer.length)
    this.buffer = merged
    if (!this.initScanned) {
      const prefix = extractWebmInitPrefix(this.buffer)
      if (prefix) {
        this.init = prefix
        this.initScanned = true
      }
    }
  }

  /** 初始化前缀（还没扫到时是 null）。 */
  get initPrefix(): Uint8Array | null {
    return this.init
  }

  /** 缓冲区里还没被消费的字节数。 */
  get bufferedBytes(): number {
    return this.buffer.length
  }

  /**
   * 取出已经**完整**的簇（后面还有下一段簇 ⇒ 它的结束位置是确定的）。
   *
   * ⚠ 末尾那簇永远不完整（它的结束要等下一片或等收尾）⇒ 它留在缓冲里。
   *   ⇒ 代价：实时转写会滞后「最后一段簇」的时长（实测约 4 秒）。
   */
  takeCompleteClusters(): WebmCluster[] {
    if (!this.init) return []
    const offsets = findWebmClusterOffsets(this.buffer, 0, this.lastTimecode)
    if (offsets.length < 2) return []
    const out: WebmCluster[] = []
    // 最后一段簇不完整（它的结束还没出现）⇒ 只取到倒数第二段为止。
    for (let i = 0; i + 1 < offsets.length; i++) {
      const start = offsets[i]
      const end = offsets[i + 1]
      const timecode = readWebmClusterTimecode(this.buffer, start)
      if (timecode === null) continue
      out.push({ bytes: this.buffer.slice(start, end), timecodeMs: timecode })
      this.lastTimecode = timecode
    }
    if (out.length) {
      // 裁掉已消费的前缀：缓冲里只剩「最后一段不完整的簇 + 它的后续字节」。
      // ⚠ 裁剪点是**最后一段簇的起点**（不是倒数第二段）—— 留错了会让缓冲
      //   多攒一整簇（实测 56 KB → 128 KB），也会让「已解码的簇」被重复计入。
      this.buffer = this.buffer.slice(offsets[offsets.length - 1])
    }
    return out
  }

  /**
   * 收尾：把缓冲里剩下的字节当作**已经结束**的音频取走。
   *
   * ⚠ 只有在**确认后面不会再有数据**时才能调用（录音结束 / 整块 blob）。
   * ⚠ 它返回的是**一段列表**而不是一段：缓冲里可能还压着好几段簇
   *   （末尾那几段在录音过程中一直「不完整」），只取第一段会把中间的音频丢掉。
   */
  flush(): WebmCluster[] {
    if (!this.init) return []
    const offsets = findWebmClusterOffsets(this.buffer, 0, this.lastTimecode)
    if (!offsets.length) return []
    const out: WebmCluster[] = []
    for (let i = 0; i + 1 < offsets.length; i++) {
      const timecode = readWebmClusterTimecode(this.buffer, offsets[i])
      if (timecode === null) continue
      out.push({ bytes: this.buffer.slice(offsets[i], offsets[i + 1]), timecodeMs: timecode })
      this.lastTimecode = timecode
    }
    // 最后一段：后面不会再有数据了 ⇒ 它的结束就是缓冲末尾。
    const lastOffset = offsets[offsets.length - 1]
    const lastTimecode = readWebmClusterTimecode(this.buffer, lastOffset)
    if (lastTimecode !== null) {
      out.push({ bytes: this.buffer.slice(lastOffset), timecodeMs: lastTimecode })
      this.lastTimecode = lastTimecode
    }
    this.buffer = new Uint8Array(0)
    return out
  }
}
