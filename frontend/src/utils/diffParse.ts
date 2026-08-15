/**
 * diffParse.ts — unified diff 行级解析（纯函数，Node 可测，无运行时依赖）。
 *
 * P2（09 篇 E5-S3）：会话工具输出里的 diff 不再整段塞进 <pre>，而是解析成
 * hunk 列表交给 DiffBlock 分段渲染；大输入（5,000 行级）下除首个 hunks 外
 * 默认折叠，保持 DOM 规模可控。
 *
 * 解析器只认 git unified diff 的可靠信号（@@ hunk 头），不尝试理解文件头。
 */

export type DiffLineType = 'context' | 'add' | 'del'

export interface DiffLine {
  type: DiffLineType
  /** 去掉前缀标记（+/-/空格）后的原始行内容。 */
  text: string
}

export interface DiffHunk {
  /** 多文件 diff 中，第二个及后续文件的文件头绑定到它的首个 hunk。 */
  fileMeta: string[]
  /** 完整 hunk 头，如 "@@ -12,5 +12,7 @@ optional context"。 */
  header: string
  oldStart: number
  newStart: number
  lines: DiffLine[]
  adds: number
  dels: number
}

export interface ParsedDiff {
  /** hunk 头之前的文件级元信息行（diff --git / --- / +++ / index 等）。 */
  meta: string[]
  hunks: DiffHunk[]
  /** 全部 hunk 行数（不含 meta 与 hunk 头）。 */
  totalLines: number
  adds: number
  dels: number
}

// m 标志：既能逐行 exec，也能对整段文本做行首匹配（looksLikeUnifiedDiff）。
const HUNK_RE = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/m

// 只有 "diff --git" 能可靠标记多文件边界。---/+++ 也可能是 hunk 内真实
// 内容（例如删除行原文以 -- 开头），不能单独作为边界。
const FILE_BOUNDARY_RE = /^diff --git /

/** 判断文本是否包含 unified diff（以 hunk 头为信号，避免误判普通文本）。 */
export function looksLikeUnifiedDiff(text: string): boolean {
  if (!text) return false
  const limit = Math.min(text.length, 65_536)
  return HUNK_RE.test(text.slice(0, limit))
}

/**
 * 从工具输出（字符串或 JSON 对象）中提取 diff 文本。
 * 对象时浅扫常见字段（diff/patch/content/output/text），命中即返回。
 */
export function extractDiffText(output: unknown): string | null {
  if (typeof output === 'string') {
    return looksLikeUnifiedDiff(output) ? output : null
  }
  if (output && typeof output === 'object') {
    const obj = output as Record<string, unknown>
    for (const key of ['diff', 'patch', 'content', 'output', 'text']) {
      const v = obj[key]
      if (typeof v === 'string' && looksLikeUnifiedDiff(v)) return v
    }
  }
  return null
}

/**
 * 解析 unified diff。不含 hunk 头时返回 null（调用方回退到 JSON 渲染）。
 */
export function parseUnifiedDiff(text: string): ParsedDiff | null {
  if (!looksLikeUnifiedDiff(text)) return null

  const meta: string[] = []
  const hunks: DiffHunk[] = []
  let current: DiffHunk | null = null
  let totalLines = 0
  let adds = 0
  let dels = 0
  let inHunks = false
  let pendingFileMeta: string[] = []

  for (let raw of text.split('\n')) {
    // Windows CRLF diff：剥掉行尾 \r，避免 pre 渲染行距异常与复制脏字符。
    raw = raw.replace(/\r$/, '')
    const m = HUNK_RE.exec(raw)
    if (m) {
      inHunks = true
      current = {
        fileMeta: pendingFileMeta,
        header: raw,
        oldStart: Number(m[1]),
        newStart: Number(m[3]),
        lines: [],
        adds: 0,
        dels: 0,
      }
      pendingFileMeta = []
      hunks.push(current)
      continue
    }
    if (!inHunks) {
      meta.push(raw)
      continue
    }
    if (FILE_BOUNDARY_RE.test(raw)) {
      // 文件边界：后续文件头绑定到下一个 hunk，保持文件归属。
      current = null
      pendingFileMeta = [raw]
      continue
    }
    if (current === null) {
      pendingFileMeta.push(raw)
      continue
    }
    if (raw.startsWith('+')) {
      current.lines.push({ type: 'add', text: raw.slice(1) })
      current.adds++
      adds++
      totalLines++
    } else if (raw.startsWith('-')) {
      current.lines.push({ type: 'del', text: raw.slice(1) })
      current.dels++
      dels++
      totalLines++
    } else if (raw.startsWith(' ')) {
      current.lines.push({ type: 'context', text: raw.slice(1) })
      totalLines++
    } else if (raw.startsWith('\\')) {
      // "\ No newline at end of file" — 不计入行数。
    } else {
      current.lines.push({ type: 'context', text: raw })
      totalLines++
    }
  }

  return { meta, hunks, totalLines, adds, dels }
}
