/**
 * 详情页分阶段加载策略（2026-10-01 真机反馈：点进详情失败/极慢）。
 *
 * 问题不在某一次请求慢，而在**闸门位置**：原实现用一个 `loading` 罩住整个页面，
 * 且 `load()` 是完全串行的 await 链（正文网络 → LLM 翻译 → 远程图预加载）。
 * 三段里任意一段慢，用户看到的都是整屏「加载中…」，连发件人和主题都看不到。
 *
 * 这里把加载拆成四个阶段，并明确每一阶段**挡不挡首屏**：
 *
 *   P1 外壳   —— 本地邮件记录（发件人/主题/摘要/已读态）。挡住，且必须极快。
 *   P2 正文   —— 本地缓存优先；命中就立刻渲染。网络刷新**不挡**首屏。
 *   P3 翻译   —— 调 LLM。**永不挡**首屏：先出原文，译文到了再替换。
 *   P4 图片   —— 预加载远程图。**永不挡**首屏：先出无图版，图就位后替换。
 *
 * 「永不挡」是硬要求：翻译和图片都是锦上添花，让它们挡住用户看信是本末倒置。
 */

/** 详情页加载阶段。P1 之后的内容都允许后到。 */
export type DetailPhase = 'shell' | 'body' | 'translate' | 'images'

export interface DetailRenderState {
  /** 是否仍处于「整屏加载中」。只由 P1（本地记录）决定。 */
  blocking: boolean
  /** 正文区是否可展示（已解析出可用正文，或已确定无正文）。 */
  bodyReady: boolean
  /** 是否正在后台拉正文。 */
  bodyLoading: boolean
  /** 是否正在后台翻译。 */
  translating: boolean
  /** 是否正在后台预加载图片。 */
  preloadingImages: boolean
}

export function initialDetailState(): DetailRenderState {
  return {
    blocking: true,
    bodyReady: false,
    bodyLoading: false,
    translating: false,
    preloadingImages: false,
  }
}

/**
 * 决定「整屏 loading」是否应当继续遮挡。
 *
 * 只有 P1（本地库里的邮件记录）没就绪时才遮挡。一旦拿到记录就应当立即渲染
 * 页面骨架——即使用户随后还要等网络正文，他至少已经看到「谁发的、什么主题」。
 */
export function shouldBlockScreen(params: {
  hasLocalRecord: boolean
  failed: boolean
}): boolean {
  if (params.failed) return false
  return !params.hasLocalRecord
}

/**
 * 正文是否已经可以展示。
 *
 * 有缓存正文 / 拿到远端正文 / 明确无正文（purged 或 snippet 兜底）都算就绪。
 * 特别注意：**「有 snippet」也算就绪**——列表页本来就带着 snippet，用它先顶上，
 * 比让正文区空着强得多。
 */
export function isBodyReady(params: {
  bodyText: string
  hasSnippet: boolean
  bodyPurged: boolean
  bodyFailed: boolean
}): boolean {
  if (params.bodyText.trim()) return true
  if (params.bodyPurged) return true
  // 拉取失败但有 snippet：仍然可展示，别让用户盯着空白。
  if (params.bodyFailed && params.hasSnippet) return true
  return false
}

/**
 * 是否应该**现在**发起远程正文请求。
 *
 * 已经拿到缓存正文时仍可刷新（保证内容新），但**不得阻塞**任何渲染。
 * 该函数的返回值只用于决定「要不要发」，不参与闸门。
 */
export function shouldFetchRemoteBody(params: {
  hasCache: boolean
  bodyPurged: boolean
  alreadyFetching: boolean
}): boolean {
  if (params.bodyPurged) return false
  if (params.alreadyFetching) return false
  return true
}

/**
 * 翻译是否应自动触发。
 *
 * 默认语言是中文，但**已经是中文的正文不翻**（见 isMostlyChinese）——既省 token，
 * 也避免用户看到自己写的中文被「翻译」一遍。纯文本占位、已清除正文不翻。
 */
export function shouldAutoTranslate(params: {
  hasBody: boolean
  mostlyChinese: boolean
  alreadyCached: boolean
  bodyPurged: boolean
}): boolean {
  if (params.bodyPurged) return false
  if (!params.hasBody) return false
  if (params.mostlyChinese) return false
  return !params.alreadyCached
}
