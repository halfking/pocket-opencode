/**
 * native-audio-parts.ts — 把**盘上已有的原生分片**流式接成整场音频的若干块（§954）。
 *
 * ── 为什么需要它（§954.1 的缺陷链，源码级可核对）──
 *   §826 查实：Android（生产主路径）上**整段高精度重转永远不发生**：
 *     ① `isBackgroundMicSupported()` 在原生端恒为真 ⇒ `nativeMode = true`；
 *     ② `new VadSegmenter(...)` 在 `if (!this.nativeMode)` 里 ⇒ 段器不存在；
 *     ③ `stop()` 的 `fullBlob = (await this.vadSegmenter?.stop()) ?? null` ⇒ 恒 null；
 *     ④ `saveMeetingAudio` 全仓只有一处调用，在 ③ 的 `if` 里 ⇒ 完整音频从不落盘；
 *     ⑤ `loadMeetingAudio` 恒返回 null ⇒ `refetchFullTranscript` 恒 `audio-not-found`。
 *   ⇒ 用户在 Android 上只拿到 **8 秒粒度**的实时转写，**第二阶段高精度全文从不发生**。
 *
 * ── 但整场音频其实早就在盘上 ──
 *   `saveAudioPart` 把**每一片**都写到了 `files/recordings/{meetingId}/part-N.<ext>`
 *   ⇒ 整场音频**已经存在**，缺的只是「把它接起来」这一步。
 *   ⇒ §826.6 ① 登记的那条待拍板项问的是「**要不要用已落盘的分片拼出整段音频**」，
 *     理由是「**它会改存储形态**」。
 *   ⚠⚠ **本模块不撞那条**：它**不拼、不存**——不产生任何新的整场音频文件或记录，
 *     只是**按块把已经在盘上的分片读出来直接送去转写** ⇒ **存储形态一个字没改**
 *     ⇒ 那一问的前提（「会改存储形态」）在本方案下**不成立**。
 *   ⇒ 本模块要守的不变量：**内存峰值与会议时长无关**（只与块长有关）。
 *
 * ── 切点规则：与 Web 链**完全同一套** ──
 *   复用 `meeting-full-transcribe-chunks.ts` 的 `planWavChunks` / `sliceWavChunk`
 *   ⇒ 切点仍然落在**最安静的位置**（不是硬切）⇒ 不会把词劈开（§927 的原意）。
 *   ⇒ 而「攒多少才切」这一步必须比 Web 链**多攒一点**：
 *     `planWavChunks` 的搜索窗是「目标点 ± searchSeconds」，且它的上界
 *     `hi` 被 `totalFrames - minChunkFrames` 夹住 ⇒ **组不够长就找不到切点**
 *     （`lo > hi` ⇒ 直接 break ⇒ 整组变成一块）。
 *     ⇒ 组的长度取 `chunkSeconds + searchSeconds + minChunkSeconds`
 *       （8 + 0.33 + 1 分钟 ⇒ 560 s）⇒ 切点才有真正的 ±20 s 搜索窗。
 *
 * ── 峰值内存 ──
 *   组（≈560 s ⇒ 17.9 MB）+ carry（≤100 s ⇒ 3.2 MB）+ 一块（≈480 s ⇒ 15.4 MB）
 *   ⇒ **与会议时长无关**（1 小时与 8 小时同量级）⇒ 满足属主那条
 *     「时长无限长、只取决于存储的大小」。
 *   ⚠ 对照：把整场拼成一块再送，峰值 = 整场（115.2 MB/小时 ⇒ 8 小时 921 MB）。
 *
 * ── §955：这一层的**形状**被收紧成「生产者的类型比消费者的更严格」──
 *   §954 落地后，「§805 的时长护栏在原生链上复活」那句话是**假的**：
 *   生产者产出的形状里**没有 `durationSec`** ⇒ 消费侧读到 `undefined`
 *   ⇒ 护栏**仍然**恒判 `no-ground-truth`（详见 `NativeAudioChunkSource` 的头注释）。
 *   ⇒ 而当时判据**量不到**真件（真件住在 import 了 `Filesystem`/`local-db` 的
 *   模块里 ⇒ **node 跑不到**）⇒ ⇒ 本模块因此多了一个**纯的**入口
 *   （`streamNativeAudioChunkSources`，**I/O 注入**）⇒ 真件在 node 里可直接跑。
 */
import {
  FULL_TRANSCRIBE_CHUNK_SECONDS,
  FULL_TRANSCRIBE_CUT_SEARCH_SECONDS,
  MIN_CHUNK_SECONDS,
  parseWavLayout,
  planWavChunks,
  sliceWavChunk,
  type WavLayout,
} from './meeting-full-transcribe-chunks.ts'

/** 一片原生分片的字节（**完整 WAV 文件**：44 字节头 + PCM 载荷）。 */
export type WavPartBytes = Uint8Array

/**
 * §955：一块「分块音频源」—— 全量重转的**输入单位**（**生产者的形状**）。
 *
 * ⚠⚠⚠ `durationSec` 在这里是**必填**，而此前它是**可选**的 ⇒ 这一节要说清为什么：
 *
 *   §954 让原生链的整场重转**真的发生**（从盘上分片流式供给），并在文档里写下
 *   「**§805 的时长护栏在原生链上复活**：`measuredAudioSec` 现在由各块时长之和给出」。
 *   ⇒ 而那句话是**假的**：生产者的形状是**手写的对象字面量**
 *     （`audio-parts.ts` 里 `yield { blob, offsetSec, bytes }`），
 *     **没有 `durationSec`**；消费侧（`meeting-final-transcript.ts`）读的是
 *     `chunk.durationSec` ⇒ 恒 `undefined` ⇒ `measuredAudioSec` 恒不产出
 *     ⇒ `finalizeRecording` 的 `measuredAudioSec` 恒 `null`
 *     ⇒ 护栏**仍然**恒判 `no-ground-truth`（还是死的）。
 *   ⇒ 而**类型系统没拦住**它，因为：① 消费侧那个字段是**可选**的；
 *     ② 生产者的形状是**匿名对象字面量**（多一个 `bytes`、少一个 `durationSec`
 *     都仍然「合法」）。
 *   ⇒ ⇒ ★ **「消费侧有可选字段」+「生产侧是手写字面量」= 一道没有牙的接缝**：
 *     两边都「对」，合起来却把信息丢了，而**所有行为判据照样全绿**
 *     （§954 的 J-12 用的**假件**比真件还完整 ⇒ 那一格永远量不到真件）。
 *
 * ⇒ 修法（本模块）：**生产者的类型比消费者的更严格** ⇒
 *   `durationSec` 必填 ⇒ 谁再把它写漏，**编译期就红**（不是运行时才发现）。
 */
export interface NativeAudioChunkSource {
  /** 这一块的音频（WAV）。 */
  blob: Blob
  /** 这一块在整场里的起点（秒）—— 用来把回执的时间轴平移回整场。 */
  offsetSec: number
  /** 这一块的音频时长（秒）—— **必填**：§805 时长护栏唯一的真值来源。 */
  durationSec: number
}

/** §955：生成块的选项（在切块选项之上多一个「只进消息」的标签）。 */
export interface NativeAudioChunkSourceOptions extends NativeWavChunkOptions {
  /** 报错时用来指认是哪一场（**只进消息**，不参与任何判定）。 */
  label?: string
}

/**
 * 音频分片在应用数据目录下的**相对目录**。
 *
 * ⚠ 写入与读回**共用这一处** ⇒ 不会「写一处、读另一处」（那正是 §954 之前
 *   「文件名叫 `.webm`、内容是 WAV」那类漂移的温床）。
 */
export function audioPartDir(meetingId: string): string {
  return `recordings/${meetingId}`
}

/** 音频分片在应用数据目录下的**相对路径**。 */
export function audioPartRelativePath(meetingId: string, seq: number, ext: string): string {
  return `${audioPartDir(meetingId)}/part-${seq}.${ext}`
}

/**
 * MIME → 落盘扩展名。
 *
 * ⚠⚠ **§954**：此前这条路径**写死 `.webm`**（`${dir}/part-${seq}.webm`），
 *   而原生链的产物是 **WAV**（`MeetingRecordService.emitPart` 里 `mimeType` 写死
 *   `"audio/wav"`，字节是 `pcmToWav` 出来的完整 WAV）
 *   ⇒ **文件名说 webm、内容是 WAV** ⇒ 任何「按扩展名判格式」的读法都会读错。
 *   ⇒ 现在按实际 MIME 取扩展名。
 *   ⚠ **向后兼容**：老库里的文件仍叫 `part-N.webm`（内容也是 WAV）
 *   ⇒ 读回时按 [`mimeType` 推出的扩展名, 老的 `.webm`] **依次尝试**。
 */
export function audioPartExtForMime(mimeType: string): string {
  const t = (mimeType || '').toLowerCase().split(';', 1)[0].trim()
  if (t === 'audio/wav' || t === 'audio/x-wav' || t === 'audio/wave' || t === 'audio/vnd.wave') return 'wav'
  if (t === 'audio/ogg' || t === 'application/ogg') return 'ogg'
  if (t === 'audio/mpeg' || t === 'audio/mp3') return 'mp3'
  if (t === 'audio/mp4' || t === 'audio/x-m4a') return 'm4a'
  if (t === 'audio/flac') return 'flac'
  return 'webm'
}

/** 切好的一块（**完整 WAV 文件**）。 */
export interface NativeWavChunk {
  /** 完整 WAV（头 + 载荷）。 */
  wav: Uint8Array<ArrayBuffer>
  /** 这一块在整场里的起点（秒）—— 用来把回执的时间轴平移回整场。 */
  offsetSec: number
  /** 这一块的音频时长（秒）。 */
  durationSec: number
}

export interface NativeWavChunkOptions {
  /** 每块的目标时长（秒）。 */
  chunkSeconds?: number
  /** 切点搜索窗（秒）：在目标点 ±这个范围内找最安静的位置。 */
  searchSeconds?: number
  /** 每块的最小长度（秒）。 */
  minChunkSeconds?: number
}

interface ResolvedOptions {
  chunkSeconds: number
  searchSeconds: number
  minChunkSeconds: number
}

function resolveOptions(opts: NativeWavChunkOptions = {}): ResolvedOptions {
  return {
    chunkSeconds: opts.chunkSeconds ?? FULL_TRANSCRIBE_CHUNK_SECONDS,
    searchSeconds: opts.searchSeconds ?? FULL_TRANSCRIBE_CUT_SEARCH_SECONDS,
    minChunkSeconds: opts.minChunkSeconds ?? MIN_CHUNK_SECONDS,
  }
}

/**
 * 取出一片 WAV 的**载荷段**与布局。
 *
 * @returns null = 这不是可解析的 16-bit PCM WAV（调用方据此中止整条流）。
 */
export function readWavPart(part: Uint8Array): { payload: Uint8Array; layout: WavLayout } | null {
  const layout = parseWavLayout(part)
  if (!layout) return null
  const payload = part.subarray(layout.dataOffset, layout.dataOffset + layout.dataLength)
  return { payload, layout }
}

/** 造一个 16-bit PCM WAV 的 44 字节头。 */
export function buildWavHeader(
  dataLength: number,
  sampleRate: number,
  channels: number,
  bits: number,
): Uint8Array {
  const header = new Uint8Array(44)
  const view = new DataView(header.buffer)
  const putStr = (off: number, s: string) => {
    for (let i = 0; i < s.length; i++) view.setUint8(off + i, s.charCodeAt(i))
  }
  const bytesPerSample = bits / 8
  putStr(0, 'RIFF')
  view.setUint32(4, 36 + dataLength, true)
  putStr(8, 'WAVE')
  putStr(12, 'fmt ')
  view.setUint32(16, 16, true)
  view.setUint16(20, 1, true) // PCM
  view.setUint16(22, channels, true)
  view.setUint32(24, sampleRate, true)
  view.setUint32(28, sampleRate * channels * bytesPerSample, true)
  view.setUint16(32, channels * bytesPerSample, true)
  view.setUint16(34, bits, true)
  putStr(36, 'data')
  view.setUint32(40, dataLength, true)
  return header
}

function concatBytes(chunks: Uint8Array[], total: number): Uint8Array<ArrayBuffer> {
  const out = new Uint8Array(total)
  let at = 0
  for (const c of chunks) {
    out.set(c, at)
    at += c.length
  }
  return out
}

/**
 * 切块规划器（**无 I/O** ⇒ node 里可直接测）。
 *
 * 用法：`push(part)` 喂进一片 ⇒ 返回 0 或 1 块；最后 `flush()` 收尾。
 * ⚠ 之所以做成「喂一片出一块」而不是「一次给全部」：
 *   一次性给全部 ⇒ 规划器自己就得持有整场 ⇒ 内存又变成 O(时长)，
 *   而本节要的正是**把那条 O(时长) 拆掉**。
 */
export class NativeWavChunkPlanner {
  private readonly opts: ResolvedOptions
  /** 组的目标长度：必须够长，`planWavChunks` 才找得到「安静」的切点。 */
  private readonly groupTargetSeconds: number
  /** 上一次的尾巴（切点之后剩下的那一截）⇒ 接到下一组的前面。 */
  private carry: Uint8Array | null = null
  private carrySeconds = 0
  /** 本组新收进来的载荷片。 */
  private group: Uint8Array[] = []
  private groupBytes = 0
  private groupSeconds = 0
  /** 已见到的片的 WAV 布局（采样率/声道/位深必须全组一致）。 */
  private layout: WavLayout | null = null
  /** 已经吐出去的秒数（整场口径）。 */
  private emittedSeconds = 0

  constructor(opts: NativeWavChunkOptions = {}) {
    this.opts = resolveOptions(opts)
    this.groupTargetSeconds =
      this.opts.chunkSeconds + this.opts.searchSeconds + this.opts.minChunkSeconds
  }

  /** 已经吐出去的秒数（整场口径）—— 调用方用它算「整场真实时长」。 */
  get emittedSec(): number {
    return this.emittedSeconds
  }

  /**
   * 规划器手上**正留着**的字节数（carry + 本组）—— **峰值内存的代理量**。
   *
   * ⚠⚠ 这条属性是**专门给门禁量的**，而且它量到的是一条**别的判据全都看不见**的性质：
   *   变异 M1（切块阈值只按「新收的秒数」算、忘了算 carry）会让 carry **单调增长**
   *   ⇒ 手上留着的字节随时长线性涨 ⇒ 峰值内存回到 **O(时长)**
   *   ⇒ 而**块长不变、时间轴连续、一秒不丢**这三条判据**照样全绿**。
   *   ⇒ ⇒⇒ ⇒ ★ **「所有行为判据都绿」不等于「没有内存问题」**：
   *   内存这条性质**必须单独量**，量不到就等于没守。
   */
  get heldBytes(): number {
    return (this.carry?.length ?? 0) + this.groupBytes
  }

  /**
   * 喂进一片。
   *
   * @throws 这片不是可解析的 16-bit PCM WAV，或与已见到的片布局不一致
   *         （⇒ 宁可整条中止，也不要**静默丢掉一段音频**）。
   */
  push(part: Uint8Array): NativeWavChunk[] {
    const parsed = readWavPart(part)
    if (!parsed) {
      throw new Error('原生音频分片不是可解析的 16-bit PCM WAV')
    }
    const { payload, layout } = parsed
    if (this.layout) {
      if (
        layout.sampleRate !== this.layout.sampleRate
        || layout.channels !== this.layout.channels
        || layout.bitsPerSample !== this.layout.bitsPerSample
      ) {
        throw new Error('原生音频分片的 WAV 布局不一致（采样率/声道/位深）')
      }
    } else {
      this.layout = layout
    }
    this.group.push(payload)
    this.groupBytes += payload.length
    this.groupSeconds += payload.length / (layout.sampleRate * layout.blockAlign)
    // ⚠⚠ 阈值必须按**组的长度**算（carry + 新收的），**不是**只按新收的算。
    //   只按新收的算 ⇒ carry 会被**逐轮累加**（每轮 +60~108 s）⇒ 越滚越大
    //   ⇒ 切出来的块越来越长 ⇒ 最后那块会突破服务端 25 MiB 的请求体闸
    //   （实测那个 bug：1 小时的收尾块涨到 764 s ⇒ base64 后 32.6 MB ⇒ 必然被拒）。
    if (this.groupTotalSeconds() < this.groupTargetSeconds) return []
    return this.cutGroup(false)
  }

  /** 收尾：把剩下的（含 carry）**继续切**，不许一次吐出一块超长的。 */
  flush(): NativeWavChunk[] {
    const layout = this.layout
    if (!layout) return []
    if (this.group.length === 0 && !this.carry) return []
    // ⚠ 收尾这块**同样要切**：会议结束时剩下的那一截可能比目标块长得多
    //   （切点落在 460~500 s ⇒ 收尾那一截最长可达 ~800 s）⇒
    //   一次吐出去 = 一个 25 MB 的 WAV ⇒ base64 后 33 MB ⇒ **超服务端请求体闸**。
    const out = this.cutGroup(true)
    this.resetGroup()
    this.carry = null
    this.carrySeconds = 0
    return out
  }

  /** 本组的总时长（carry + 新收的）。 */
  private groupTotalSeconds(): number {
    return this.carrySeconds + this.groupSeconds
  }

  private concatGroup(): Uint8Array {
    const chunks: Uint8Array[] = []
    let total = 0
    if (this.carry) {
      chunks.push(this.carry)
      total += this.carry.length
    }
    for (const c of this.group) {
      chunks.push(c)
      total += c.length
    }
    return concatBytes(chunks, total)
  }

  private wrap(payload: Uint8Array, layout: WavLayout): Uint8Array<ArrayBuffer> {
    const header = buildWavHeader(
      payload.length,
      layout.sampleRate,
      layout.channels,
      layout.bitsPerSample,
    )
    return concatBytes([header, payload], header.length + payload.length)
  }

  /**
   * 把攒够的一组切掉。
   *
   * ⚠ 切点规则**完全复用** `planWavChunks`（最安静处）⇒ 与 Web 链同一套。
   * ⚠ 末块留作 carry ⇒ 下一组从它后面接上 ⇒ 不会在切点处丢掉音频。
   * @param final true = 这是最后一组 ⇒ 末块**也要吐出去**（不再留 carry）。
   */
  private cutGroup(final: boolean): NativeWavChunk[] {
    const layout = this.layout
    if (!layout) return []
    const groupPayload = this.concatGroup()
    const groupWav = this.wrap(groupPayload, layout)
    const plan = planWavChunks(groupWav.buffer as ArrayBuffer, {
      chunkSeconds: this.opts.chunkSeconds,
      searchSeconds: this.opts.searchSeconds,
      minChunkSeconds: this.opts.minChunkSeconds,
    })
    const out: NativeWavChunk[] = []
    if (!plan || plan.bounds.length <= 1) {
      // 整组装得下一块 ⇒ 整组作为一块（**不切**，切点无从谈起）。
      out.push({
        wav: groupWav,
        offsetSec: this.emittedSeconds,
        durationSec: this.groupTotalSeconds(),
      })
      this.emittedSeconds += this.groupTotalSeconds()
      this.resetGroup()
      return out
    }
    const last = plan.bounds.length - 1
    const emitUpTo = final ? plan.bounds.length : last
    for (let i = 0; i < emitUpTo; i++) {
      const [from, to] = plan.bounds[i]
      const wav = sliceWavChunk(groupWav.buffer as ArrayBuffer, plan, i)
      out.push({
        wav: new Uint8Array(wav),
        offsetSec: this.emittedSeconds,
        durationSec: (to - from) / layout.sampleRate,
      })
      this.emittedSeconds += (to - from) / layout.sampleRate
    }
    if (final) {
      // 末块也吐出去了 ⇒ 不留 carry。
      this.resetGroup()
      return out
    }
    // 末块留作 carry：接到下一组的前面 ⇒ 切点处**不丢音频**。
    const [from, to] = plan.bounds[last]
    this.carry = groupPayload.slice(from * layout.blockAlign, to * layout.blockAlign)
    this.carrySeconds = (to - from) / layout.sampleRate
    this.resetGroup()
    return out
  }

  private resetGroup(): void {
    this.group = []
    this.groupBytes = 0
    this.groupSeconds = 0
  }
}

/**
 * 把一片片分片流式接成若干块。
 *
 * ⚠ 这是**唯一**的生产入口：规划器与它共用同一份逻辑 ⇒ 门禁测的就是生产跑的那条。
 */
export async function* streamNativeWavChunks(
  parts: AsyncIterable<Uint8Array>,
  opts: NativeWavChunkOptions = {},
): AsyncGenerator<NativeWavChunk> {
  const planner = new NativeWavChunkPlanner(opts)
  for await (const part of parts) {
    for (const chunk of planner.push(part)) yield chunk
  }
  for (const chunk of planner.flush()) yield chunk
}

/**
 * §955：把盘上的分片**流式**接成整场音频的若干块（**生产者的本体**）。
 *
 * ⚠⚠⚠ **为什么把 I/O 注入出来、把它放在这个纯模块里**：
 *
 *   §954 之后，生产者住在 `audio-parts.ts`，而那个文件 import 了
 *   `@capacitor/filesystem` 与 localDB ⇒ **node 里跑不到**
 *   ⇒ 判据只能对着**假件**测 ⇒ **真件与假件之间那道接缝从未被量到**
 *   ⇒ 于是「假件带 `durationSec`、真件不带」这件事，**所有判据都看不见**
 *   ⇒ §954 才会写下那句假的「护栏在原生链上复活」。
 *   ⇒ 现在：**读哪一片、读到什么，由调用方注入** ⇒ 生产者本体在 node 里直接跑
 *   ⇒ ⇒ ★ **判据必须能跑到真件**（否则「假件比真件完整」这条永远查不出来）。
 *
 * ⭐ **逐字段显式映射**（不用 `{...chunk}` 展开）：展开会把「哪个字段该叫什么」
 *   藏在运行期，而**字段名漂移**（§954 那条接缝的病根）恰恰是编译期看不见的那一类
 *   ⇒ 显式写出来 ⇒ 漏字段/写错名都当场暴露。
 *
 * ⚠ **读不到一片就整条中止**（§954 的决定，**这里第一次可测**）：
 *   跳过一个读不到的片 ⇒ 那一段音频变成一个**洞** ⇒ 洞之后的偏移会与墙钟错开
 *   ⇒ 说话人归属整体错位 ⇒ **比「没有整场重转」更糟**。
 */
export async function* streamNativeAudioChunkSources(
  files: ReadonlyArray<{ seq: number; mimeType: string }>,
  readPart: (seq: number, mimeType: string) => Promise<Uint8Array | null>,
  opts: NativeAudioChunkSourceOptions = {},
): AsyncGenerator<NativeAudioChunkSource> {
  const { label, ...chunkOpts } = opts
  async function* parts(): AsyncGenerator<Uint8Array> {
    for (const f of files) {
      const bytes = await readPart(f.seq, f.mimeType)
      if (!bytes) {
        throw new Error(
          `原生音频分片读不到：${label ? `${label} ` : ''}seq=${f.seq}`,
        )
      }
      yield bytes
    }
  }
  for await (const chunk of streamNativeWavChunks(parts(), chunkOpts)) {
    yield {
      blob: new Blob([chunk.wav], { type: 'audio/wav' }),
      offsetSec: chunk.offsetSec,
      durationSec: chunk.durationSec,
    }
  }
}
