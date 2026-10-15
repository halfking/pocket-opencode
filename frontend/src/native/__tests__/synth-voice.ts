/**
 * __tests__/synth-voice.ts —— 门禁用合成语音夹具（**不是测试文件**）
 *
 * 刻意不用真实录音：门禁必须能在任何机器上确定性地跑出同样的数，
 * 而真机录音进不了版本库。
 *
 * 合成方式刻意贴近「音色」的物理来源：
 *   基频（f0，音高）+ 三个共振峰的三角包络（声道形状），
 * 这两项**正是 log-mel 特征要测的量**，也与被替换掉的旧特征
 * （时域幅度包络）**正交** —— 所以这套夹具能真正区分两种特征：
 * 只要有人把特征改回时域包络，下面的判据立刻转红。
 */

/** mel 滤波器组覆盖的上限，合成时不要往上放能量（会被带外截断）。 */
const MAX_HZ = 7600

/** 幅度包络：有音节、有短语起伏。**故意与音色无关** —— 这正是旧特征的盲区。 */
function envelope(n: number, syllables: number, seed: number): Float32Array {
  const out = new Float32Array(n)
  for (let i = 0; i < n; i++) {
    const p = i / n
    const syl = Math.max(0, Math.sin(Math.PI * (p * syllables + seed * 0.13)))
    const phrase = 0.5 + 0.5 * Math.sin(Math.PI * 2 * (p * 1.3 + seed * 0.07))
    out[i] = syl * phrase
  }
  return out
}

/** 谐波叠加 + 共振峰包络。 */
function voice(n: number, f0: number, formants: number[], gain: number): Float32Array {
  const out = new Float32Array(n)
  for (let i = 0; i < n; i++) {
    const t = i / 16000
    let s = 0
    for (let h = 1; h <= 40; h++) {
      const fh = f0 * h
      if (fh > MAX_HZ) break
      let g = 0.05
      for (const fc of formants) g += 1 / (1 + ((fh - fc) / (fc * 0.35)) ** 2)
      s += (g * Math.sin(2 * Math.PI * fh * t)) / h
    }
    out[i] = s * gain
  }
  return out
}

export interface VoiceSpec {
  /** 基频。 */
  f0: number
  /** 三个共振峰。 */
  formants: number[]
  /** 包络里的音节数（= 说话节奏）。 */
  syllables: number
  /** 改变包络形状用；同一人不同句子应当换它。 */
  seed: number
  /** 整体增益（说话音量）。 */
  gain?: number
}

/** 合成一段「某人说了某句话」的 16k 单声道 PCM。 */
export function synthVoice(spec: VoiceSpec, seconds = 8): Float32Array {
  const n = 16000 * seconds
  const env = envelope(n, spec.syllables, spec.seed)
  const src = voice(n, spec.f0, spec.formants, spec.gain ?? 0.03)
  const out = new Float32Array(n)
  for (let i = 0; i < n; i++) out[i] = env[i] * src[i]
  return out
}

/** 低沉男声。 */
export const MALE: Omit<VoiceSpec, 'syllables' | 'seed'> = {
  f0: 115,
  formants: [520, 1180, 2400],
}
/** 高亢女声。共振峰整体上移 —— 与基频一起构成「音色」。 */
export const FEMALE: Omit<VoiceSpec, 'syllables' | 'seed'> = {
  f0: 215,
  formants: [850, 1900, 2900],
}

/**
 * 叠加均匀白噪（会议现场的环境声）。
 *
 * ⚠ 门禁需要它，是因为**噪声是这套廉价特征的主要敌人**：
 *   实测同一人在 0.001 幅度底噪下相似度就掉到 0.9815，已跌破 0.99 阈值。
 *   确定性伪随机（固定种子的 LCG），保证每次跑出同一个数。
 */
export function withNoise(pcm: Float32Array, amp: number, seed = 7): Float32Array {
  const out = new Float32Array(pcm)
  let s = seed
  for (let i = 0; i < out.length; i++) {
    s = (s * 1103515245 + 12345) & 0x7fffffff
    out[i] += ((s / 0x7fffffff) * 2 - 1) * amp
  }
  return out
}
