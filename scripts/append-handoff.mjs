// append-handoff.mjs — 把 BUG-L/M/N/O 这一轮的结论写进 handoff 与 CHANGELOG。
// 用脚本追加而不是手改，是因为 handoff 是 UTF-8 且很长，PowerShell 的
// Add-Content 在 PS 5.1 下容易写坏编码（默认走 ANSI/BOM）。
// 用法：node scripts/append-handoff.mjs
import { readFileSync, writeFileSync } from 'node:fs'

const HANDOFF = 'docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md'
const CHANGELOG = 'CHANGELOG-2026-09-30-BUG-D-root-fix.md'

const HANDOFF_SECTION = `

---

## 4.15 BUG-L / M / N / O：写路径的第四轮清扫（2026-09-30 11:00-12:10）

### 4.15.0 先说方法论：本轮为什么能一次抓出 4 个缺陷

前几轮（BUG-D ~ BUG-K）都靠「真机上点 UI + 看 Network 面板」发现问题。这轮换了路子：
**从前端源码里静态抽出全部写请求，再对真后端逐条发探测请求**。理由是 BUG-L 的形态
——路由前缀注册了、handler 却在内部按 method 拒绝——静态前缀对账**天然看不见**，
而真探测一次就能看见。

两个脚本（共用同一套提取器，避免两份实现漂移）：

| 脚本 | 判据 | 能抓 | 不能抓 |
|---|---|---|---|
| \`scripts/audit-write-routes.mjs\` | 前端写路径能否命中已注册 mux 前缀 | 路径压根没注册 | **method 级拒绝（抓不到 BUG-L）** |
| \`scripts/probe-write-methods.mjs\` | 对真后端发请求看状态码 | **method 级拒绝（BUG-L 同类）** | 需要后端在跑 |

提取器在 \`scripts/lib/extract-write-paths.mjs\`。它的两个坑写在文件头，这里也记一笔：

- **假阴性（0 findings）**：第一版只扫 \`services/\`，且把 \`\${BASE}/notes\` 直接折叠成
  \`:seg/notes\`——不以 \`/\` 开头被丢弃，输出「0 write calls」。看着像"全部通过"，其实
  什么都没查。修法：先按同文件 \`const\` 展开模块常量。
- **假阳性（4 条）**：第二版用跨行大正则 \`/http\\(...\\)(\\s\\S{0,300}?method:...)/\`，
  把 \`email.ts:172\` 的 **GET** 调用和 175 行另一个调用里的 \`method:'POST'\` 吸成一条。
  修法：括号平衡，只在**本次调用自己的实参**里找 method。
- 现在有自检：解析出 0 条写调用时脚本以 exit 2 报错并明说"提取器坏了，不是通过"。

### 4.15.1 BUG-L：闪卡建卡片恒 405

- **现象**：真机 \`POST /api/flashcards/notes -> 405 Method Not Allowed\`。闪卡卡片永远存不进后端。
- **根因**：契约 §2 与前端 \`services/flashcards.ts\` 的 \`createNote\` 都打
  \`POST /api/flashcards/notes\`，但后端 \`handleFlashcardsItem\` 把
  \`len(parts)==1 && parts[0]=="notes"\` 一律交给 \`flashcardsNotesCollection\`，
  而后者只允许 GET（405 "GET only"）。创建实现 \`flashcardsCreateNote\` 只挂在
  **无尾斜杠**的 \`/api/flashcards\`。两条路径不等价。
- **为什么 BUG-K 的测试没抓到**：那次断言的是 store==nil 时的 503，而 503 检查在
  \`handleFlashcardsItem\` **最开头**，早于任何 method 分派——405 永远被 503 挡住。
  要观察真实分派必须注入非 nil 的零值 store。
- **修法**：后端补齐 POST（等价于 \`POST /api/flashcards\`），**不动前端**
  （契约测试 \`flashcards.contract.test.ts\` 锁的正是 \`/notes\` 这条）。
- **回归锁**：\`backend/internal/server/flashcards_create_note_route_test.go\`
- **证据**：\`scripts/verify-buglnm.mjs\` 12/12；真机 UI 保存 201；PG 有 note+card。

### 4.15.2 BUG-M：客户端输入错误被归成 500

- **现象**：\`probe-write-methods.mjs\` 对 \`POST /api/flashcards/cards/:id/review\` 发空 body，
  拿到 \`500 {"error":"invalid rating 0 (must be 1..4)"}\`。
- **危害**（不是"返回码不好看"）：前端 \`ApiError.retryable\` 依 5xx 判定可重试，
  会对一个**永远不可能成功**的请求反复重试；按 5xx 计故障率的看板会把参数错误记进去。
- **修法**：在进 store 之前挡，返回 400。
- **回归锁**：\`backend/internal/server/flashcards_review_rating_test.go\`

### 4.15.3 BUG-N：编辑笔记恒 405（store 层也缺能力）

- **现象**：\`PUT /api/notes/:id -> 405\`。前端 \`notesApi.update\` 打的就是 PUT。
  笔记的建/读/删都是好的，所以肉眼看模块"大部分能用"，编辑却完全不可用。
- **根因**：\`handleNoteOperations\` 的 switch 只有 GET/DELETE；
  \`notes.Store\` 里**根本没有任何更新方法**。要修必须补两层。
- **修法**：
  - \`backend/internal/notes/store.go\` 新增 \`NotePatch\` + \`UpdateNoteScoped\`。
    所有权进 UPDATE 谓词（不用先查后写）；**Content 变更时同步重算 Snippet**。
  - \`server_assistant.go\` 加 \`handleNoteUpdate\`，PUT/PATCH 同一套部分更新语义。
- **为什么必须同步 snippet**：列表摘要读的是 snippet。不同步的话
  "编辑后标题还在列表"这种断言**几乎恒真**（标题没动），会掩盖正文根本没存上。
- **回归锁**：\`backend/internal/server/notes_update_route_test.go\`
- **证据**：\`verify-buglnm.mjs\` 断言回读的 snippet 含新正文、且列表摘要也同步了。

### 4.15.4 BUG-O：闪卡卡片保存成功但永远看不见（数据丢失级）

这是本轮最严重的一个，**不是显示问题**。

- **现象**：真机建卡组 201 → 填正反面保存 \`POST /api/flashcards/notes\` **201** →
  查 PG：note 与 card 都在、deck_id 正确 → 宿主侧 \`GET /api/flashcards?since=0\`
  能拿到那张卡 → **但卡组详情页「开始复习」恒 disabled，卡片永远不出现。**
- **根因（两半，缺一不可）**：
  1. **客户端水位线语义错**：\`syncFromServer\` 把 \`lastSyncedAt\` 设成
     \`floor(serverTimeMs/1000)\`（服务器此刻时间），而服务端过滤是
     \`updated_at > $since\`。凡是在"写入完成 ~ 这次拉取"之间发生的变更，
     updated_at 都落在水位线之前，之后**永远拉不回来**。
     注意"宿主侧 since=0 能拿到"**不能否证**它——since=0 时不过滤。
  2. **保存后不回读**：首张 card 的 id 由服务端 \`newFlashcardID\` 生成，客户端本地
     没有这条记录，只能靠 sync 拉。而 \`FlashcardEditView.save()\` 原来是
     \`void store.flushOutbox()\`（fire-and-forget）就 \`goBack()\`，不触发任何 sync。
- **修法**：
  - 客户端 \`frontend/src/stores/flashcards.ts\`：水位线改取**本批实际收到的最大
    \`updatedAt\`**；空结果时**不推进**水位线（空结果不代表"此前都已同步"）。
  - 服务端 \`backend/internal/flashcards/store.go\`：6 处 \`> \$2\` 改 \`>= \$2\`。
    **服务端也必须改**：水位线取"本批最大"后，严格大于依然会在**同一秒内的多条变更**
    上丢数据（先收到 A → 水位线 T；服务端同秒写入 B → \`T > T\` 不成立 → B 永久丢失）。
    \`>=\` 会重复返回水位线那一秒的行，而客户端 merge-by-id 幂等，重复无副作用；
    漏数据不可逆。这个不对称是刻意的。
  - \`FlashcardEditView.save()\`：先 \`await flushOutbox()\`（让服务端建好 note+card），
    再 \`await store.refresh()\` 把服务端生成的 card 拉回，最后 \`goBack()\`。
    顺序反了拉不到。refresh 失败**不报成保存失败**——数据已落库。
- **回归锁**：
  - \`backend/internal/flashcards/store_since_test.go\`（静态锁，防语义被改回去）
  - \`frontend/src/stores/flashcards-sync-watermark.test.ts\`（5 用例）

### 4.15.5 本轮证伪的三个"疑似缺陷"（比新缺陷更值得记）

| 曾经的怀疑 | 查证结果 | 怎么查的 |
|---|---|---|
| \`OPTIONS /api/notes/:id\` 返回 200 而非 405 | **设计如此**：\`corsMiddleware\` 短路了所有 OPTIONS（server.go:885） | 读代码 + \`scripts/probe-options.mjs\` 实测任意路径/无鉴权都是 200 |
| 闪卡列表不显示卡片 | **验收标准本身错了**：列表页按设计只显示卡组（name + 今日待复习数），卡片在卡组详情页 | CDP dump DOM + 读 \`FlashcardListView.vue\` |
| 真机上"新建卡组入口没出现" | **测试脚本假设错了**：卡组入口是页内 inline（input + button），不是弹窗；按钮 disabled 只是因为 \`newDeckName\` 为空 | CDP 看到 \`input[placeholder=卡组名称]\` 与 \`新建卡组[disabled]\` 都在 |
| 探针里 \`fetch('/api/flashcards')\` 返回 HTML | **探针写错了**：裸相对路径在 \`https://localhost\` origin 下落到打包资源，与 BUG-J 同源 | 改用 \`http://localhost:8088\` 前缀后正常 |

### 4.15.6 与本轮无关的既有失败（别记到我头上，也别当新缺陷）

- \`backend/internal/server\` 的 \`TestMeetingWorkspaceIsolation/list_A\` **失败**。
  已在 \`ca4a53e\`（不含本轮任何改动）上用 \`git worktree\` 复现，**错误信息完全一致**
  → 预先存在。单独跑该测试是 PASS，全量跑才 FAIL，是测试间状态干扰。
  它同时暴露了一个真实疑点：跨 workspace 的 meeting GET 返回 200，列表返回 0。
  **未修，未定性**，留给下一轮。
- \`backend/internal/agent\`（25.8s）、\`backend/internal/email\` 也有 FAIL，
  本轮未改动这两个包（\`git diff --name-only HEAD\` 为空）。

### 4.15.7 新增脚本

| 脚本 | 用途 |
|---|---|
| \`scripts/lib/extract-write-paths.mjs\` | 写路径提取器（静态审计与真探测共用） |
| \`scripts/audit-write-routes.mjs\` | 静态前缀对账（--all 看全量） |
| \`scripts/probe-write-methods.mjs\` | **真后端 method 级探测**（--only=xxx 限定范围） |
| \`scripts/verify-buglnm.mjs\` | BUG-L/M/N 端到端验证（12 项） |
| \`scripts/inspect-flashcards-envelope.mjs\` | 闪卡增量 envelope 取证 |
| \`scripts/cdp-flashcards-store.mjs\` | 在真机 WebView 上下文里读 store / 打真机 API |
| \`scripts/probe-options.mjs\` | OPTIONS 行为取证 |
| \`scripts/fix-since-comparator.mjs\` | 一次性：\`> \$2\` → \`>= \$2\`（可删） |
`

const CHANGELOG_SECTION = `

---

## BUG-L / M / N / O（2026-09-30 第二轮清扫）

全部由 \`scripts/probe-write-methods.mjs\`（前端写路径 × 真后端 method 级探测）发现。
这类缺陷静态前缀对账抓不到——路由前缀注册了，handler 却在内部按 method 拒绝。

| 编号 | 现象 | 根因 | 修法 | 回归锁 |
|---|---|---|---|---|
| **BUG-L** | \`POST /api/flashcards/notes\` 恒 405，闪卡卡片存不进后端 | 创建实现只挂在无尾斜杠的 \`/api/flashcards\`；\`/notes\` 子路径的 handler 只允许 GET | 后端补齐 POST，两路径等价；不动前端（契约测试锁的是 \`/notes\`） | \`flashcards_create_note_route_test.go\` |
| **BUG-M** | review 空 body 返回 500 \`invalid rating 0\` | 客户端输入错误走了 store error 分支被归为 500 | 进 store 前挡，返回 400 | \`flashcards_review_rating_test.go\` |
| **BUG-N** | \`PUT /api/notes/:id\` 恒 405，编辑笔记不可用 | \`handleNoteOperations\` 只有 GET/DELETE；\`notes.Store\` 没有任何更新方法 | 新增 \`NotePatch\` + \`UpdateNoteScoped\`（所有权进 UPDATE 谓词，Content 变更同步重算 snippet）+ \`handleNoteUpdate\` | \`notes_update_route_test.go\` |
| **BUG-O** | 闪卡卡片保存 201、PG 有数据，但卡组页永远看不到卡片 | ① 客户端水位线取 \`serverTimeMs\` 而非本批最大 \`updatedAt\`；② 服务端 \`updated_at > since\` 严格大于；③ 保存后 fire-and-forget 不回读，服务端生成的 card id 客户端拿不到 | ① 水位线改本批最大 \`updatedAt\`，空结果不推进；② 6 处 \`>\` 改 \`>=\`；③ \`await flushOutbox()\` 后 \`await refresh()\` 再 \`goBack()\` | \`store_since_test.go\` + \`flashcards-sync-watermark.test.ts\` |

**关于 BUG-O 的服务端 \`>=\` 改动**：客户端水位线改成"本批最大 updatedAt"后，
严格大于依然会在**同一秒内的多条变更**上丢数据（先收到 A → 水位线 T；服务端同秒写入
B → \`T > T\` 不成立 → B 永久丢失）。改成 \`>=\` 会重复返回水位线那一秒的行，
而客户端 merge-by-id 幂等，重复没有副作用，漏数据不可逆。这个不对称是刻意取舍。

### 本轮证伪的疑似缺陷（比新缺陷更值得记）

- \`OPTIONS\` 任意路径返回 200：\`corsMiddleware\` 的标准预检短路，设计如此。
- 闪卡列表不显示卡片：列表页按设计只显示卡组，卡片在卡组详情页——验收标准写错了。
- 真机"新建卡组入口没出现"：卡组入口是页内 inline 而非弹窗，测试脚本假设错了。
- 探针里裸 \`fetch('/api/...')\` 返回 HTML：探针没加 base 前缀，与 BUG-J 同源。

### 与本轮无关的既有失败

\`backend/internal/server\` 的 \`TestMeetingWorkspaceIsolation/list_A\` 失败，
已在 \`ca4a53e\`（不含本轮改动）上用 git worktree 复现且错误信息一致 → 预先存在。
它同时暴露一个未定性的疑点：跨 workspace 的 meeting GET 返回 200、列表返回 0。
`

let h = readFileSync(HANDOFF, 'utf8')
if (h.includes('4.15 BUG-L / M / N / O')) {
  console.log('handoff 已含 4.15，跳过')
} else {
  // 插到 §5「已验证 / 未验证」之前：结论在前，证据在后。
  const marker = '## 5. 已验证 / 未验证'
  const i = h.indexOf(marker)
  if (i < 0) { console.error('找不到 §5 标记，未写入 handoff'); process.exit(1) }
  h = h.slice(0, i) + HANDOFF_SECTION.trimStart() + '\n' + h.slice(i)
  writeFileSync(HANDOFF, h)
  console.log(`handoff 已更新（+${HANDOFF_SECTION.length} 字符）`)
}

let c = readFileSync(CHANGELOG, 'utf8')
if (c.includes('BUG-L / M / N / O（2026-09-30 第二轮清扫）')) {
  console.log('CHANGELOG 已含本节，跳过')
} else {
  writeFileSync(CHANGELOG, c.trimEnd() + CHANGELOG_SECTION)
  console.log(`CHANGELOG 已更新（+${CHANGELOG_SECTION.length} 字符）`)
}
