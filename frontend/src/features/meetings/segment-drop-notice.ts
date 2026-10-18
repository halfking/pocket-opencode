/**
 * segment-drop-notice.ts — 「**这一段没有转出文字**」的诊断文案（纯函数，无副作用）。
 *
 * ── 为什么它是缺陷（§828）──
 *   `ingestSpeechBlob` 里有一句 `if (!sttResult.text.trim()) return null`。
 *   它本身是对的（空文本不该建一条空分段），但它此前**什么都不说**：
 *   既不建行、也不计数、也不出声。调用点 `processSegment` 只在**抛错**时才会
 *   `sttError`/warn ⇒ 「转写成功但没出字」这条路径连一个字的痕迹都没有。
 *
 *   ⇒ 用户那句话**从会议记录里消失了**，而界面上没有任何东西提示它曾经存在过。
 *   ⚠ 旧顺序下更糟：早于这句 return 的 `saveAudioPart` 也被跳过了
 *   ⇒ 连**音频**也没存 ⇒ 这段话再也无法事后补救（§826：Android 上没有整段音频兜底）。
 *
 * ⚠ 抽成纯模块而不是写在调用点，是为了**能测**：`ingest-speech.ts` 的顶层 import
 * 会拖进 `api/stt`、`speaker-embedding`、`audio-parts`（浏览器/原生/HTTP 代码），
 * 在 node 里 `import` 它直接 ERR_MODULE_NOT_FOUND（§52 的老问题）。
 * 而这条文案如果只贴在调用点，判据只能去扫 `console.warn` 的语法形状 ——
 * 把它改成 `void 0 && console.warn(...)` 照样全绿（§818 实测过的零牙）。
 */

/** 引擎标识：本地 sherpa 还是云端网关（只影响文案里的排查方向，不影响结论）。 */
export type SegmentEngine = 'local' | 'cloud' | string

export interface SegmentDropInput {
  /** 本次会议内的分片序号（VAD 取片顺序）。 */
  seq: number
  startMs: number
  endMs: number
  /** 实际用的转写引擎；拿不到就不写这一段。 */
  engine?: SegmentEngine
}

/**
 * 生成一条诊断文案。
 *
 * 刻意包含三样东西，缺一不可：
 *  · **是哪一段**（seq + 时间范围）—— 否则排查的人不知道丢的是哪句话；
 *  · **丢的是什么**（这一段音频）—— 否则读者以为只是文本为空；
 *  · **还能不能找回**（音频分片已落盘，可事后重转）—— 否则读者会以为这段彻底没了。
 */
export function describeSegmentDrop(input: SegmentDropInput): string {
  const seq = Number.isFinite(input.seq) ? input.seq : 0
  const start = Number.isFinite(input.startMs) ? Math.round(input.startMs) : 0
  const end = Number.isFinite(input.endMs) ? Math.round(input.endMs) : start
  const engine = input.engine ? `（引擎 ${input.engine}）` : ''
  return `[ingest-speech] 第 ${seq} 段${engine}（${start}-${end}ms）转写没有出文字：`
    + '这一段的文字没有进会议记录（不会建空分段），'
    + '但音频分片已落盘，可事后重转补回。'
    + '⚠ 若它反复出现，先查转写引擎与该段音质，而不是查存储。'
}
