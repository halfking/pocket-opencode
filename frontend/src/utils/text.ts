// text.ts — 纯文本工具。**中立层**：不 import 任何业务模块，
// 因此 `src/api/*` 与 `src/features/*` 两侧都能引用而不构成循环依赖。
//
// ⚠ 为什么不放在 `src/api/meetings.ts`（helper 的第一版落点）：
//   `api/meetings.ts` 已经 import 了 `features/meetings/*`（renderTranscript 等），
//   而 `features/meetings/refined-ingest-writeback.ts` 又需要这个 helper
//   ⇒ 放 api 层会让 features 层反向 import api 层，**成环**。
//   ⇒ 这就是「同一段逻辑该放哪」的判别动作：
//   **共用代码放到所有使用者的下方，而不是某个使用者旁边。**

/**
 * 按码元截断到最多 n 个，但**回退到完整字符边界**。
 *
 * ⚠ 为什么必须回退（§245.2 实测）：`slice` 只看码元，不看字符。
 *   星平面字符（emoji 等）占 **2 个码元**，切在中间会留下一个**落单代理对**。
 *   它不会立刻报错 —— 现代 V8 的 `JSON.stringify` 把它转义成 `\uD83D`，
 *   往返也不丢 —— 但**过到 Go 侧会被静默替换成 U+FFFD，且 `err == nil`**：
 *   实测 `json.Unmarshal([]byte(`{"summary":"进\uD83D"}`))` 产出 `"进\uFFFD"`，
 *   没有任何一层会报错，只是内容静默变脏。
 *   而回退最多丢 1 个码元，代价可以忽略。
 *
 * 与 Go 侧 `server_meeting.go` 的 `truncateStr` 同形 ——
 *   那边早就有这条回退（它甚至是为了躲 PostgreSQL 的
 *   `invalid byte sequence for encoding "UTF8"` 而写的，见该函数注释）。
 *
 * ⚠ 本函数**不做**字节/码元的单位对齐（那是 §245.7 的产品决定，未拍板）。
 *   它只解决「切在字符中间」这一类静默损坏，长度口径保持现状。
 *
 * ⚠ 只对**尾端**生效：`slice` 只可能在前 n 个码元处切一次，
 *   所以末尾那一个落单高位代理对就是全部风险。
 */
export function truncateCodeUnits(s: string, n: number): string {
  const cut = s.slice(0, n)
  return /[\uD800-\uDBFF]$/.test(cut) ? cut.slice(0, -1) : cut
}

/**
 * §916：字符串的 **UTF-8 字节长度** —— 与 Go 的 `len(string)` 同一口径。
 *
 * ⚠⚠ **为什么必须按字节而不是按字符**（这是 §916 的坑本体）：
 *   服务端 `/api/llm/chat` 的长度闸写的是
 *     `if len(m.Content) > 32000 { 400 "message too long (max 32000 bytes per message)" }`
 *   （`backend/internal/server/server_assistant.go:2948`）
 *   —— Go 的 `len()` 对 string 返回的是**字节数**。
 *   ⇒ 实测：`len("中") == 3`（Go 跑出来的，见 §916 文档），
 *     32000 **字节**只装得下 **10666 个中文字** ⇒ 按「字符」算的实现会超闸 3 倍。
 *   ⇒ ⚠ 错误信息原先写的是「chars」—— **信息与判定不同形**，
 *     正是这个错单位把「按字符修」的实现放过去的 ⇒ 已按实测把文案改成 bytes。
 *
 * ⚠ 用 `TextEncoder` 而不是手写字节累加：`TextEncoder` 是**标准**，
 *   落单代理对按 U+FFFD（3 字节）编码，与 Go `json.Unmarshal` 的替换行为一致；
 *   手写一份必然带来「复刻与真值漂移」的老问题（§244 那条门的老教训）。
 */
const _utf8Encoder = new TextEncoder()

export function utf8ByteLength(s: string): number {
  return _utf8Encoder.encode(s).length
}

/**
 * §916：从**尾部**保留最多 maxBytes 个 **UTF-8 字节**，且切点必在字符边界。
 *
 * ⚠ 为什么需要「尾部」这一版：`truncateCodeUnits` 只能取头部，
 *   而**滚动摘要**的输入里，新的内容在**尾部**（会议还没讲完）⇒
 *   只取头部会让兜底每次都在总结会议开头，永远看不到新说的话。
 *
 * ⚠⚠ **单位是字节，不是码元** —— 与 `truncateCodeUnits` **不同**，
 *   这是故意的：这里的对手是**服务端的字节闸**（`len(m.Content)`），
 *   不是 §245.7 那个尚未拍板的「字节/码元/字符」产品口径。
 *   ⇒ 两处并存不是同一件事的两种写法，注释各自写清自己在对谁。
 *
 * ⚠ 切点按**码点**走（不是码元）⇒ 不会留下落单代理对
 *   （§245.2 的静默变脏：落单代理对过 JSON 到 Go 会被换成 U+FFFD 且 err==nil）。
 */
export function truncateUtf8BytesTail(s: string, maxBytes: number): string {
  if (maxBytes <= 0) return ''
  let bytes = 0
  let end = s.length
  while (end > 0) {
    let start = end - 1
    const c = s.charCodeAt(start)
    // 低代理前面若跟着高代理 ⇒ 这俩是一个码点（星平面字符）⇒ 一起取走
    if (c >= 0xdc00 && c <= 0xdfff && start > 0) {
      const p = s.charCodeAt(start - 1)
      if (p >= 0xd800 && p <= 0xdbff) start -= 1
    }
    const cp = s.codePointAt(start) as number
    const width = cp < 0x80 ? 1 : cp < 0x800 ? 2 : cp < 0x10000 ? 3 : 4
    if (bytes + width > maxBytes) break
    bytes += width
    end = start
  }
  return s.slice(end)
}