/** 邮件正文语言切换：原文即时回切，其它语言走 AI 并缓存。 */
export type EmailLang =
  | 'original'
  | 'zh-CN'
  | 'zh-TW'
  | 'en-US'
  | 'ja-JP'
  | 'ko-KR'

export interface EmailLangOption {
  code: EmailLang
  label: string
  short: string
}

export const EMAIL_LANGS: EmailLangOption[] = [
  { code: 'original', label: '原文', short: '原文' },
  { code: 'zh-CN', label: '简体中文', short: '中' },
  { code: 'zh-TW', label: '繁體中文', short: '繁' },
  { code: 'en-US', label: 'English', short: 'EN' },
  { code: 'ja-JP', label: '日本語', short: '日' },
  { code: 'ko-KR', label: '한국어', short: '한' },
]

/**
 * 需求：「增加翻译成指定语言的功能，默认为中文」。
 * 默认目标语言就是简体中文；正文已是中文时不翻（见 isMostlyChinese）。
 */
export const DEFAULT_EMAIL_LANG: EmailLang = 'zh-CN'

/**
 * 判断正文是否已以中文为主。
 *
 * 为什么要判：默认语言是中文，若正文本来就是中文，再送去「翻译成中文」既浪费
 * token，又可能让 LLM 顺手改写措辞（用户会看到自己写的中文被「翻译」了一遍）。
 *
 * 阈值取 0.15 而不是「含任意一个汉字」：技术邮件常中英混排，含几个汉字不代表
 * 主要语言是中文；反过来纯英文通知里带一个产品中文名也不该触发翻译。
 *
 * **必须先排除日文/韩文**：日语大量使用汉字（「請求書」「発行」），单看汉字占比
 * 会把日文正文判成中文，于是「翻译成中文」变成把日文改写成中文——恰好是用户
 * 最不想要的结果。假名（ひらがな/カタカナ）或谚文（한글）一旦出现就足以定性。
 */
export function isMostlyChinese(text: string, threshold = 0.15): boolean {
  if (!text) return false
  // 去掉 HTML 标签与常见实体，避免标签名/属性里的字符干扰统计。
  const plain = text
    .replace(/<[^>]*>/g, ' ')
    .replace(/&[a-z]+;|&#\d+;/gi, ' ')
  // 假名 / 谚文 / 谚文 → 该正文不是中文。
  if (/[\u3040-\u30ff\uac00-\ud7af]/.test(plain)) return false
  const han = (plain.match(/[\u4e00-\u9fff]/g) || []).length
  if (han === 0) return false
  // 用「非空白字符数」做分母，忽略排版空白。
  const letters = (plain.match(/[\p{L}\p{N}]/gu) || []).length
  if (letters === 0) return false
  return han / letters >= threshold
}

export function langShortLabel(code: EmailLang): string {
  return EMAIL_LANGS.find((l) => l.code === code)?.short ?? '原文'
}

export function langFullLabel(code: EmailLang): string {
  return EMAIL_LANGS.find((l) => l.code === code)?.label ?? '原文'
}

// ---------------------------------------------------------------------------
// 源语识别
//
// 为什么需要它：此前的策略只有「是不是中文」这一个二值判断，落到实际
// 行为上有三个洞：
//   1. 目标语言选 English 时，中文正文被直接判定「无需翻译」——用户明确
//      点了 English，却看到原文。判断依据和用户意图被绑死了。
//   2. 日文正文走「非中文 → 翻译」是对的，但无法区分目标语，提示词里
//      写不出「这是日语，请勿回译成日语」这类约束。
//   3. 中英混排的技术邮件（占比 0.16）卡在阈值边缘时行为不稳定——
//      同一封信可能这次翻、下次不翻。
//
// 改成「先识别源语，再与目标比较」：源语 == 目标才跳过翻译。
// ---------------------------------------------------------------------------

export type DetectedSource = 'zh' | 'ja' | 'ko' | 'en' | 'other'

/** 各类文字的 Unicode 区段。 */
const RE_KANA = /[\u3040-\u30ff]/
const RE_HANGUL = /[\uac00-\ud7af]/
const RE_HAN = /[\u4e00-\u9fff]/
const RE_LATIN = /[A-Za-z]/
/** 泰文/天城文/阿拉伯文等：出现即判定为「非上述语种」。 */
const RE_OTHER_SCRIPT = /[\u0e00-\u0e7f\u0900-\u097f\u0600-\u06ff\u0400-\u04ff]/

/**
 * 识别正文主语言。
 *
 * 判定顺序有讲究：
 *  - 先看「独占语种」字符（假名/谚文/其它文字）。日文与中文共用汉字，
 *    只有假名能把两者分开；**必须**先查，否则「請求書を発行しました」会被
 *    判成中文，于是「翻译成中文」变成把日文改写成中文——用户最不想要的。
 *  - 再比汉字与拉丁字母的字数占比。技术邮件常中英混排，汉字少不代表中文。
 */
export function detectSourceLang(text: string): DetectedSource {
  const plain = (text || '')
    .replace(/<[^>]*>/g, ' ')
    .replace(/&[a-z]+;|&#\d+;/gi, ' ')
  if (!plain.trim()) return 'other'
  if (RE_OTHER_SCRIPT.test(plain)) return 'other'
  if (RE_HANGUL.test(plain)) return 'ko'
  // 有假名即日语；纯汉字（无假名）按中文处理。
  if (RE_KANA.test(plain)) return 'ja'

  const han = (plain.match(/[\u4e00-\u9fff]/g) || []).length
  const latin = (plain.match(/[A-Za-z]/g) || []).length
  if (han === 0) return latin > 0 ? 'en' : 'other'
  if (latin === 0) return 'zh'
  return han >= latin ? 'zh' : 'en'
}

/** EmailLang → 文字体系，用于与 detectSourceLang 的结果比较。 */
const LANG_TO_SCRIPT: Record<EmailLang, DetectedSource> = {
  original: 'other',
  'zh-CN': 'zh',
  'zh-TW': 'zh',
  'en-US': 'en',
  'ja-JP': 'ja',
  'ko-KR': 'ko',
}

/**
 * 是否需要翻译：源语与目标语不同才翻。
 *
 * 简繁中文视作同一语系（`zh-TW` 目标遇到简体原文仍算「需翻译」，
 * 但实际差异极小，翻一次成本可接受）；原文目标永不翻译。
 */
export function shouldTranslate(text: string, target: EmailLang): boolean {
  if (target === 'original' || !text.trim()) return false
  const src = detectSourceLang(text)
  const dst = LANG_TO_SCRIPT[target]
  if (src === 'other') return true // 认不出来就翻，让模型自己判断
  return src !== dst
}


export function buildTranslatePrompt(body: string, target: EmailLang): string {
  const name = langFullLabel(target)
  return [
    `请把下面这封邮件翻译成${name}。`,
    '必须保留原有格式：换行、段落、列表、引用，以及 HTML 标签与属性（如有）。',
    '规则：',
    '1. <img> 等标签的 src 必须原样保留，不要改写、不要删除。',
    '2. 不要翻译人名、公司名、订单号、金额、日期与 URL。',
    '3. 不要添加说明、标题或代码围栏，只输出译文本身。',
    '4. 文中的 {{P0}}、{{P1}} 是被保护的原文片段，**原样输出这些占位符**，',
    '   不要翻译它们的内容，也不要改写编号。',
    '',
    body,
  ].join('\n')
}

export function extractTranslatedBody(raw: string): string {
  let text = raw.replace(/\r\n/g, '\n').trim()
  const fenced = text.match(/^```[a-zA-Z0-9]*\n([\s\S]*?)\n```$/)
  if (fenced) text = fenced[1].trim()
  text = text.replace(/^如下是译文[：:]\s*\n+/i, '').trim()
  text = text.replace(/\n+（已保留格式）\s*$/i, '').trim()
  return text
}

// ---------------------------------------------------------------------------
// 占位符保护（2026-10-01 需求：强化翻译能力）
//
// 为什么要保护：LLM 翻译邮件正文时最常见的破坏不是「翻错」，而是**顺手改写**
// 了不该动的东西——把订单号 A-12345 写成 A-l2345、把金额 $1,299 写成
// $1.299、把 tracking URL 的某个参数换掉。这些在通知/账单类邮件里是致命的，
// 因为用户就是照着它去找订单或点链接。
//
// 做法：翻译前把这些片段抽成 {{P0}} 之类的短占位符，翻译后原样填回。
// 占位符要选**模型不容易改写**的形态：短、全大写、无语义、纯 ASCII。
// ---------------------------------------------------------------------------

export interface ProtectedDoc {
  /** 替换后的正文，可直接发给模型。 */
  text: string
  /** 按索引还原片段。 */
  slots: string[]
}

/**
 * 需要保护且**绝不翻译**的片段模式。
 *
 * 顺序有讲究：
 *  - **HTML 标签必须最先抽**。否则 `<a href="https://x.com">` 里的 URL 会先被
 *    URL 规则吃掉，标签就变成 `<a href="{{P0}}">`——标签名与属性结构仍暴露在
 *    模型眼前，模型照样可能改写 class/id。先抽标签，属性整体就安全了。
 *  - URL 紧跟其后：标签已抽走，剩下的是正文里的裸链接。
 */
const PROTECT_PATTERNS: RegExp[] = [
  // HTML 标签整体（含属性）：结构与 src/href 都封在一个占位符里。
  /<\/?[a-z][a-z0-9-]*(?:\s+[^<>]*?)?\/?>/gi,
  // http(s) 与裸 www
  /https?:\/\/[^\s<>"')\]]+/gi,
  // cid: 内联图（HTML 邮件里 src="cid:xxx"）
  /cid:[^\s<>"')\]]+/gi,
  // data: 内联图片
  /data:image\/[^\s<>"')\]]+/gi,
  // HTML 实体
  /&(?:nbsp|amp|lt|gt|quot|#\d+|#[xX][0-9a-fA-F]+);/g,
  // 邮件地址
  /\b[\w.+-]+@[\w-]+\.[\w.-]+\b/g,
  // 日期时间：日期与时刻整体成一个片段，避免被拆成两半各自占位
  // （拆开会让模型看到「{{P0}} {{P1}}」这种可被调整的组合）。
  /\b\d{4}[-/年]\d{1,2}[-/月]\d{1,2}日?(?:\s+\d{1,2}:\d{2}(?::\d{2})?)?/g,
  // 金额（带货币符号或 ¥/￥/€/$/£ 前缀）
  /[¥￥$€£]\s?\d[\d,.]*/g,
  // 独立时刻
  /\b\d{1,2}:\d{2}(?::\d{2})?\s*(?:AM|PM|am|pm)?/g,
  // 电话/区号：必须排在「订单号」之前。
  //
  // 顺序反了会出事：`400-820-8820` 先被订单号规则匹配成 `400-820`（吃掉前两段），
  // 只剩 `8820` 变成占位符 —— 于是模型看到的是「客服电话 400-820-{{P0}}」，
  // 前半截号码仍然暴露、且整串被拆散，正是最不能拆的那类内容。
  /(?:\+?\d{1,3}[-.\s]?)?(?:\(\d{2,4}\)|\d{2,4})[-.\s]\d{3,4}[-.\s]\d{3,4}/g,
  // 订单号/发票号/快递单号：字母数字混合且含分隔符
  /\b[A-Z]{1,6}[-#]?\d{3,}[-_A-Z0-9]*\b/g,
  // 版本号（必须在纯数字长串之前，否则 1.2.3 会被拆成 1 / 2 / 3）
  /\bv?\d+\.\d+(?:\.\d+)+(?:[-+][\w.]+)?\b/gi,
  // 纯数字长串（电话、区号、卡号后四位）
  /\b\d{4,}\b/g,
]

/**
 * 把不可翻译片段替换成占位符。
 *
 * 相同片段**复用同一个占位符**（去重）：营销邮件里同一个 tracking URL /
 * 客服电话可能重复十几次，逐个占位会把提示词撑大好几倍，而这几处本来就
 * 必须是同一个值。
 */
export function protectSegments(input: string): ProtectedDoc {
  const slots: string[] = []
  const index = new Map<string, number>()
  let text = input
  for (const re of PROTECT_PATTERNS) {
    text = text.replace(re, (whole) => {
      const reused = index.get(whole)
      if (reused !== undefined) return `{{P${reused}}}`
      const id = slots.length
      slots.push(whole)
      index.set(whole, id)
      return `{{P${id}}}`
    })
  }
  return { text, slots }
}

/** 把占位符还原成原文片段。模型漏掉/改写占位符时不至于毁掉正文。 */
export function restoreSegments(input: string, slots: string[]): string {
  let out = input
  slots.forEach((seg, i) => {
    // 全局替换：同一片段在正文里出现多次时都能还原。
    out = out.split(`{{P${i}}}`).join(seg)
  })
  return out
}

/**
 * 带占位符保护的翻译。
 *
 * 流程：保护 → 交给模型 → 剥壳 → 还原。
 * 还原放在剥壳之后：模型有时会把译文包在 ```html 围栏里，
 * 围栏里出现的占位符同样需要还原。
 */
export async function translateWithProtection(
  body: string,
  target: EmailLang,
  chat: (prompt: string) => Promise<string>,
): Promise<string> {
  const { text, slots } = protectSegments(body)
  const raw = await chat(buildTranslatePrompt(text, target))
  return restoreSegments(extractTranslatedBody(raw), slots)
}

// ---------------------------------------------------------------------------
// 长正文分块
//
// 为什么不整封一次丢：模型有上下文上限，超了会**从中间截断**（表现为一封
// 看起来「翻译到一半没了」的邮件，比不翻更糟）。且整封一次也让失败粒度太大。
//
// 分块策略：优先按空行切段（段落是天然的语义边界，不会把句子劈开），
// 再按硬上限兜底。单块超过硬上限时按行切。
// ---------------------------------------------------------------------------

/** 单块软上限：超过就开新块（按字符数粗略估算 token）。 */
export const TRANSLATE_CHUNK_CHARS = 4000
/** 单块硬上限：超过必须切，否则模型大概率截断。 */
export const TRANSLATE_MAX_CHARS = 6000

/**
 * 把正文切成适合逐块翻译的片段。
 *
 * 保留分隔符（`\n\n`）并让**每块都以分隔符开头**、除首块外——
 * 这样拼回去时块与块之间的段落间隔天然正确，不需要额外补换行。
 */
export function splitForTranslation(body: string): string[] {
  const src = (body || '').replace(/\r\n/g, '\n')
  if (!src.trim()) return []
  if (src.length <= TRANSLATE_CHUNK_CHARS) return [src]

  const out: string[] = []
  let current = ''
  const flush = () => {
    if (current) out.push(current)
    current = ''
  }

  for (const para of src.split(/(?<=\n\n)/)) {
    // 单段就超硬上限：按行硬切（句子被劈开也比整段丢失好）。
    if (para.length > TRANSLATE_MAX_CHARS) {
      flush()
      for (const line of para.match(/[\s\S]{1,4000}/g) || []) out.push(line)
      continue
    }
    if ((current + para).length > TRANSLATE_CHUNK_CHARS) flush()
    current += para
  }
  flush()
  return out.filter((c) => c.trim())
}

/** 同时跑多个翻译任务，最多 `limit` 个并发。 */
async function mapWithConcurrency<T, R>(
  items: T[],
  limit: number,
  worker: (item: T, index: number) => Promise<R>,
): Promise<R[]> {
  const results: R[] = new Array(items.length)
  let cursor = 0
  const runners = Array.from({ length: Math.min(limit, items.length) }, async () => {
    for (;;) {
      const i = cursor++
      if (i >= items.length) return
      results[i] = await worker(items[i], i)
    }
  })
  await Promise.all(runners)
  return results
}

/**
 * 完整的邮件正文翻译管线（分块 + 并发 + 占位符保护 + 失败降级）。
 *
 * 失败降级是**逐块**的：某一块翻译失败（超时/网络）时保留该块原文，
 * 其余块照常出译文。一块坏掉不该让整封退回原文。
 */
export async function translateBodyPipeline(
  body: string,
  target: EmailLang,
  chat: (prompt: string) => Promise<string>,
  opts: { concurrency?: number } = {},
): Promise<string> {
  if (target === 'original' || !body.trim()) return body
  const chunks = splitForTranslation(body)
  // 单块与多块走**同一条降级路径**：整封只有一个块时，翻译失败同样应该
  // 退回原文而不是把异常抛给调用方。否则调用方（详情页）必须为
  // 「只有一段的短邮件」再写一份 try/catch，而多段邮件已经静默降级了——
  // 两条路径行为不一致，正是这类边角最容易漏的地方。
  const runChunk = async (chunk: string): Promise<string> => {
    try {
      return await translateWithProtection(chunk, target, chat)
    } catch {
      // 单块失败：退回该块原文。
      return chunk
    }
  }
  if (chunks.length <= 1) return runChunk(chunks[0] ?? body)

  const parts = await mapWithConcurrency(chunks, opts.concurrency ?? 2, runChunk)
  return parts.join('')
}


export function resolveDisplayBody(
  original: string,
  cache: Record<string, string>,
  lang: EmailLang,
): string {
  if (lang === 'original') return original
  return cache[lang] || original
}

export async function translateEmailBody(
  body: string,
  target: EmailLang,
  chat: (prompt: string) => Promise<string>,
): Promise<string> {
  if (target === 'original' || !body.trim()) return body
  const raw = await chat(buildTranslatePrompt(body, target))
  return extractTranslatedBody(raw) || body
}
