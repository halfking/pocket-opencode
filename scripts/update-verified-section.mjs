// update-verified-section.mjs — 把 BUG-L/M/N/O 的已验证/未验证补进 handoff §5。
// 单独成文件是因为内联 node -e 里带中文引号和反斜杠极易被 shell 吃掉。
import { readFileSync, writeFileSync } from 'node:fs'

const P = 'docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md'
let s = readFileSync(P, 'utf8')
if (s.includes('BUG-L/M/N/O）新增已验证')) {
  console.log('§5 已含本轮补充，跳过')
  process.exit(0)
}

const Q = '"' // ASCII 双引号，替代中文引号避免 shell/编码问题
const block = [
  '### ✅ 已验证 · 补充（BUG-L/M/N/O）',
  '',
  '- **BUG-L** POST /api/flashcards/notes 真后端 201 + PG 落库 + 真机 UI 保存 201',
  '- **BUG-M** review 空 body / rating=9 均返回 400（不再 500）',
  '- **BUG-N** PUT /api/notes/:id 返回 200，回读 snippet 含新正文，列表摘要同步更新',
  '- **BUG-O** 水位线 + 服务端 >= + 保存后回读三处修复，回归锁各就位（前端 5 用例 / 后端静态锁）',
  '- **写路径 method 级探测全量**：94 条唯一写路径，405 从 1 → 0；20 个 404 中 19 个是',
  '  handler 内部「资源不存在」（正常），1 个是 SSO 未启用（功能开关）',
  '- vue-tsc --noEmit exit 0；go build ./... OK；internal/notes、internal/flashcards 全绿',
  '',
  '### ⚠️ 本轮新增未验证 / 未修（不要当成已完成）',
  '',
  '- **真机 BUG-O 闭环未复验**：APK 当时仍在 gradle 构建中。装上后必须重跑',
  '  `scripts/redmi-write-ops-modules.mjs`，确认卡组详情页「开始复习」不再是 disabled。',
  '  **该结论在真机复验通过前不成立。**',
  '- **真机笔记编辑（BUG-N）UI 闭环未验**：后端与宿主侧脚本已证实 12/12，',
  '  但没有在真机上点过编辑按钮。',
  '- `TestMeetingWorkspaceIsolation/list_A` 失败（预先存在，见 §4.15.6），未修也未定性。',
  '- `backend/internal/agent`、`backend/internal/email` 也有 FAIL，本轮未触碰这两个包。',
  '- 真机 Maestro 仍需用户手动开「USB 安装」（见 §4.11.1）。',
  '- 生产默认 https 路径仍未系统回归。',
  '- Keystore 原生插件仍未实现（代码欠账）。',
  '',
].join('\n')

const marker = '### ❌ 未验证'
const i = s.indexOf(marker)
if (i < 0) {
  console.error('未找到 §5 的「未验证」标记，未写入')
  process.exit(1)
}
s = s.slice(0, i) + block + s.slice(i)
writeFileSync(P, s)
console.log(`§5 已补充（+${block.length} 字符）`)
