// append-handoff-5.mjs — 追加 §4.19：BUG-S 的三层返工与「只提交不改工作区」教训。
import { readFileSync, writeFileSync } from 'node:fs'

const P = 'docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md'
let s = readFileSync(P, 'utf8')
if (s.includes('4.19 BUG-S 的三层返工')) { console.log('已含 4.19，跳过'); process.exit(0) }

const SECTION = `

---

## 4.19 BUG-S：修了两轮才修对，暴露三个方法论级教训（2026-09-30 13:20-14:10）

BUG-S（\`/flashcards/browser\` 与 \`/flashcards/stats\` 整页白屏）**修了两轮，
第一轮完全无效**。失败过程比修复结果更值得记。

### 4.19.1 现象与最终根因

真机逐路由审计报两个空白页，console 有决定性错误：

\`\`\`
SyntaxError: Not allowed nest placeholder
    at jn (vue-vendor-Dy9o8ARz.js:37:2250)
\`\`\`

根因：项目已升级到 vue-i18n v9+，命名插值是 \`{count}\`；这批文案还是 Vue 2 的
**字面量**插值 \`{{count}}\`。vue-i18n 在**替换占位符**时把外层 \`{...}\` 当成
一个占位符、内容又是 \`{count}\`，判定为嵌套并抛错。抛错在渲染期 ->
组件渲染中断 -> 只剩壳。与语言无关（9 种语言都这么写）。

### 4.19.2 教训一：审计脚本必须证明自己有区分能力

第一版 \`audit-i18n-compile.mjs\` 全用**无参** \`t(key)\`，报「9 语言 332 键
全部通过」。

对照实验更致命：用**修复前**的 locales（\`{{count}}\` 原文）跑同一个脚本 ——
同样「0 失败」。说明无参路径根本走不到那段解析。

而真机照旧崩。**一个永远返回 OK 的检查和一个真正有效的检查，在报告上长得
一模一样。** 改法：从 \`{{name}}\` 抠出参数名，按 named 方式传进去。这才稳定
报出每语言 10 个必崩键。

写 \`verify-audit-detects.mjs\` 做注入实验（往临时副本注入同形态缺陷，确认脚本
抓得到；再对未改动的原文件确认不报）。以后新写审计脚本都该配一个。

### 4.19.3 教训二：只提交不改工作区，等于没修

第一版用 \`stage-i18n-fix-brace.mjs\` 把修复写进 **git 索引/HEAD**，但**没动
工作区**：

\`\`\`
HEAD     en-US flashcards.browser.resultCount = "{count} results"    已修
工作区   en-US flashcards.browser.resultCount = "{{count}} results"   未修
\`\`\`

而 \`vite build\` 读的是**工作区**的 locales。于是新 APK 打包进去的还是坏版本，
装机复验 \`appHTMLLen 371 -> 371\`，错误一字不变。

**验证环境的输入永远来自工作区，不是 git。**

这和「\`git commit -- <path>\` 从工作区取内容」是同一类错误的两个面：一个是我
以为在改索引其实改了工作区，一个是我以为在改工作区其实改了索引。现在拆成两个
脚本配套：\`fix-i18n-nested-in-worktree.mjs\` 改工作区（APK 输入源），
\`stage-i18n-nested.mjs\` 改索引/HEAD。缺一个就分裂，而分裂时真机永远验证
工作区那份。

### 4.19.4 教训三：白屏会遮蔽同页的其他缺陷

BUG-S 修好后，\`/flashcards/stats\` 的可见文本里出现：

\`\`\`
记忆保持率 0.0% t('flashcards.stats.retentionHint', { again:
\`\`\`

**用户看到的是开发者源码**。\`StatsView.vue\` 模板里那行漏了 \`{{ }}\` 包裹，
浏览器把 JS 当正文渲染。

它在 BUG-S 之前一直存在，只是白屏让它**没有机会显示**。典型的
「修好一层露出下一层」。

**所以：确认「不崩了」之后，必须再看一眼页面内容**，不能只看控制台有没有报错。
新增 \`audit-vue-mustache.mjs\` 全量扫这类漏 \`{{ }}\`（173 个 .vue，0 误报），
脚本自身两次翻车也记在文件注释里：先用 \`lastIndexOf('<')\` 判断标签内外但逻辑
写反（365 误报），再是标签正则被属性值里的 \`>\` 截断（剩 12 误报）。

### 4.19.5 修完之后的状态

| 页面 | 修前 | 修后（真机） |
|---|---|---|
| \`/flashcards/browser\` | appHTMLLen 371，空白，2 条 SyntaxError | appHTMLLen 6972，「卡片浏览器 8 条结果」+ 真实卡片，0 错误 |
| \`/flashcards/stats\` | appHTMLLen 370，空白，2 条 SyntaxError | appHTMLLen 5786，复习曲线渲染，0 错误（BUG-T 待复验） |

带参 i18n 审计：9/9 语言 0 失败。
Cloze 字面示例 \`{{c1::answer}}\` **未被误改**（正则只匹配纯变量名，不匹配
带 \`::\` 的）—— 这是刻意设计，放宽正则就会把正确的语法示例改坏。
`

const marker = '## 5. 已验证 / 未验证'
const i = s.indexOf(marker)
if (i < 0) { console.error('找不到 §5'); process.exit(1) }
s = s.slice(0, i) + SECTION.trimStart() + '\n' + s.slice(i)
writeFileSync(P, s)
console.log(`handoff §4.19 已追加（+${SECTION.length} 字符）`)
