// append-handoff-4.mjs — 追加 §4.18：BUG-Q / BUG-S 与可达性审计方法论。
import { readFileSync, writeFileSync } from 'node:fs'

const P = 'docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md'
let s = readFileSync(P, 'utf8')
if (s.includes('4.18 BUG-Q')) { console.log('已含 4.18，跳过'); process.exit(0) }

const SECTION = `

---

## 4.18 BUG-Q / BUG-S：把「可达性」做成两道可复跑的审计（2026-09-30 12:35-13:20）

BUG-P 之后没有停在「修好闪卡入口」，而是顺着「可达性」这条线做了全量审计，
又挖出两个缺陷，并沉淀成两道可重复执行的检查。

### 4.18.1 BUG-Q：「定时自动化」入口指向不存在的路由

静态对账时一眼看到：\`MoreHubView.vue\` 里写的是 \`/scheduled-tasks\`，
而 \`router-mobile.ts\` 里只有 \`/settings/scheduled-tasks\`。

这比 BUG-P 更糟：
- BUG-P：**少**一个入口（闪卡没入口）
- BUG-Q：**有**一个入口指向虚空（点进去是未匹配路由）

两者都不会被「路由表里有 / 接口能通 / 页面能通过 hash 访问」这类验收抓到。

已改。**/finance** 与 **/contacts** 另有入口（/finance 在 SettingsView 里，
/contacts 暂无主入口——只记为遗留，见 §5）。

### 4.18.2 BUG-S：vue-i18n v2 字面量插值残留，整页白屏

\`scripts/audit-route-render.mjs\` 逐条渲染 37 个路由，报出两个空白页：

\`\`\`
BLANK  /flashcards/browser   body="跳到主要内容"   (appHTMLLen=371)
BLANK  /flashcards/stats     body="跳到主要内容"   (appHTMLLen=370)
\`\`\`

\`scripts/cdp-page-diagnose.mjs\` 抓到决定性证据：

\`\`\`
SyntaxError: Not allowed nest placeholder
    at jn (vue-vendor-Dy9o8ARz.js:37:2250)
\`\`\`

**根因**：项目已升级到 vue-i18n v9+，命名插值语法是 \`{count}\`；但这批文案还是
Vue 2 的**字面量**插值 \`{{count}}\`。vue-i18n v9 解析 \`{{count}}\` 时把外层
\`{...}\` 当成一个占位符、内容又是 \`{count}\`，判定为嵌套占位符并抛错。抛错在
**渲染期** -> 组件渲染中断 -> 只剩壳。

**与语言无关**：9 种语言都写成 \`{{count}}\`，切语言救不了。

修了 5 个**已证实崩溃**的键（browser.resultCount / tagsSelected、
stats.reviewsPerDay / lapsesPerDay / retentionHint）。只改 en-US 与 zh-CN ——
这两个块只有这两种语言有翻译，其余 7 种走 \`fallbackLocale: 'en-US'\`，
改 en-US 即覆盖整条 fallback 链。

#### 刻意**没有**全量改那 108 处

\`audit-i18n-placeholder.mjs\` 扫出每语言 12 处、9 语言共 108 处。只修 5 个：

- \`flashcards.edit.clozePlaceholder\` / \`clozeHint\` 里的
  \`{{c1::H₂O::hydrogen dioxide}}\` 是 **Cloze 语法的字面展示**（教用户怎么写
  挖空）。改成 \`{c1::...}\` 会让用户看到错误的语法示例。**必须保留。**
- \`clozeCount\` / \`io.exportOk\` / \`io.importOk\` / \`study.decks.dueShort\`
  同样是 \`{{name}}\` 写法，但所在页面本次没渲染到，**未证实会崩**。
  不按「看起来像 bug」处理 —— 要触发它们得先完成对应操作（Cloze 模式、
  导入导出、学习页）。留作待验证项。

**教训**：扫描器报出的「疑似问题」不等于缺陷。分不清「占位符」与「要展示给用户
看的字面量」时，盲改会把正确的东西改坏。这类必须逐条判断。

### 4.18.3 沉淀：可达性要跑两道互补的检查

| 检查 | 脚本 | 能发现 | 发现不了 |
|---|---|---|---|
| 入口静态对账 | grep MoreHubView 的 to 值 vs 路由表 | **没有入口**（BUG-P）、**入口指向不存在的路径**（BUG-Q） | 路由存在但页面渲染失败 |
| 逐路由渲染验证 | \`audit-route-render.mjs\` | **路由存在但白屏**（BUG-S） | 没有入口（因为 hash 能直接进） |

两者盲区互补，都要做。全绿也不能互相替代。

判据都是「能区分通/不通」的：落地 hash 是否被守卫弹走、body 文本长度、
有无 404 文案、是否卡在 loading。**不断言「页面打开了」**——那是恒真断言。

### 4.18.4 第一个版本失败记录：逐个点入口

最初写的是「进 #/more 逐个点入口」（\`audit-entry-reachability.mjs\`），
跑 150 秒零输出后放弃。原因：CDP 派发指针事件后无法可靠判断点击是否落地，
且没有超时保护，整脚本挂死。

改成逐路由导航（稳定、可重复、失败能精确定位到是哪个路由）后才跑通。
**教训**：驱动方式失败时要换方法，不要在同一个不 work 的方案上调参。脚本留在
仓库里作为「试过但不可行」的记录，避免下一轮重蹈。
`

const marker = '## 5. 已验证 / 未验证'
const i = s.indexOf(marker)
if (i < 0) { console.error('找不到 §5'); process.exit(1) }
s = s.slice(0, i) + SECTION.trimStart() + '\n' + s.slice(i)
writeFileSync(P, s)
console.log(`handoff §4.18 已追加（+${SECTION.length} 字符）`)
