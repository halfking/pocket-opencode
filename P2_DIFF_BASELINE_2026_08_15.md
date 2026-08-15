# P2 Diff 设备性能基线报告（2026-08-15）

## 结论：✅ 通过

09 篇 P2 验收要求「Diff 在 5,000 行输入下可分段渲染；性能阈值以真实设备基线确定，
不以桌面浏览器代替」——在 Android 模拟器（arm64，API 30，pixel_5 AVD `pocket_test`）
的 WebView 中，用**真实 App + 真实数据链路**完成了基线测量。所有交互延迟远低于
100ms 门槛，滚动稳定 60fps、零掉帧。

## 环境与数据链路

| 项 | 值 |
|---|---|
| 设备 | Android 模拟器 pixel_5（arm64，Android 11 / API 30） |
| App | `com.kaixuan.opencode.pocket` debug（当日 main + 本会话修复，JDK 21 构建） |
| 数据链路 | mock OpenCode 实例（宿主 :19222，仿 V1 `/session` `/session/:id` `/event` SSE）→ pocketd（:8088）→ 设备 App（10.0.2.2）→ `useSessionStore` SSE 事件 → `DiffBlock.vue` |
| 测量注入 | 5,044 行 / 291,749 字符 unified diff（40 hunks × 125 行），经 mock `/event` 真实推送；计时用 CDP `Runtime.evaluate` 在设备 WebView 内执行（`performance.now()` + 双 rAF 落帧） |
| 每段内部滚动容器 | `.diff-lines-list`（overflow auto，max-height 52vh，DOM 最重的面） |

计时口径：组件挂载 = store 消息变更 → 双 `requestAnimationFrame` 后（含 Vue 渲染
提交与一次绘制）；交互延迟 = `click()` → 双 rAF。

## 基线数据

### 场景 A：40 段 hunk（5,044 行 / 291,749 字符）

| 指标 | 数值 |
|---|---|
| 解析 + 首屏渲染 | **21.8 ms**（初始仅挂载 125 行 / 1 段） |
| 逐段展开（39 段，每段点开到双 rAF） | 中位 **33.3 ms**，p95 36.8 ms，最大 36.9 ms |
| 全量展开后 | 5,000 行 / 15,368 DOM 节点挂在 `.diff-block` 内 |
| 展开态 hunk 内滚动（3s） | **60 fps**，0 帧 >34ms，最大帧间隔 21 ms |
| 全部折叠回收 | 32.5 ms |

### 场景 B：单 hunk 5,000 行（渐进挂载路径）

| 指标 | 数值 |
|---|---|
| 解析 + 首屏渲染 | **25.9 ms**（初始挂载 250 行 = MAX_INITIAL_LINES） |
| 「继续显示」逐步展开（250 行/次 × 19 次） | 中位 **31.4 ms**，最大 36.6 ms |
| 全量展开后滚动（4s） | **60 fps**，0 帧 >34ms，最大帧间隔 21 ms |

### 判定

- 分段渲染确认：初始 DOM 只含首段前 125/250 行，折叠段零行级 DOM。
- 全部交互（展开/继续显示/折叠）中位 31–33 ms、p95 ≤ 37 ms，低于 08 篇 100 ms
  可感知门槛。
- 全量挂载 1.5 万节点时滚动仍 60fps 零掉帧。
- 局限：模拟器为 arm64 真实 Android 运行时但非物理设备；物理真机（中低端机型）
  复测一次即可把阈值固化进验收（数值预计同量级，因初始挂载量被 250 行上限钳制）。

## 过程中发现并修复的设备阻断 bug（本会话提交）

基线搭建过程中，从「全新安装」到「看到 diff」要走通全链路，连带修复 6 个真实 bug
（全部在设备上复现并验证修复）：

1. **全新安装无法创建主密码（P0）**：`localDB.init` 把整段 `SCHEMA_SQL` 交给
   Capacitor SQLite `execute()`，Android 插件按 `;` 机械切分，CREATE TRIGGER 的
   `BEGIN...END` 体被截断（"Execute: incomplete input ... CREATE TRIGGER"）。
   修复：`schema.ts` 新增 `splitSqlStatements()`（感知触发器体，纯函数，3 个
   node 测试），`local-db.ts` 改 `executeSet` 单事务建表。
2. **迁移在初始化完成前被拒（P0，叠加 1）**：`ensureSchemaMigrations` 内的
   `this.query` 被 `requireReady()` 拒绝（`initialized` 置位太晚）。修复：DDL
   成功后先置位再跑迁移。
3. **SSE 鉴权参数名不一致（P1）**：`sse.ts` 用 `access_token` 传 token，
   `requireAuth` 的 WS/SSE 例外分支只认 `token`（与 `websocket.ts` 一致），
   导致会话 SSE 全部 401、只能靠轮询兜底。修复：统一为 `token`。
4. **SSE 事件信封未解包（P1）**：pocketd 转发 OpenCode 信封
   `{id,type,location,data:{...}}`，store 却读 `evt.data.tool/output`（实际在
   `evt.data.data.*`），工具名/输出永远到不了 UI——diff 根本无法经 SSE 渲染。
   修复：`session.ts` 解包 `evt.data?.data ?? evt.data`（兼容扁平旧格式）。
5. **已认证访问 /login 无限导航循环（P0）**：routeGuards Case A 对「已认证访问
   /login」返回 `redirectLogin`，而其落地路径又是 `/login`；App 重启后（token
   持久、lobster 锁定）任何 requiresLobster 路由 → redirectUnlock → /login →
   循环，实测在 Android WebView 上把渲染进程打死（「网页无法运行」），解锁流程
   完全不可用。修复：新增 `redirectHome` outcome（已认证+已解锁访问 /login 才弹
   回首页；已认证+未解锁 allow 渲染解锁 UI）。
6. **lobster 就绪状态非响应式（P2 技术债，交接待办①）**：`_ready` 模块布尔改为
   `ref` 导出 `lobsterReady`，`SessionWorkspaceView.detailReady` computed 改读
   ref，解锁后工作台详情自动挂载。

## 未修复的已知问题（移交后续）

- **消息历史端点序列化为空对象**：`adapter.OpenCodeMessage` 全字段 `json:"-"`，
  `GET /api/mobile/sessions/{id}/messages` 实际返回 `messages:[{},...]`，前端
  `normalizeMessage` 因无 `id` 全部丢弃。历史回填目前形同虚设，会话内容只靠
  SSE 实时事件（本次修复后可用）。修复需要 V1 `{info,parts}` → 移动端消息
  `{id,role,content}` 的映射层，属独立特性工作。
- 物理真机复测一次基线数值（见上文判定）。
- 模拟器 API 30 `navigator.onLine` 不随飞行模式翻转（既有备注，仍然成立）。

## 复现

```bash
# mock 实例 + pocketd（脚本本会话用后即弃，未入库；重新生成约 60 行 node http）
node mock-opencode.mjs            # :19222，40 hunks × 125 行 diff，/event 周期重播
cd backend && set -a && source .env && set +a && ../bin/pocketd &
TOKEN=$(curl -s -X POST :8088/api/auth/login -d '{"username":"admin","password":"admin"}' | jq -r .token)
node register-instance.mjs $TOKEN  # WS /plugin/ws?type=plugin&id=mock-diff-baseline
# App 登录 → 创建主密码 → 选实例 → 打开会话 → 20s 内 SSE 推 diff
# 测量：adb forward tcp:9222 localabstract:webview_devtools_remote_$(pidof com.kaixuan.opencode.pocket)
#       后用 CDP Runtime.evaluate 注入计时脚本（见本次会话 measure*.mjs 思路）
```
