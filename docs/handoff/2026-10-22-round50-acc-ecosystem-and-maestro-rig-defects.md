# round50 —— ACC 生态读取面接通；ACC 会话标识整片丢失修复；Maestro rig 真缺陷；13 条主包流逐条结论

日期：2026-10-22
分支：`fix/acc-runtime-service-jwt`（openpocket），基线 `d7166f69`
agent-companion：`main` @ `e1cf36c`（线上 daemon 已下发 `0.1.0-e1cf36c.1792616171`）

## 一句话结论

本轮把「openpocket 经 ACC 连接到 agent-companion 并取到会话与任务」这条链路
**从代码到线上跑通**，途中挖出并修掉 **3 个生产级真缺陷**（ACC 会话标识字段
整片丢失；ACC Runtime 客户端从未装配；Maestro rig 的 monkey 启动在 macOS/
Android 上恒失败），另修掉 **9 类测试装置缺陷**（locale 判据、跨 flow 残留
污染、键盘遮挡导致的误点、`hideKeyboard` 的隐藏副作用、**Maestro 变量
`${VAR}` 根本没展开**、页面就绪判据是瞬时的、夹具重启 App 导致本地库重新
上锁、行节点合并导致点歪、**录音残留毒掉了后面 4 条 flow**），
以及 **3 处登录 flow 的判据错误**。

13 条主包 Maestro 流最终 **12 条绿**，剩下 1 条（`settings-llm-gateway`）
红在**环境前置缺失**（本机没有可用的 LLM 网关 API Key）—— 失败现场证明
网关地址与模型列表都已正确渲染，唯一缺的是凭据，如实登记不放宽断言。

**本轮另有一项产品级 a11y 发现**：D 态「创建主密码」模态框在本机 WebView 上
**完全不进 Android 无障碍树**（详见 §3.4），它让「首次安装」这条路径在
TalkBack 与任何 a11y 自动化下都不可达。

---

## 1. agent-companion（被测服务侧）

### 1.1 线上 daemon 已跑到最新 HEAD

| 项 | 值 |
|---|---|
| 部署根 | `~/kaixuan/acp`（launchd `com.agent-companion.npm`，:12110） |
| 当前版本 | `0.1.0-e1cf36c.1792616171`（含 `e1cf36c` preset 点号文件修复 + `72ffc8b` memora `X-Project-ID` 头） |
| 上一版（保留可回滚） | `0.1.0-1f9f1fa.1792606386` |
| 回滚方式 | `ln -sfn <旧版本>/bin/agent-companion ~/kaixuan/acp/agent-companion && ac-ctl restart` |
| 会话专项回归 | `python3 .work/testrun/session_battery.py http://127.0.0.1:12110` ⇒ **TOTAL=71 PASS=71 FAIL=0 SKIP=0**（本轮结束时复验仍全绿） |

### 1.2 本仓 HEAD 的两处缺陷（前一轮修完，本轮复核仍绿）

- **跨源重复会话**：CLI store 与 runtime-state.sqlite 的 413 个 id 完全重合，
  而清单按 `(kind,id)` 折叠、跨 kind 永不塌缩 ⇒ 800 行里 299 行重复、挤掉
  299 个真实会话、offset 分页返回**重叠页**。修法：CLI store 拥有优先，
  runtime-DB 独有的字段回填给幸存的 mcode 行。实测重复 id **299 → 0**，
  清单行数 800 → **602**（全部唯一）。
- **live-e2e 在 macOS 上根本跑不起来**：`os.MkdirTemp("")` 落在
  `/var/folders/<hash>/T/`，unix socket 路径超 macOS `sun_path` 的 104 字节
  ⇒ daemon `bind: invalid argument` 启动即死。修法：状态目录走 `/tmp` 短路径
  并显式断言长度。修复后 `go run ./cmd/live-e2e -fixture-agent -phases
  execute,resume,cancel` ⇒ **`LIVE_E2E_OK`**。

---

## 2. openpocket ↔ ACC ↔ agent-companion

### 2.1 生产缺口①：ACC Runtime 客户端**从未被装配**（已修）

`SetACCRuntime` 只在测试里被调用，`backend/cmd/pocketd/main.go` 从未 new 过
`accruntime.Client` ⇒ `s.accRuntime` 恒为 nil ⇒ `mobile_approval_handler.go`
里 `if s.accRuntime != nil` 永远为假 ⇒ 绑定到 ACC dispatch 的任务
**不经过 ACC permission gate**（门禁形同虚设）；ACC-bound 任务的取消只会拿到
503 `acc_runtime_not_configured`。

### 2.2 生产缺口②：ACC `/api/v2/*` 只认 HS256 签名服务 JWT（已修）

对活 ACC 实测（`GET /api/v2/approvals`）：

| Authorization 形态 | 结果 |
|---|---|
| 无 | 401 `missing bearer token` |
| 静态 Bearer | 401 `invalid bearer token` |
| **HS256 签名 JWT** | **200** `{"approvals":[],"count":0,"ok":true}` |

而 openpocket 的 `acchttp` 只能发静态 `APIKey` ⇒ 恒 401，且与「ACC 挂了」
**不可区分**。修法：新增 `backend/internal/accjwt`（手写 HS256，claims 沿用
ACC 线上契约 `sub/username/tenant_id/type/role/iat/nbf/exp`，nbf 回拨 30s）；
`acchttp.Config` 增加 `Signer` 接口且 **Signer 优先于 APIKey**（反序会发静态
token 拿 401，而签好的 JWT 就在配置里，排查方向会被带偏）；签名失败/空串直接
让请求失败且不落到服务端。

配置项：`POCKET_ACC_RUNTIME_URL` / `POCKET_ACC_CONTROL_SECRET`。

### 2.3 生产缺口③：ACC 会话的标识字段**整片丢失**（本轮新发现并修）

ACC 的 `acc_list_sessions`（acc-go `internal/sessionmanager.SessionRecord`）
把 `id` / `agent_session_id` / `gw_session_id` / `dispatch_id` / `output` /
`created_at` 放在**顶层**，**从不返回 `metadata`**。而 openpocket 的
`mcp.AccSession` 读的是 `session_id` / `metadata` / `started_at` / `ended_at`。

后果不是「少几个字段」，而是**会话标识全丢**：`accSessionToRow` 从 Metadata
里取 `agent_session_id`，取不到就退到 `row.SessionID`（ACC 也不返回
`session_id`，是空串）。

实测症状（修复前）：

```
GET /api/tasks/<acc task_id>/session-bundle
→ {"current":[{"id":"", "title":"ReClaw code-review/start: wf-ec", …}]}   ← id 是空的
```

修复后（同一台 ACC、同一 task_id）：

```
→ {"historical":[{"id":"156b2830-98f8-4f04-9678-1ee373dea05e",
                  "agentSessionId":"ses_ece2e86ffffeY1EcUq2uB2OtVP",
                  "gwSessionId":"gw_dispatch_65dcd3c3-…",
                  "startedAt":"2026-10-12T20:32:55.469521Z"}]}
```

### 2.4 新增 ACC 生态读取面（4 个路由）

pocketd 此前**没有一条经 ACC 的读取路径**（只有直连 agent-companion 的 v1 面），
而多机器的 agent-companion 实例是**汇聚在 ACC 上**的 —— 本机 ACC 上挂着十几个
实例（`agent-companion-host` / `acs-macbook-30` / `acs-local` /
`runtime-dev-01` …），它们此前在 pocketd 里完全不可见。

| 路由 | 数据来源 | 实测（对活 ACC） |
|---|---|---|
| `GET /api/acc/agents` | ACC v2 REST `/api/v2/agents` | 200，列出 agent-companion 实例，status=online |
| `GET /api/acc/tasks` | ACC v2 REST `/api/v2/canonical/tasks` | 200，count=0（ACC 侧 canonical tasks **确实为空**，如实反映） |
| `GET /api/acc/projects` | ACC v2 REST `/api/v2/projects` | 200，count=0（同上） |
| `GET /api/acc/sessions` | ACC MCP `acc_list_sessions`（**不带** task_id） | 200，**count=15**，`agentSessionId` 非空 |

为什么 sessions 必须不带 `task_id`：ACC 的 canonical task 列表在本机实测是
**空的**，而 agent-companion 上报回来的会话带着自己的 `task_id`
（形如 `acs-macbook-30b:dispatch_<uuid>`）⇒ 「按 task_id 查会话」在 pocketd
里永远查不到东西 —— ACC 里有会话，pocketd 却一个也列不出来。

`tasks` / `projects` 的条目按**原始 JSON** 透传（`json.RawMessage`），不抄一份
字段表：ACC 的 task/project 结构随 canonical contract 演进，抄一份就会变成
「字段永远缺一个」的影子契约；透传既不丢字段、ACC 加字段时也自动跟随。

### 2.5 直接连 ACC 的对照实测（不经 pocketd）

`acc_get_agents` 返回 agent-companion 实例列表；
`acc_list_sessions` 返回 agent-companion 上报的会话
（`agent_session_id` / `dispatch_id` / `task_id` 齐备）；
`acc_get_tasks` 返回 `[]`。

---

## 3. 真机（AVD）部署与逐功能测试

### 3.1 环境事实（如实登记）

**本机没有 USB Android 真机** —— `adb devices` 只有模拟器
（`emulator-5554` 被并行会话占用、本会话克隆 AVD 起 `emulator-5556`），
USB 上只识别到 iPhone。因此「真机部署与逐功能测试」是在 **AOSP Android 14
模拟器（1080×2400）** 上完成的，与本仓 round47 handoff 的既有做法一致
（其原文即写「真机（实为 AVD）」）。设备 locale 为 **en-US**，
WebView 版本 **113.0.5672.136**（这一点在 §3.4 里是决定性的）。

### 3.2 13 条主包 Maestro 流

最终结果见 §5。

### 3.3 本轮修掉的测试装置缺陷（9 类）

| # | 缺陷 | 取证 | 修法 |
|---|---|---|---|
| ① | **判据缺英文分支** ⇒ en-US 设备上 5 条 flow 恒红 | 失败现场树里 'AI Work Mess' / 'New deck' / 'Flashcards'；设置页 'Language → English' | 给**走 i18n** 的中文判据补 `中文|English`；英文取值全部取自 `frontend/src/locales/en-US.json`（不猜）。硬编码中文的（「刷新」「定时任务」「用量汇总」…）两种 locale 都一样，不补 |
| ② | **跨 flow 残留污染** | tasks-crud 造的「Maestro任务」残留 ⇒ 首页常驻「需要你介入」横幅 ⇒ **压住底部导航** ⇒ 下一条 flow 点「更多」没反应 | 新增 `scripts/tasks-residue-fixture.mjs` 夹具：flow 前清上一轮、flow 后清本轮（只删标题精确匹配的，宁可漏删也不误删共享后端里的真任务） |
| ③ | **键盘遮挡导致误点**（flashcards-write） | 树里两个 "Save"：顶栏 `type=button @click` 与底部 `type=submit`；软键盘开着遮住底部那个 | `tapOn: {text:"保存\|Save", index: 0}` 明确点顶栏 |
| ④ | **`hideKeyboard` 的隐藏副作用** | Android 上 Maestro 的 hideKeyboard 是**按返回键**实现 ⇒ 顺带导航回退，页面从建卡页回到卡组页，下一步报「Element not found」 | 去掉该步骤并写进注释（顶栏 Save 在 y=81..160，本来就不在键盘遮挡区）。**本轮又踩到同一族坑第二次**（notes-crud，见 §3.5） |
| ⑤ | **Maestro 的 `${VAR}` 根本没展开** | 失败日志明写 `Inputting text: undefined` ⇒ 密码框里躺的是字面量 "undefined" ⇒ 必然 401 | `scripts/maestro-run.mjs` 新增 `materializeFlowWithEnv`：跑前生成临时 flow、在前置区插入 `env:` 块（口令只经环境变量传递，临时文件 0600，跑完删除），跑完再把 Maestro 落盘产物里的口令明文抹掉 |
| ⑥ | **页面就绪判据是瞬时的**（email-accounts） | 原判据等「已同步 N 个账户，新邮件 M」（后台收信刚完成时的 fetchHint，几秒即逝）或「下拉同步邮件」（下拉手势进行中才渲染）⇒ 等的是「此刻恰好不在」的节点 | 换成**常驻**信号：收件箱的分类 chip 组（`email-categories.ts:5-11`，硬编码中文 ⇒ 不必补英文分支） |
| ⑦ | **夹具重启 App ⇒ 本地库重新上锁**（flashcards-write） | 闪卡夹具会 force-stop + 重启 App 自证缓存已清；本地 SQLCipher 密钥只在内存 ⇒ 守卫把依赖本地库的路由弹到「解锁本地数据」屏 | harness：夹具跑完后再 `ensureLocalDbUnlocked()` 一次并复位起点路由；解不开就响亮退出（§3.8） |
| ⑧ | **行节点合并 ⇒ 选中了屏幕最底边那 12px**（email-browse） | Maestro 日志显示它点的是 `bounds=[28,2388][1036,2400]`（整行只有 12px 在屏内）⇒ 触点 y=2394 打在**底部导航栏**上 | `tapOn` 加 `index: 0`，锁定树里第一个匹配项（§3.9） |
| ⑨ | **录音残留毒掉了后面 4 条 flow** | meetings-entry 开的录音从不收尾 ⇒ 麦克风一直录 + 「会议录音中 mm:ss」浮层每秒重绘 ⇒ 无障碍树与画面不同步 | meetings-entry 收尾停掉录音并断言（§3.6） |

另有一处 harness 层面的环境缺陷：**`monkey` 起不来 App**（见 §4.1）。

### 3.4 产品级发现：D 态「创建主密码」弹窗**不进 Android 无障碍树**

**现象**：登录成功、设备上还没有主密码时，App 会弹出「创建主密码」模态框。
- 视觉上确实渲染在屏幕上（截图 + `document.body.innerText` + 计算样式
  `position:fixed / z-index:1500 / display:grid / opacity:1 / visibility:visible`
  四项都证实）；
- 但 **Android 无障碍树里一个节点都没有**：标题「创建主密码」、提示、三个
  输入框、「取消」「确认」**全部缺席**。

**影响**：
- **TalkBack 用户在首次安装后会被卡死** —— 登录成功了，但主密码弹窗读不到、
  点不到 ⇒ 主密码建不了 ⇒ App 用不下去；
- 任何基于无障碍树的自动化都拿不到这个弹窗（Maestro/UIAutomator 同理）。

**取证**（同一时刻、同一页面上的对照实验，逐条排除）：

| 实验 | 变更 | 结果 |
|---|---|---|
| 基线 | 无 | 弹窗子树**不在**无障碍树里 |
| 去掉 `aria-modal` | 改标记 | 仍不在 ⇒ 与 aria-modal 无关 |
| `position: fixed → absolute` | 改布局 | 仍不在 ⇒ 与定位 / 滚动容器无关 |
| 同一容器内插入**同构**合成浮层 | 改 DOM | **在**无障碍树里（标题、按钮都有）⇒ 与标记结构无关 |
| 把弹窗节点 remove + 重新 insert | 改 DOM | **在**无障碍树里（bounds 齐全）⇒ 是**插入那一刻**无障碍树没有重建 |

**判定**：本机 WebView 是 **113.0.5672.136（2023-05）**；而仓库自己的
`_set-master-password.yaml`（2026-10-01 实测坐标）说明**在作者那台设备上这个
弹窗是能进无障碍树的** ⇒ 这不是普遍的 App 缺陷，而是**本机 WebView 版本**
上的无障碍树不刷新。

**处置（以及为什么没改产品代码）**：
- 没有在产品里塞「强制无障碍树刷新」的 hack —— 那是浏览器版本相关的错层
  修复，而本机 WebView 早已过时（真实用户几乎不会命中）。
- 跑批前用 harness 的 preflight（CDP 走 App 自己的表单与自己的接口）完成
  **一次性设备初始化**：把主密码建好（`pocket_crypto_cfg.hasMasterPassword=true`），
  让最终跑批走「登录成功 ⇒ 直接进 App」这条主路径。
- **如实登记的覆盖缺口**：D 态（首次安装、无主密码）这条路径**本轮没有被
  Maestro 覆盖** —— 它由 harness 的 preflight 以 CDP 覆盖（登录 + 建主密码 +
  进 `#/ai`，实测通过）；Maestro 这条路在本机被无障碍树挡住。flow 里已保留
  D 态分支（在能看见弹窗的设备上自动生效）。

### 3.5 又一次 `hideKeyboard` 导航回退（notes-crud）

本轮跑批里 notes-crud 红了，红在 `^MaestroPKM笔记$` 不可见。失败现场的树里
**那条笔记明明建好了**（列表项文本是「MaestroPKM笔记空笔记10/22」—— 标题+
摘要+日期被 WebView 合并成一个节点），而且现场停在**列表页**，不是编辑页。

真因：改名步骤里的 `hideKeyboard`。Android 上 Maestro 的 hideKeyboard 是
**按返回键**实现的；模拟器刚重启、输入法首启很慢，键盘还没起来 ⇒ 那个返回键
直接被 App 吃掉 ⇒ **页面从编辑页退回列表页** ⇒ 下一条判据必然红。

⇒ 与 flashcards-write 同一条纪律：**能不收键盘就不收**。已删掉该步骤
（标题输入框与「返回」箭头都在页面顶部，本来就不在键盘遮挡区），
并把这条坑写进 flow 注释。

### 3.6 录音残留：一条 flow 开的录音，毒掉了后面 4 条 flow

**这是本轮最大的一处装置缺陷**，形态隐蔽、影响面大。

`meetings-entry.yaml` 进 `?record=1` 会**自动开始录音**，而原文件**从不收尾**
⇒ 录音一直开着，后果有两条：

1. **设备麦克风在整轮跑批期间一直在录**（这是不该留下的副作用）；
2. 离开宿主页后，屏幕上常驻一个「会议录音中 mm:ss」浮层
   （`frontend/src/components/RecordingPill.vue`），它**每秒刷新**（计时器）
   ⇒ 页面每秒重绘 ⇒ **WebView 的无障碍树被不停重建** ⇒ Maestro 拿到的
   树与屏幕**对不上**、元素定位与滚动全部失准。

被它带崩的 4 条 flow（形态高度一致：元素「明明在屏内」却判找不到，或
**点击落到了树里没有的那个元素上**）：

| flow | 失败现象 | 现场 |
|---|---|---|
| `more-grid-reach` | `scrollUntilVisible "任务\|Tasks"` ⇒ `No visible element found` | 失败截图里「Tasks」**在屏内**；树里也有 `Tasks Button [31,1506][388,1680]` |
| `more-entries-open` | 同上（`成本与配额\|Cost & Quota`） | 同上，「Cost & Quota」在屏内 |
| `email-browse` | 点开一封邮件后 `邮件详情\|Email detail` 不出现 | 失败截图里**录音浮层正在跑**（`会议录音中 32:47 1966 s`） |
| `flashcards-write` | 点顶栏 `Save` 后没回到卡组页 | 失败截图上顶栏右端是「•••」，而**树**说那里是 `Save Button [931,81][1047,160]` ⇒ **树与画面不是同一份**，点到了「•••」 |

⇒ 修法：`meetings-entry.yaml` 自己收尾 —— 停掉录音，并断言 dock 的
aria-label 从「停止录音」翻成「开始录音」（`MeetingMicDock.vue:6`），
那正是「确实停了」的证据。停完之后整轮跑批不再有录音浮层。

⚠️ 顺带更正本报告早先的一处判断：§3.3 ④ 里说「`hideKeyboard` 会导航回退」
是**闪卡**那条 flow 的问题；**同一条纪律**在本轮的 notes-crud 上又踩了一次
（见 §3.5），而**录音浮层**是另一族（树与画面不同步），两者不要混为一谈。

### 3.7 登录 flow 的三处判据错误（2026-10-02-real-login）
这条 flow 之前从来没绿过，原因**不是产品坏了**，是判据本身错了三处：

| # | 错在哪 | 现象 | 修法 |
|---|---|---|---|
| ① | 落点集合 `visible: "创建主密码\|解锁本地数据\|密码登录"` **把「还停在登录页」也算成合法落点** | 口令没展开 ⇒ 登录 401 ⇒ 页面纹丝不动 ⇒ 第三个候选「密码登录」恰好成立 ⇒ 这条 extendedWaitUntil 照样 COMPLETED，真失败被推迟到下游 | 落点集合去掉「密码登录」，只剩 ①进 App（外壳「打开菜单\|Open menu」）②D 态弹窗 ③B 态解锁屏 |
| ② | 收尾硬判据 `assertNotVisible: "^密码登录$"` 在 D 态下**永远不成立** | `MasterPasswordDialog` 渲染在 LoginView **内部**，D 态下登录页整棵树还在树里（被弹窗盖着）；Maestro 的 visible 不区分遮挡 | 判据前先把 D 态弹窗处置掉（跑 `_set-master-password.yaml`），弹窗关闭后 LoginView 被 `router.replace('/ai')` 卸载，登录页才真正消失 |
| ③ | 登录手势链里的 `${POCKET_DEV_PASS}` **从来没成功展开过** | 日志明写 `Inputting text: undefined` | 同 §3.3 ⑤：`materializeFlowWithEnv` |

修完后实测：`^密码登录$|AI 工具` 出现 → 手势输口令 → 点登录 → 落点判定通过
→ D 态分支（在本机被跳过）→ 「打开菜单\|Open menu」出现 → 登录页确实消失，
**全绿**。

### 3.8 夹具重启 App ⇒ 本地库重新上锁 ⇒ 守卫把 flow 弹到解锁屏

flashcards-write 第二次跑批仍红，但失败现场换了：不是编辑器页，而是
**「解锁本地数据」屏**（「检测到已有登录态，但本地加密库未解锁」）。

真因：闪卡夹具为了自证「重启后缓存确实没了」会 **force-stop + 重启 App**，
而本地 SQLCipher 的密钥**只存在于内存** ⇒ 重启后即使 token 还在，守卫也会把
**依赖本地库**的路由弹到解锁屏。而 preflight 的解锁发生在**夹具之前**，
于是这条 flow 是在「本地库已上锁」的状态下跑的。

形态极具误导性：前半段（建卡组、建卡、点 Save）**全部 COMPLETED**，
却在最后一步红在「今日待复习 1 张」—— 报错指向「闪卡到期数算错了」，
真因是**这条 flow 根本没在闪卡页上跑完**（守卫是在「回到卡组页」那一刻
才把解锁屏弹出来的）。

⇒ 修法（`scripts/maestro-run.mjs`）：**夹具跑完后再解锁一次**，并复位回起点
路由。解锁失败就**响亮退出**，不拿一条「前提不成立」的 flow 去凑结果。

### 3.9 行节点被合并 ⇒ 选中了屏幕最底边那 12px ⇒ 触点打在导航栏上

email-browse 红在「点开一封信 ⇒ 详情页不出现」。失败现场里那封信**明明在**
（『Reddit，11小时前，…，未读』[28,527][1036,900]），而 Maestro 的日志显示
它实际点的是另一个节点：

```
Tapping on element: text=阿里云计算，22小时前，【阿里云】云安全中心周报，未读
                    bounds=[28,2388][1036,2400]     ← 整行只有 12px 在屏内
```

⇒ 触点落在 y=2394 ⇒ **打在底部导航栏上** ⇒ 详情页当然不出现。
真因是**行节点在无障碍树里是合并的**（发件人 + 相对时间 + 主题 + 摘要），
「相对时间」这个模式同时命中列表里每一行，而 Maestro 默认选中的不是首行。

⇒ 修法：`tapOn` 加 `index: 0`，锁定**树里第一个**匹配项（完整在屏内的首行）。
改完复跑 28 步全绿（含详情页、动作区、以及「正文不是原始 MIME」的负向判据）。

### 3.10 两条登录 flow 不能同批跑

`2026-10-02-real-login` 与 `login-gesture` 都要求**未登录态**。但前者
**会真的登录成功** ⇒ 同一批里跑在它后面的 `login-gesture` 起点已经是
已登录的 App ⇒ 它的第一条断言「用户名|Username 可见」直接红。

⇒ 两条登录 flow 必须**各自单独跑一次 harness**（`POCKET_SKIP_CDP_LOGIN=1`）。
本轮第一次把它们放进同一批，就是这么红的。

---

## 4. 本轮新增/修改的代码与配置

### 4.1 `scripts/maestro-run.mjs` —— monkey 恒失败，改用显式组件

实测（emulator-5556 / AOSP Android 14，force-stop 后跑）：

```
adb shell monkey -p <pkg> -c android.intent.category.LAUNCHER 1
→ 退出码 251、App 没起来（前台仍是 NexusLauncher、pidof 无 pid），
  而输出里既没有「No activities found to run, monkey aborted.」，
  也没有「Events injected: 1」—— 从输出看它只是「没干活」，没有可匹配的标记
```

⇒ 判据只能是「App 有没有真的在前台」，退出码与输出正则都不可靠。

后果两处：`flashcards-test-fixture.mjs` 直接 `sh(monkey)` 且无兜底 ⇒
恒定抛错 ⇒ **夹具整个断掉** ⇒ runner 把 flashcards-write **跳过**（现象是
「这条 flow 没跑」，与夹具数据、与产品功能都无关）；preflight 里 monkey
**恒定失败** ⇒ 每轮白跑一次注定失败的调用。

修法：显式组件 `am start -n <pkg>/.MainActivity` 为主路径 —— 不依赖
PackageManager 的隐式意图解析（宿主高负载把 SystemUI 压到 ANR 时隐式解析
会临时失效），实测冷启动一次就成（exit=0、前台立刻变成该包的 MainActivity、
pidof 有值）。

### 4.2 `scripts/maestro-run.mjs` —— 变量展开闭环（本轮新增）

| 改动 | 位置 | 说明 |
|---|---|---|
| `materializeFlowWithEnv` | 跑批主循环 | 跑前生成 `.maestro/.maestro-tmp-<name>.yaml`（前置区插 `env: POCKET_DEV_PASS / POCKET_MASTER`，mode 0600），跑完删除。**口令只经进程 env 与临时文件传递，不入库、不进命令行** |
| `scrubMaestroArtifacts` | 每条 flow 跑完 | Maestro 会把**展开后**的命令参数写进自己的 `maestro.log` / `commands.json`（实测口令明文就在里面），跑完按**精确值**替换成 `<redacted>` |

⚠️ 已知副作用：Maestro 用 flow 文件名做落盘目录名，所以注入 env 后的那轮
产物目录名会带 `.maestro-tmp-` 前缀，找失败现场时按这个前缀找。

⚠️ 另一个坑（实测）：`runFlow` 的相对路径按**当前 flow 所在目录**解析，
写 `.maestro/_set-master-password.yaml` 会被解析成 `.maestro/.maestro/…`
⇒ 解析期就报 `Parsing Failed`，且报错位置指向外层 `runFlow`、离真因很远。
子流引用必须用**裸文件名**（与 `_login.yaml` 一致）。

### 4.3 openpocket 后端改动清单

| 文件 | 改动 |
|---|---|
| `backend/internal/accjwt/`（新） | 手写 HS256 服务 JWT（claims 沿用 ACC 线上契约，nbf 回拨 30s） |
| `backend/internal/accruntime/ecosystem.go`（新） | `ListAgents` / `ListCanonicalTasks` / `ListProjects`（v2 REST 面） |
| `backend/internal/mcp/client.go` | `AccSession` 补齐 ACC 实际返回的顶层字段；新增 `ListAllSessions`（不带 task_id，limit 夹到 ACC 的 200 上限） |
| `backend/internal/server/acc_ecosystem.go`（新） | 4 个 ACC 读取路由（`/api/acc/{agents,tasks,projects,sessions}`） |
| `backend/internal/server/session_bundle.go` | `accSessionToRow` 改为**顶层字段优先、metadata 兜底** |
| `backend/cmd/pocketd/main.go` | 装配 ACC Runtime 客户端（Signer 形态） |

### 4.4 测试

- `backend/internal/accruntime/ecosystem_test.go`（新）：envelope 变体
  （`{"agents":[…]}` / `{"data":[…]}` / 裸数组）、外层对象认不出列表 ⇒ 空切片
  而非错误、HTTP 错误**不得**伪装成空列表、原始 JSON 透传不丢未来字段。
- `backend/internal/mcp/acc_session_fields_test.go`（新）：用**真实抓到的**
  ACC 响应锁死顶层字段映射 + 老 metadata 格式仍可解码。
- `backend/internal/mcp/acc_list_all_sessions_test.go`（新）：不得带 `task_id`、
  limit 夹到 200。
- `backend/internal/server/session_bundle_test.go`：顶层优先 + metadata 兜底。

`go test ./...`（本轮结束前复跑）：**只有 2 个包失败，全是 `TestSkipLedger_*`
台账治理测试**（`internal/notes` 与 `internal/server`），与本轮改动无交集 ——
已用「干净树（只有 origin/main）上同样失败」钉死。

---

## 5. 13 条主包 Maestro 流最终结果

跑批环境：**emulator-5556**（AOSP Android 14 / 1080×2400 / **en-US** /
WebView **113.0.5672.136**）· 后端 pocketd :18099 · ACC :4100/:4101 ·
agent-companion :12110（`0.1.0-e1cf36c.1792616171`）。

判定口径：**只有 flow 自己的命令全部 COMPLETED 才算绿**；红了就如实登记，
不放宽断言、不伪造绿灯。步数为该轮 Maestro 日志里的 COMPLETED / FAILED 计数。

| # | flow | 结果 | 步数 | 备注 |
|---|---|---|---|---|
| 1 | `smoke-login` | ✅ | 8 / 0 | 登录后落在 AI 首页 |
| 2 | `notes-crud` | ✅ | 35 / 0 | 本轮修：删掉会**导航回退**的 `hideKeyboard`（§3.5） |
| 3 | `tasks-crud` | ✅ | 29 / 0 | 含「新建任务」弹窗、任务 CRUD |
| 4 | `meetings-entry` | ✅ | 18 / 0 | 本轮加：跑完**停掉录音**（§3.6） |
| 5 | `messages-hub` | ✅ | 18 / 0 | 消息聚合时间线 |
| 6 | `more-grid-reach` | ✅ | 39 / 0 | 根因＝录音残留（§3.6），录音收尾后复验全绿 |
| 7 | `more-entries-open` | ✅ | 20 / 0 | 同上 |
| 8 | `email-accounts` | ✅ | 28 / 0 | 本轮修：页面就绪判据从**瞬时**改成**常驻**（§3.3 ⑥） |
| 9 | `email-browse` | ✅ | 28 / 0 | 本轮修：`tapOn` 加 `index: 0`（§3.9）；含「正文不是原始 MIME」负向判据 |
| 10 | `flashcards-write` | ✅ | 37 / 0 | 本轮修：夹具重启 App 后**再解锁一次**（§3.8） |
| 11 | `settings-llm-gateway` | ❌ | 17 / 1 | **红在环境前置缺失**：网关地址与模型列表都已渲染，唯一缺 API Key（§6.1） |
| 12 | `2026-10-02-real-login` | ✅ | 15 / 0 | 单独跑（`POCKET_SKIP_CDP_LOGIN=1`）；本轮修三处判据（§3.7） |
| 13 | `login-gesture` | ✅ | 17 / 0 | 单独跑（`POCKET_SKIP_CDP_LOGIN=1`）；**必须单独跑**（§3.10） |

**12 绿 / 1 红。** 唯一那条红不是产品缺陷，而是**本机缺 LLM 网关凭据**
（§6.1），失败现场已如实登记。

⚠️ 这张表是**多轮拼出来的**，不是一轮跑完的：首轮 11 条批次有 6 条红，
逐条定位并修掉装置缺陷后复跑（§3.5–§3.10）才得到上表。
每一格都对应 `~/.maestro/tests/<时间戳>/` 下的现场目录，步数可复核。

---

## 6. 如实登记的未通过项与前提

1. **`settings-llm-gateway` 红在环境前置缺失**：失败现场树显示网关地址
   `https://llm.kxpms.cn/v1` **已渲染**、可用模型列表 **已拉到**
   （`glm-5.3` / `minimax-m3` / `kimi-k3` / `+6`），状态徽标是
   **`Not Configured`** —— 唯一缺的是 API Key。本机没有可用的 LLM 网关
   凭据（本机 llm-gateway 的 `LLM_GATEWAY_SECRET_KEY` 对本地与远程网关都
   返回 401 `invalid_key`；ACC 侧无 `LLM_GATEWAY_API_KEY`）。
   **没有为通过测试而放宽断言、没有伪造绿灯。**
2. **`login-gesture` / `2026-10-02-real-login` 需要未登录态**：这两条断言登录页
   文案，而 harness 的 preflight 已真实登录 ⇒ 起点是 `#/ai`、登录页根本不出现。
   用 `POCKET_SKIP_CDP_LOGIN=1` 单独跑（preflight 已有该开关）。
3. **邮箱两条 flow 需要播种账户**：本地库 `email_accounts = 0` 时，收件箱页
   **不渲染同步状态行**（flow 的页面就绪信号）。用
   `scripts/seed_email_accounts.sh` 播种 5 个内置账户（凭据只从
   `envs/loader.sh` 经环境变量读，不入库）后 ⇒ 5 个账户 IMAP 连通、
   同步到**真实邮件**（播种当时 117 封，持续同步到本轮收尾时 **323 封**，
   `GET /api/emails` 实测），两条 flow 全绿。
4. **本机没有 USB Android 真机**（§3.1）；本轮跑批用的模拟器中途掉过一次
   （已用同一 AVD `pocket-test` 在 5556 端口重启；冷启动导致输入法首启很慢，
   触发了 §3.5 那次 `hideKeyboard` 竞态）。
5. **D 态（首次安装建主密码）这条路径本轮未被 Maestro 覆盖**（§3.4）：
   本机 WebView 113 的无障碍树不暴露该弹窗；该路径由 harness 的 preflight
   以 CDP 覆盖并实测通过。

---

## 7. 提交与推送

openpocket 分支 `fix/acc-runtime-service-jwt`（origin 已同步）：

| 提交 | 内容 |
|---|---|
| `d7166f69` | feat(acc): 装配 ACC Runtime Control 客户端（HS256 服务 JWT） |
| `72765975` | fix(maestro): 真机 rig 两处阻断（系统权限弹窗 + 主密码填值不生效） |
| `d1cf33b2` | fix(maestro): 复位落回纯路由 —— 消掉「点当前 tab 把刚开的弹窗弄没」的竞态 |
| `45ca7b70` | fix(maestro): 补回被覆盖的 harness 修复 + 录音权限预授 + 复位落回纯路由 |
| `67909fc4` | feat(acc): 补 ACC 生态读取面，并修复「ACC 会话标识整片丢失」 |
| `78faeb94` | fix(maestro): 判据补英文分支 + 启动改用显式组件（三处真缺陷） |
| `a2922c36` | fix(maestro): 补 tasks-crud 残留清理夹具，并修 email-browse 的邮箱英文分支 |
| `596f4b28` | fix(maestro): 闪卡「保存」点的是被键盘遮住的 submit 按钮 —— 改点顶栏那个 |
| （本轮）`fix(maestro): 口令传递闭环` | 跑前给 flow 注入 env 块（`${VAR}` 才真的展开）、跑后抹掉产物里的明文、夹具跑完后再解锁一次本地库 |
| （本轮）`fix(maestro): 真机登录 flow 的三处判据错误` | 落点集合去掉「密码登录」、D 态弹窗处置、收尾硬判据 |
| （本轮）`fix(maestro): notes-crud 的 hideKeyboard 会导航回退` | 去掉该步骤 |
| （本轮）`fix(maestro): email-accounts 的页面就绪判据是瞬时的` | 换成常驻的分类 chip |
| （本轮）`fix(maestro): email-browse 点歪到屏幕最底边那一行` | `tapOn` 加 `index: 0` |
| （本轮）`fix(maestro): meetings-entry 开的录音从不收尾` | 收尾停掉录音并断言 dock 翻成「开始录音」 |
| （本轮）`docs: round50 测试报告` | 本文件 |

agent-companion `main`：`e9f0d44`（跨源重复会话）、`516fac0`（live-e2e 在
macOS 跑不起来）已推送；本轮又把线上 daemon 下发到 `e1cf36c`。
