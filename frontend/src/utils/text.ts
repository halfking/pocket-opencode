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