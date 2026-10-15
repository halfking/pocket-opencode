/**
 * speaker-features.ts — 说话人特征（纯 DSP，**不 import 任何原生模块**）
 *
 * ══ 为什么单独拆一个文件 ══
 * `speaker-embedding.ts` 顶部 `import { sherpa } from './sherpa'`，
 * 而那行**没有扩展名** ⇒ Node 的 ESM 解析器直接 `ERR_MODULE_NOT_FOUND`
 * ⇒ 整条说话人链路（embedding 提取 + 聚类判据）在 Node 里**一行都跑不起来**。
 * 于是：说话人分离从来没有过任何行为门。
 *
 * 本文件刻意零 import，于是它连同 `speaker-diarization.ts` 一起
 * 可以在 Node 里被 `node --test` 真的执行到（见同目录两个 *.test.ts）。
 * 这与 `voiceprint-writes.ts` 的约定是同一条：**决策与 DSP 保持纯，
 * I/O 一律外置**。
 *
 * ══ 为什么特征必须是频谱，不能是包络 ══
 * 原实现（`spectrumEmbedding`）把音频按时间均分成 32 块、每块取
 * 平均绝对值。它**只依赖时域幅度包络，对频谱完全无感** ——
 * 也就是对音色（共振峰）零敏感。
 *
 * 实测（2026-10-06，`node --experimental-strip-types` 跑真实导出函数）：
 *
 *   同一个幅度包络 + 基频差 2.4 倍（男 110Hz vs 女 260Hz）
 *     → cosine = 0.998473        ← 判据认为「同一个声音」
 *   不同的人（包络不同）
 *     → cosine = 0.241667        ← 判据认为「不同声音」
 *
 * 也就是说它实际在测「**这两段话的响度节奏像不像**」，
 * 而产品要的是「**这是不是同一个嗓音**」。两个方向都错：
 * 同一人换个说法被拆成多个说话人，不同的人说话节奏一致被并成一个。
 *
 * 本文件改用 **log-mel 谱的均值 + 标准差（48 维）**：
 *   · 均值 = 长期平均谱 → 主要由声道形状决定 = 音色 = 说话人身份；
 *   · 标准差 = 发音动态 → 与语速、口型开合有关，同属说话人特征。
 *   · 每一帧先做 **CMVN**（减去该帧 24 个 log-mel 值的均值），
 *     去掉帧级的电平/谱倾斜差异，只留音色。
 *
 * 实测（同一批信号，3 音色各 3 句）：
 *   无逐帧 CMVN：同人 0.9999~1.0000 / 异人 0.9818~0.9847，窗口 0.0151
 *   有逐帧 CMVN：同人 0.9989~1.0000 / 异人 0.9325，      窗口 0.0664
 *
 * ⚠ **合成语音的窗口宽度严重乐观**。用真实人声谱（TTS 神经声码器，
 *   9 音色 × 3 句）重测后，异人尾部升到 0.9901，与阈值 0.99 只差 0.0001。
 *   逐帧 CMVN 在真实数据上同样占优（异人 max 0.9927 → 0.9901，
 *   同人 min 0.9059 → 0.9441，两个错误方向都更好），
 *   所以保留；但它**没有**解决分布重叠。标定见 speaker-diarization.ts。
 */

/** FFT 长度（= 帧长）。必须是 2 的幂。 */
export const FFT_SIZE = 512
/** 帧移（10ms @16k）。 */
export const HOP = 160
/** mel 滤波器组个数。 */
export const MEL_BANDS = 24
/** 输出维度 = 均值 MEL_BANDS + 标准差 MEL_BANDS。 */
export const EMBED_DIM = MEL_BANDS * 2

const SAMPLE_RATE = 16000
const F_MIN = 80
const F_MAX = 7600
/** 数值下限，避免 log(0) 产生 -Inf 把整条链污染掉。 */
const EPS = 1e-10

const hzToMel = (hz: number) => 2595 * Math.log10(1 + hz / 700)
const melToHz = (mel: number) => 700 * (10 ** (mel / 2595) - 1)

/** 三角 mel 滤波器组：NMEL 行 × (FFT_SIZE/2+1) 列。 */
function buildMelBank(): Float32Array[] {
  const bins = FFT_SIZE / 2 + 1
  const lo = hzToMel(F_MIN)
  const hi = hzToMel(F_MAX)
  const edge = Array.from({ length: MEL_BANDS + 2 }, (_, i) =>
    melToHz(lo + ((hi - lo) * i) / (MEL_BANDS + 1)),
  )
  const bank: Float32Array[] = []
  for (let m = 0; m < MEL_BANDS; m++) {
    const row = new Float32Array(bins)
    const a = edge[m]
    const b = edge[m + 1]
    const c = edge[m + 2]
    for (let k = 0; k < bins; k++) {
      const f = (k * SAMPLE_RATE) / FFT_SIZE
      if (f <= a || f >= c) continue
      row[k] = f < b ? (f - a) / (b - a) : (c - f) / (c - b)
    }
    bank.push(row)
  }
  return bank
}

const MEL_BANK = buildMelBank()
const WINDOW = (() => {
  const w = new Float64Array(FFT_SIZE)
  for (let i = 0; i < FFT_SIZE; i++) w[i] = 0.5 - 0.5 * Math.cos((2 * Math.PI * i) / FFT_SIZE)
  return w
})()

/** 原地迭代式基-2 FFT。 */
function fft(re: Float64Array, im: Float64Array): void {
  const n = re.length
  for (let i = 1, j = 0; i < n; i++) {
    let bit = n >> 1
    for (; j & bit; bit >>= 1) j ^= bit
    j ^= bit
    if (i < j) {
      const tr = re[i]; re[i] = re[j]; re[j] = tr
      const ti = im[i]; im[i] = im[j]; im[j] = ti
    }
  }
  for (let len = 2; len <= n; len <<= 1) {
    const ang = (-2 * Math.PI) / len
    const wr = Math.cos(ang)
    const wi = Math.sin(ang)
    const half = len >> 1
    for (let i = 0; i < n; i += len) {
      let cr = 1
      let ci = 0
      for (let k = 0; k < half; k++) {
        const ar = re[i + k]
        const ai = im[i + k]
        const br = re[i + k + half]
        const bi = im[i + k + half]
        const vr = br * cr - bi * ci
        const vi = br * ci + bi * cr
        re[i + k] = ar + vr
        im[i + k] = ai + vi
        re[i + k + half] = ar - vr
        im[i + k + half] = ai - vi
        const nr = cr * wr - ci * wi
        ci = cr * wi + ci * wr
        cr = nr
      }
    }
  }
}

/**
 * 采样率必须与 `FFT_SIZE` 的频率分辨率配套使用。
 *
 * ⚠ 录音链路固定 16k（`recording-audio-transcode.ts` / `extractWebEmbedding`
 *   都用 16000），所以这里不做重采样。**改采样率就必须同时改 F_MAX 与
 *   滤波器组**，否则 `melToHz` 算出的频点会落在奈奎斯特之外，
 *   高频权重恒为 0 —— 而高频正是区分男女声最有效的部分。
 */
export const FEATURE_SAMPLE_RATE = SAMPLE_RATE

/**
 * PCM（单声道，16k）→ 说话人特征向量。
 *
 * 短于一帧的输入返回**零向量**——由 `isUsableEmbedding` 判为「无信息」，
 * 而不是被当成一个诡异的说话人特征拿去参与聚类。
 */
/**
 * 参与统计的**最低帧能量分位**。
 *
 * ⚠ **它没有解决噪声问题，只是略有改善**（2026-10-06 实测，合成语音叠白噪，
 *   同一人的相似度；开/关本门控）：
 *     底噪 0.001 → 0.9765 / **0.9815**
 *     底噪 0.01  → 0.9267 / **0.9380**
 *     底噪 0.03  → 0.7201 / **0.7348**
 *   也就是说：**只要底噪达到 0.001 量级，同一个人就会被拆成好几个说话人**
 *   （阈值 0.99，见 speaker-diarization.ts）。
 *   这不是本特征独有的软肋，而是「长期平均谱」这类廉价特征的根本限制。
 *   真机校准见 speaker-diarization.ts 的 `SAME_SPEAKER_MIN` 头注释。
 *
 *   留它是因为它在整条噪声曲线上**单调变好**，且不损害可分性
 *   （同人 0.9999~1.0000 / 异人 0.9818~0.9847，区间仍无重叠）。
 */
const VOICED_PERCENTILE = 0.30

export function logMelEmbedding(pcm: Float32Array): Float32Array {
  const out = new Float32Array(EMBED_DIM)
  const frames = Math.floor((pcm.length - FFT_SIZE) / HOP) + 1
  if (frames <= 0) return out

  // 第一趟：逐帧能量，算出「有声门限」。
  const energy = new Float64Array(frames)
  for (let t = 0; t < frames; t++) {
    const base = t * HOP
    let e = 0
    for (let i = 0; i < FFT_SIZE; i++) {
      const v = pcm[base + i] ?? 0
      e += v * v
    }
    energy[t] = e
  }
  const sorted = Float64Array.from(energy).sort()
  const gate = sorted[Math.min(frames - 1, Math.floor(frames * VOICED_PERCENTILE))]

  const acc = new Float64Array(MEL_BANDS)
  const acc2 = new Float64Array(MEL_BANDS)
  const frame = new Float64Array(MEL_BANDS)
  const re = new Float64Array(FFT_SIZE)
  const im = new Float64Array(FFT_SIZE)
  let used = 0

  for (let t = 0; t < frames; t++) {
    if (energy[t] < gate) continue
    used++
    const base = t * HOP
    for (let i = 0; i < FFT_SIZE; i++) re[i] = (pcm[base + i] ?? 0) * WINDOW[i]
    im.fill(0)
    fft(re, im)
    // ★ 逐帧 CMVN：先算这一帧的 24 个 log-mel 值，减掉它们自己的均值。
    //   去掉的是**帧级**的电平/谱倾斜差异（麦克风离嘴的距离、语速带来的
    //   宽带增益变化），留下的才是音色。
    let frameMean = 0
    for (let m = 0; m < MEL_BANDS; m++) {
      const row = MEL_BANK[m]
      let e = 0
      for (let k = 0; k < row.length; k++) {
        const w = row[k]
        if (w !== 0) e += w * (re[k] * re[k] + im[k] * im[k])
      }
      frame[m] = Math.log(e + EPS)
      frameMean += frame[m]
    }
    frameMean /= MEL_BANDS
    for (let m = 0; m < MEL_BANDS; m++) {
      const v = frame[m] - frameMean
      frame[m] = v
      acc[m] += v
      acc2[m] += v * v
    }
  }
  // ⚠ `used >= 1` 由构造保证：gate 取自 energy 自身的第 30 百分位，
  //   达到该值的帧满足 `energy[t] < gate` 为假 ⇒ 至少有一帧入选。
  //   所以这里**不需要**除零守卫 —— 曾写过一个 `if (used === 0) return out`，
  //   变异验证（去掉它，40 条门全绿）证明它不可达，已删。
  //   （「不可达的死代码」比「多一行保险」更贵：它看起来像被覆盖了，其实没有。）

  for (let m = 0; m < MEL_BANDS; m++) {
    const mean = acc[m] / used
    out[m] = mean
    out[MEL_BANDS + m] = Math.sqrt(Math.max(0, acc2[m] / used - mean * mean))
  }

  // ★ CMVN：先减全局均值再 L2 归一。
  //   去掉之后实测（合成语音）：音量差 ÷2/÷4/÷8 时同一人相似度只降
  //   0.000~0.003，**要到 ÷64 才跌破 0.99 阈值**；底噪 0.001 时 0.9640
  //   vs 0.9765。⇒ 它是**稳健性增强**，不是「不做就彻底不能用」，
  //   门禁因此断言的是它保证的那条性质（向量均值为 0），
  //   而不是「撤掉它会崩」—— 后者在现实音量范围内并不成立。
  let mean = 0
  for (let i = 0; i < out.length; i++) mean += out[i]
  mean /= out.length
  let norm = 0
  for (let i = 0; i < out.length; i++) {
    out[i] -= mean
    norm += out[i] * out[i]
  }
  norm = Math.sqrt(norm) || 1
  for (let i = 0; i < out.length; i++) out[i] /= norm
  return out
}

/**
 * 向量是否携带可用信息。
 *
 * 零向量**必须**在进入聚类之前就被识别出来。原实现里
 * `ingest-speech.ts` 写的是 `extractEmbedding(blob).catch(() => new Float32Array())`，
 * 而 `cosineSimilarity(零向量, 任意)` 恒为 0 < 阈值
 * ⇒ **每一次 embedding 失败都被算成一个新说话人**，且没有任何提示。
 *
 * ⚠ 必须**分两趟**扫，不能一趟里见到非零就 return：
 *   `for (...) { if (!finite) return false; if (x !== 0) return true }`
 *   会在 index 0 就返回 true，于是**排在后面的 NaN 永远看不到**
 *   —— 而一个带 NaN 的向量会静默污染该 profile 的每一次后续比较。
 *   （这道断言就是为此写的：向量前 7 格是 0.5、第 8 格是 NaN。）
 */
export function isUsableEmbedding(v: Float32Array | null | undefined): v is Float32Array {
  if (!v || v.length === 0) return false
  for (let i = 0; i < v.length; i++) {
    if (!Number.isFinite(v[i])) return false
  }
  for (let i = 0; i < v.length; i++) {
    if (v[i] !== 0) return true
  }
  return false
}

/**
 * 余弦相似度。**维度不同直接返回 -1**（不可比），而不是截断到 `min(len)`。
 *
 * 为什么必须严格：原生 cap-sherpa 的 ECAPA 向量是 192/512 维，
 * Web 兜底是 48 维，`local_voiceprints` 里两种都可能有。
 * 原实现 `for (i < Math.min(a.length, b.length))` 拿 ECAPA 的**前 48 维**
 * 去和 48 维频谱特征比 —— 实测同一个人相似度 0.2298，**永远不会被认出来**。
 * 也就是说：只要用户曾在支持 ECAPA 的设备上录过一次声纹，之后在 Web 上
 * 每段都会变成新说话人。
 *
 * -1 而不是 0 的理由：0 是一个**合法**的相似度（「正交」），
 * 让「不可比」和「正交」同值，调用方就分不清该跳过还是该新建。
 */
export function cosineSimilarity(a: Float32Array, b: Float32Array): number {
  if (a.length !== b.length) return -1
  let dot = 0
  for (let i = 0; i < a.length; i++) dot += a[i] * b[i]
  return dot
}

/** 两个向量是否属于同一个特征空间（维度一致）。 */
export function isComparable(a: Float32Array, b: Float32Array): boolean {
  return a.length === b.length
}

export function embeddingToBlob(v: Float32Array): Uint8Array {
  return new Uint8Array(v.buffer, v.byteOffset, v.byteLength)
}

/**
 * 反序列化。
 *
 * ⚠ 旧实现写的是 `new Float32Array(arr.buffer, arr.byteOffset, arr.byteLength / 4)`。
 *   非 4 整除的长度**不会**抛错（V8 截断小数），实测 `byteLength=11` 照样
 *   返回 length=2 —— 所以「会 RangeError」那个说法是错的。
 *   真正会炸的是**偏移未对齐**：`byteOffset` 不是 4 的倍数时抛
 *   `RangeError: start offset of Float32Array should be a multiple of 4`。
 *   而这行在 `loadSpeakerProfiles` 的 `map` 里 ⇒ 一次错位就可能让
 *   **整场录音的说话人列表加载失败**，外层 catch 把它吞成「空库」。
 *   现在复制一份到 byteOffset=0 的新缓冲区，长度向下取整。
 */
export function blobToEmbedding(buf: Uint8Array | ArrayBuffer): Float32Array {
  const arr = buf instanceof Uint8Array ? buf : new Uint8Array(buf)
  const usable = arr.byteLength - (arr.byteLength % 4)
  if (usable === 0) return new Float32Array(0)
  const copy = arr.slice(0, usable)
  return new Float32Array(copy.buffer, 0, usable / 4)
}
