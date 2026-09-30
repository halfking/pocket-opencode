// correct-bugn-qualification.mjs — 修正 BUG-N 的定性（2026-09-30 真机复验后）。
//
// 为什么要改：初稿把 BUG-N 写成"编辑笔记恒 405，功能完全不可用"。
// 真机复验推翻了后半句：
//   - `notesApi.update` 全仓库**从未被调用**（grep 证实）
//   - 笔记编辑走 `notes-store.updateNote` → notes-persist → **Capacitor SQLite
//     本地库**（local_notes），根本不经过后端 /api/notes/:id
//   - 真机 `scripts/redmi-write-ops.mjs` 6/6 全过，其中「编辑笔记：列表摘要显示
//     新正文」PASS —— 编辑一直是好的
//
// 所以 BUG-N 的准确定性是：**前后端 API 契约不匹配（前端声明了 PUT，后端没
// 实现，store 层连更新方法都没有）**，属于技术债 + 未来风险（任何走 HTTP 的
// 同步/其他客户端都会撞上），**不是**用户当前可见的功能故障。
// 之前的「笔记 6/6」不是假阳性。
import { readFileSync, writeFileSync } from 'node:fs'

const P = 'docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md'
let s = readFileSync(P, 'utf8')
let changed = 0

const fixes = [
  [
    '- **现象**：`PUT /api/notes/:id -> 405`。前端 `notesApi.update` 打的就是 PUT。\n  笔记的建/读/删都是好的，所以肉眼看模块"大部分能用"，编辑却完全不可用。',
    '- **现象**：`PUT /api/notes/:id -> 405`。前端 `notesApi.update` 打的就是 PUT。\n' +
    '  笔记的建/读/删都是好的，所以肉眼看模块"大部分能用"。\n' +
    '\n' +
    '  > ⚠️ **定性修正（真机复验后）**：这条**不是**用户可见的功能故障。\n' +
    '  > `grep -r "notesApi.update"` 在整个 `frontend/src` **零命中** —— 这个方法\n' +
    '  > 从未被调用。笔记编辑实际走 `notes-store.updateNote` → `notes-persist` →\n' +
    '  > **Capacitor SQLite 本地库**（表 `local_notes`），完全不经过后端。\n' +
    '  > 真机 `scripts/redmi-write-ops.mjs` **6/6 全过**，其中「编辑笔记：列表摘要\n' +
    '  > 显示新正文」PASS —— 编辑一直是好的。\n' +
    '  >\n' +
    '  > 所以 BUG-N 的准确定性是：**前后端 API 契约不匹配**（前端声明了 PUT，\n' +
    '  > 后端没实现，`notes.Store` 连更新方法都没有）。这是技术债 + 未来风险\n' +
    '  > （任何走 HTTP 的同步、或其他客户端都会撞上），**不是**当前 UI 故障。\n' +
    '  > 本轮的修复是补齐契约，让 `notesApi.update` 不再是死路。',
  ],
  [
    '- **真机笔记编辑（BUG-N）UI 闭环未验**：后端与宿主侧脚本已证实 12/12，\n  ' +
    '  但没有在真机上点过编辑按钮。',
    '- **BUG-N 的 UI 闭环已查清，但结论与预期相反**：编辑走本地 SQLite，不经 HTTP，\n' +
    '  真机 6/6 通过。所以 BUG-N 不应被描述成"修好了用户可见的 bug"。',
  ],
  [
    '- **真机 BUG-O 闭环未复验**：APK 当时仍在 gradle 构建中。装上后必须重跑\n' +
    '  `scripts/redmi-write-ops-modules.mjs`，确认卡组详情页「开始复习」不再是 disabled。\n' +
    '  **该结论在真机复验通过前不成立。**',
    '- ~~**真机 BUG-O 闭环未复验**~~ → **已复验通过**（11:45 构建、11:46 装机）：\n' +
    '  `scripts/redmi-write-ops-modules.mjs` **7/7**。强证据：新建卡组显示 `1 cards`\n' +
    '  （修前 `0 cards`）；卡组详情页「开始复习」enabled；正文可见\n' +
    '  `1 卡组 1 今日待复习 1 张 ... 正面-080323 — New`。\n' +
    '  API 时序也对上了：`POST /notes 201` → 紧接着 `?since=新时间戳` 回读。',
  ],
  [
    '- **BUG-N** PUT /api/notes/:id 返回 200，回读 snippet 含新正文，列表摘要同步更新',
    '- **BUG-N（API 契约层）** PUT /api/notes/:id 返回 200，回读 snippet 含新正文，\n' +
    '  列表摘要同步。注意：**当前 UI 不走这条路径**（编辑走本地 SQLite），\n' +
    '  详见 §4.15.3 的定性修正',
  ],
  [
    '- **BUG-N** \`PUT /api/notes/:id\` 恒 405，编辑笔记不可用',
    '- **BUG-N** \`PUT /api/notes/:id\` 恒 405（API 契约不匹配；当前 UI 走本地 SQLite，'
    + '不受影响，见 §4.15.3）',
  ],
]

for (const [from, to] of fixes) {
  if (s.includes(from)) {
    s = s.replace(from, to)
    changed++
  } else {
    console.log(`MISS: ${from.slice(0, 60).replace(/\n/g, ' ')}`)
  }
}

// CHANGELOG 里的同一处定性也要改
const C = 'CHANGELOG-2026-09-30-BUG-D-root-fix.md'
let c = readFileSync(C, 'utf8')
const cFrom = '| **BUG-N** | `PUT /api/notes/:id` 恒 405，编辑笔记不可用 |'
const cTo = '| **BUG-N** | `PUT /api/notes/:id` 恒 405（API 契约不匹配，**非当前 UI 故障**：编辑走本地 SQLite） |'
if (c.includes(cFrom)) { c = c.replace(cFrom, cTo); writeFileSync(C, c); changed++ }
else console.log('MISS: CHANGELOG BUG-N 行')

writeFileSync(P, s)
console.log(`已应用 ${changed} 处修正`)
