/**
 * voice-draft-update-notice.ts — 随手记「草稿正文回填失败」的判定与文案（纯函数，无副作用）。
 *
 * ── 为什么它是缺陷（§833）──
 *   `NoteRecorderRuntime.runStop()` 在兜底转写之后有一处
 *
 *       try   { await this.voiceDraftSink!.update(draftId, finalText) }
 *       catch { /* 回填失败不该吞掉录音：草稿已经落库了，音频在，只是文字没跟上。 *\/ }
 *
 *   catch 是**完全空的**：不 console、不写 `this.error`、不产出任何信号。
 *   而这条路径恰恰是「正文被补全」的唯一一次机会，回填失败的后果**不是**
 *   「录音丢了」——音频在草稿里好好躺着——而是：
 *
 *   ① 草稿正文**永久停在停止瞬间那一版**（`settledText`）；
 *   ② 紧接着的 `/summarize` 读的是**库里那份正文**（`NoteListView.adoptVoiceDraft`
 *      → `presentVoiceDraft` → `notesApi.summarize(noteId)`），于是**即时摘要
 *      是按不完整的正文生成的**；
 *   ③ 那份摘要**读起来完全正常**（它是好摘要，只是漏了最后几句）⇒ 用户无从分辨。
 *
 *   ⚠ `presentVoiceDraft(note, text)` 收到的 `text` 恰恰**不会**被写回笔记：
 *      全函数只用它做 `if (!text || !text.trim()) return` 这道门槛，
 *      正文一律以 `note.content`（库里那份）为准 ⇒ 界面上补不回来。
 *
 * ⇒ 与 §832 同族但更隐蔽：§832 是「建草稿失败 ⇒ 什么都没留下」，
 *   这里是「留下了一条**看起来完整**的草稿 + 一份**看起来正常**的摘要」。
 *
 * ── 为什么不把这段逻辑留在调用点 ──
 *   `recordingRuntime.ts` 的顶层 import 会拖进浏览器/原生/HTTP 代码，
 *   在 node 里 `import` 直接 ERR_MODULE_NOT_FOUND（§52 的老问题）；
 *   而文案若只贴在 catch 里，判据只能扫 `console.warn` 的语法形状 ——
 *   改成 `void 0 && console.warn(...)` 照样全绿（§818 实测过的零牙）。
 */

/** 回填判据的三个输入（与调用点的局部变量一一对应）。 */
export interface VoiceDraftBackfillInput {
  /** 草稿 id：为空说明草稿压根没建成（那是 §832 的建草稿失败，不是本节）。 */
  draftId: string | null | undefined
  /** 建草稿那一刻的正文（`settledText`）。 */
  settledText: string
  /** 收尾时的最终正文（`finalText`）。 */
  finalText: string
}

/**
 * 这一轮**是否需要**回填正文。
 *
 * 三个条件缺一不可，语义与 §463 那条「整段校对几乎永远为假」的注释是配套的：
 *  · `draftId` 非空 —— 没有草稿就没有可回填的对象；
 *  · `finalText` 非空 —— 回填空串会把正文擦成空白，比不回填更坏；
 *  · `finalText !== settledText` —— 两边一样时回填是无谓的一次写库，
 *    而它一旦失败就会**假造**一条「回填失败」的告警（自己给自己报错）。
 */
export function shouldBackfillVoiceDraft(input: VoiceDraftBackfillInput): boolean {
  const { draftId, finalText } = input
  const settledText = input.settledText
  if (!draftId) return false
  // ⚠ 归一化而非直接 .trim()：本函数是**判据**，必须对任何输入都返回布尔值。
  //   生产里 finalText 来自 `this.transcript.value.trim()`，恒为字符串；
  //   但判据一旦对 undefined 抛 TypeError，它就成了「抛异常而不是给答案」的
  //   半函数 —— 负控用例（§833）第一次跑就撞上了这一点，当场修掉。
  const text = typeof finalText === 'string' ? finalText : ''
  const prev = typeof settledText === 'string' ? settledText : ''
  if (!text.trim()) return false
  return text !== prev
}

export interface VoiceDraftUpdateFailureInput {
  /** catch 到的东西。只用于拼排查线索，不直接怼给用户。 */
  error?: unknown
}

/** 空串 = 不该提示（调用点不该走到这里）。 */
export function describeVoiceDraftUpdateFailure(_input: VoiceDraftUpdateFailureInput = {}): string {
  return '录音的文字没有写回草稿：草稿正文停在按下停止那一刻的版本，'
    + '音频仍然完整地存在这条草稿里，但刚才那段 AI 总结是按不完整的正文生成的。'
    + '请打开这条草稿手动补上最后几句（可对着音频核对），或重录一次。'
}

/** 诊断行的前缀：与 §832 的建草稿失败区分开，便于在控制台里按前缀搜。 */
export const VOICE_DRAFT_UPDATE_FAIL_TAG = '[note-recorder] 草稿正文回填失败（音频已落库，正文未跟上）：'
