// append-handoff-3.mjs — 追加 §4.17：BUG-P（闪卡模块用户根本进不去）。
import { readFileSync, writeFileSync } from 'node:fs'

const P = 'docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md'
let s = readFileSync(P, 'utf8')
if (s.includes('4.17 BUG-P')) { console.log('已含 4.17，跳过'); process.exit(0) }

const SECTION = `

---

## 4.17 BUG-P：闪卡模块修好了三轮，用户却根本进不去（2026-09-30 12:20-12:50）

这一条比 BUG-L/M/N/O 加起来更值得记，因为它是**前三轮验收的方法盲区**
直接导致的漏检。

### 现象

写 \`.maestro/flashcards-write.yaml\` 时，flow 第一步是「点『更多』→ 找闪卡」，
结果直接 FAILED。Maestro 的失败截图给出了真相 —— MORE FEATURES 一栏 10 项：

\`\`\`
Chat / PKM Notes / Email / RSS / Vault / Scheduled Automation /
Skill Market / Agent Market / Local Agent / Workbuddy
\`\`\`

**没有闪卡**。查 \`MoreHubView.vue\` 的 \`mainFeatures\`：10 条，0 条指向
\`/flashcards\`。

### 为什么前三轮都没发现

闪卡路由一直存在（\`router-mobile.ts\` 的 \`/flashcards\`），BUG-K/L/O 三轮把
后端与页面逻辑都修好了。**但前三轮验收全部用 CDP 直接改 \`location.hash\` 导航**
（\`#\`/flashcards\`、\`#/notes/new\`…），完全绕过了真实 UI 入口。

于是「功能测试全绿」和「用户进不去」可以同时成立：
- 路由能进 ≠ 用户能到
- 接口 201 ≠ 模块可达

这是**可访问性缺陷**，不是功能缺陷。任何只验「路由存在 + 接口通」的验收都会漏。

### 修法

\`MoreHubView.vue\` 的 \`mainFeatures\` 末尾加
\`{ to: '/flashcards', icon: 'style', label: t('nav.flashcards') }\`，
\`nav.flashcards\` 在 9 种语言补齐（走 \`stage-i18n-nav-flashcards.mjs\`，不夹带
locales 里并发会话的未提交改动）。

### 证据（修前/修后对照）

| | 修前 | 修后 |
|---|---|---|
| 模拟器 | 失败截图：MORE FEATURES 10 项无 Flashcards | — |
| 真机 | \`cdp-more-hub.mjs\`：九宫格无闪卡文案 | \`闪卡入口: 存在 ✅\` |
| 真机 BUG-K/L/O | 7/7 | 7/7（新 APK 复验，无回归） |

### 新增探针 \`scripts/cdp-more-hub.mjs\`

专门核对「功能点是否**从真实 UI 入口可达**」。

它自己翻过一次车，正好印证本轮的主题：第一版只抓 \`a[href]\`，而九宫格是
**点击事件绑定的元素、没有 href**，于是只捞到底部导航 5 项，输出了
「闪卡入口缺失」。那个结论是**探针不完整**，不是产品结论。改成按可见文案抓之后
才拿到真实的 47 个条目。

脚本里有完整性自检：一个九宫格条目都没抓到时 exit 2 并明说「本轮不能给出
缺失结论」。**探针失效时必须闭嘴，不能输出一个看起来像结论的东西。**

### 教训（建议进验收清单）

1. 验收**每个功能点是否可达**，不能只验「路由存在 + 接口通」。可达性必须在
   真实 UI 入口上验。
2. CDP 改 hash 与 Maestro 从入口导航，**盲区不一样**，两种驱动方式交替使用。
   本轮把 CDP 脚本换成 Maestro，才顺手抓出这个洞。
3. 探针报「缺失」之前，先自证探针本身是完整的（这次是靠截图发现的）。
`

const marker = '## 5. 已验证 / 未验证'
const i = s.indexOf(marker)
if (i < 0) { console.error('找不到 §5'); process.exit(1) }
s = s.slice(0, i) + SECTION.trimStart() + '\n' + s.slice(i)
writeFileSync(P, s)
console.log(`handoff §4.17 已追加（+${SECTION.length} 字符）`)
