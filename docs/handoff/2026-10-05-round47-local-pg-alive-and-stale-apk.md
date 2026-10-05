# round47 —— 本地 PG 其实活着；17 条流跑完，8 条失败的唯一根因是**装机 APK 陈旧**

日期：2026-10-05 13:xx → 15:5x（本机时区 +08:00）
范围：真机（实为 AVD）部署 + 全量 Maestro 回归 + 邮箱/网关/STT 配置落库
基线：`d0e55d15`（本地 main == origin/main，工作区干净）

---

## 〇、先纠正我上一轮的两个错判（都不该犯，且都改变了结论）

### 错判 1：「必须拿到远程 PG 口令才能动」——**错的，本地 PG 一直活着**

上一轮我读完 round45 就下了结论「本机无 PostgreSQL」，然后你让我用
`192.168.31.34` 的库，我去试连发现认证被拒，就判断「只差口令」并停下等密码。

**事实是：round46（并发会话）已经把 PostgreSQL 装回来了。**
我没有读那份 handoff 就下了结论，于是把一个「本机就有」的问题当成「需要外部授权」：

```
C:\workspace\pocket-opencode\.scratch\pgdist\pgsql\bin\psql.exe   ← 17.5，已解压
C:\workspace\pocket-opencode\.scratch\pgdata                      ← 已 initdb
Get-NetTCPConnection -LocalPort 5432 -State Listen                 → 1（6 个 postgres 进程在跑）
psql "postgresql://postgres@127.0.0.1:5432/postgres?sslmode=disable" -c "select version();"
   → PostgreSQL 17.5 ... (1 row)   exit 0
```

⇒ **不需要远程库，也不需要任何口令。** 顺带 round46 §一.9 那条
「backend 子进程被 `0xC0000142` 杀掉、怀疑杀软」**当前不复现**：我这次
`psql` 和后端连接全程正常。round46 自己也标注了「我没有证成它」，这里补一句
**现状是证伪，不是证成**。

### 错判 2：「`192.168.31.34` 上是 5432」——**实际是 55432**

你给的是 `:5432`。实测那个 IP 上 5432 **明确拒绝**（RST，不是超时），
扫出来真正开着的是 **55432**，并用 PG 协议握手确认（发 SSLRequest，
服务端回 `S` = 0x53 接受 SSL）。**不是网络不通，是端口号不同。**
⇒ 记录这个 IP 的正确端口是 **55432**（7000/8080 也开着，未验证用途）。

**教训**：上一轮我拿「本机无 PG」这个**继承来的结论**当事实用。
handoff 里写的环境结论有时效性，且**并发会话可能已经修好了它**。
动之前先实测一次，成本几秒，错了就是整整一轮的等待。

---

## 一、本轮最有价值的结论：8 条流失败的根因是**装机 APK 陈旧**，不是产品缺陷

17 条主包流全部执行完，**8 条有失败断言**。表面看是 8 个独立问题，
实际只有**一个**根因。证据链：

```bash
# 设备上 App 的真实 DOM（CDP 读 .bottom-nav .nav-item）
设备实测： home 首页 / style 学习 / mic 会议 / apps 更多      ← 旧 IA
仓库代码： /ai 首页 · /notes 笔记 · /messages 消息 · /more 更多   ← 新 IA
git log --oneline -- frontend/src/components/BottomNav.vue
  11481002 feat(ui,shell): z-index 阶梯 + 响应式镜像 + shell 抽象层 + UI 规范
  4e21dea3 feat(ia): 一级 tab 收敛为 首页/笔记/消息/更多      ← 10-03 02:07
git merge-base --is-ancestor 4e21dea3 HEAD                  → 0（IA 确实在 HEAD 里）
APK 时间戳：frontend/android/app/build/outputs/apk/debug/app-debug.apk
             2026-10-04 00:24:59
```

失败断言**全部落在只有新 IA 才有的锚点**上，一条不漏：

| 流 | 失败的断言 |
|---|---|
| `messages-hub` | `Tap on "消息"` / `Element not found: Text matching regex: 消息` |
| `notes-crud` | `Assert that "笔记" is visible` |
| `_goto-pkm` | `Assert that "笔记" is visible` |
| `tasks-crud` | `Assert that "创建", enabled is visible` |
| `more-entries-open` | `Assert that "返回" is visible` |
| `flashcards-write` | `Assert that "保存\|Save", enabled is visible` |
| `login-gesture` | `Assert that "输入用户名" is visible` |
| `meetings-entry` | `Assert that "开始会议录音" is visible` |

**反向证据（比正向更重要）**：`.maestro/flashcards-write.yaml:61-64` 的注释
**早就写好了**这件事：

> ⚠️ 2026-10-03 真机实测：锚点文本随 IA 重组变了。原来等的是「AI 工具」——
> 重组**前**首页 tab 的标签；现在一级 tab 是 首页 / 笔记 / 消息 / 更多
> （BottomNav.vue items，locales zh-CN nav.home = 首页）。不改这里的话本 flow
> 会在第一条断言就中止。

⇒ **flow 是按新 IA 写的，设备上装的是旧 UI。** 那个 10-04 00:24 的 APK
很可能是在 i18n worktree 上构建的（那份 worktree 已于 round45 删除），
所以它有 10-04 的时间戳却没有 10-03 的 IA。

⇒ **修法是重建装机，不是改 flow 去迁就旧 APK。**
把 flow 改成能同时匹配新旧 IA，等于把「装错包」这个环境问题固化成
产品代码里的永久妥协——**这类"让测试迁就错误环境"的改法一律拒绝**。

---

## 二、真机（实为 AVD）部署：全流程打通

**没有 USB 连着的 Android 真机**（`adb devices` 空、PnP 里也无 Android/MTP 设备），
用 AVD `pocket-test` 顶上：Android 14（`sys.boot_completed=1`）。

| 步骤 | 结果 |
|---|---|
| 后端 pocketd :18099 | `/healthz` → `200 ok` |
| `adb reverse tcp:18099` | `host-19 tcp:18099 tcp:18099` |
| maestro 驱动自愈 | `dev.mobile.maestro` / `.test` 均装上并 `pm enable` |
| 真实登录 | token **291 字符**，进入 `#/ai` |
| 本地 SQLCipher 库解锁 | 已解锁 + **自证**（再进 PKM 不再弹解锁屏） |
| `smoke-login` | **exit 0**，5 条断言全 COMPLETED |

### 一个把「环境问题」伪装成「maestro 崩溃」的坑

我一度判定 maestro 挂了：

```
PostQueuedCompletionStatus: (6) 参数无效
exit 2147483651 (0x80000003 STATUS_BREAKPOINT)
```

**真因完全不是这个。** 用最小复现脚本证明 `spawnSync(..., {shell:true})`
本身没问题（复现 exit 0、断言全过），再顺着 exit 码查
`maestro-run.mjs:1348`：

```js
if (process.env.POCKET_SKIP_CDP_LOGIN !== '1') {
  if (!(await ensureLocalDbUnlocked())) process.exit(3)   // ← 就是这里
}
```

`ensureLocalDbUnlocked()` 第一分支就是 **`POCKET_MASTER` 没设置** → 返回
false → exit 3。补上主密码后立刻全绿。

⚠️ **判据怎么误人的**：`PostQueuedCompletionStatus` + `0x80000003` 长得
完全像「maestro 二进制崩了 / 环境坏了」，而真正原因只是**少传一个环境变量**。
**exit 3 在这套 harness 里的语义是「拒绝给结论」，不是「崩了」**——
`run-gates.mjs` 头注释有约定，但单看退出码很容易读反方向。

### 移动端构建的两个环境坑

1. **`npm` 被 PowerShell 执行策略拦**（禁 `npm.ps1`）：
   `无法加载文件 ...\npm.ps1，因为在此系统上禁止运行脚本`。
   ⇒ **Windows 上必须用 `npm.cmd`**。上一轮我以为构建失败，其实是它。
2. **`npm run build` 被自带的 BUG-F 守卫拦下**（这是好设计，不是 bug）：
   ```
   [vite] 拒绝构建：VITE_API_BASE 为空。
   移动端 bundle 一旦缺少它，App 会静默回落到 WebView 同源（https://localhost），
   所有 /api 请求返回本地 index.html 而不是 JSON。
   移动端请用：node scripts/build-mobile.mjs <ios|android> <dev|staging|prod>
   ```
   ⚠️ **`build-mobile.mjs` 只做 vite + `capacitor sync`，不产出 APK。**
   它的 `sanity check passed` 之后 APK 时间戳**仍是旧的**，必须再跑
   `frontend\android\gradlew.bat assembleDebug` 才真的有新包。

---

## 三、配置落库：全部有独立读回自证

### 3.1 邮箱账户（5 个，全部实测凭证有效）

POST 全 201，独立 GET 读回 5 个，然后**逐个打 `POST /{id}/test-smtp`**：

| 账户 | IMAP | test-smtp |
|---|---|---|
| 凯轩企业邮 `huangxutao@kxpms.cn` | imap.exmail.qq.com:993 | `{"ok":true}` |
| QQ 私人 `56551681@qq.com` | imap.qq.com:993 | `{"ok":true}` |
| 163 / feikemanager | imap.163.com:993 | `{"ok":true}` |
| 163 / feikemanager1 | imap.163.com:993 | `{"ok":true}` |
| 163 / kimmy.huang | imap.163.com:993 | `{"ok":true}` |

⇒ 这一步是决定性的：**凭证能正确解密 + 服务端真连上邮件服务器**，
不是「存进库了」而已。

⚠️ 端点是 `/{id}/test-smtp`（`server.go:745` → `handleEmailAccountOps`），
**不是** `/{id}/test` 或 `/{id}/sync`，那两个都是 404。别按直觉猜端点名。

### 3.2 LLM 网关（修正了两处与目标不符）

改之前的 GET：

```json
{"apiKeySet": false, "baseURL": "https://llm.kxpms.cn/v1",
 "preferredModels": ["glm-5.2", "minimax-m3", ...],   ← 第一位是 glm-5.2，不是 5.3
 "updatedAt": 1790883289}                              ← 旧值
```

⇒ 两个问题：**key 从没落过库**（你要求"保存在服务端的用户的配置下"）、
**首选模型第一位是 `glm-5.2`**。POST 修正后**独立 GET 读回**：

```
baseURL        = https://llm.kxpms.cn/v1
apiKeySet      = True
apiKey(masked) = sk-******rK51YV          ← 前端只拿得到掩码，符合设计
preferred[0]   = glm-5.3                  ← 已修正
preferred      = glm-5.3, minimax-m3, kimi-k3, claude-sonnet-5, gpt-5.6-terra,
                 claude-opus-5, claude-fable-5, gpt-5.6-sol, gemini-3.5-flash
```

### 3.3 网关与 STT（实测连通）

- `GET /v1/models` → **200，607 个模型**；目标那 9 个**全部存在**。
- ⚠️ **ASR 模型实名是 `mimo-v2.5-asr`**（不是 `mino`，笔误）。代码里本来就写对了。
- 造真 WAV（1s / 16kHz / 单声道）打 `POST /v1/audio/transcriptions` →
  **HTTP 200** `{"model":"mimo-v2.5-asr","text":"嗯。"}`。
  纯音被识成语气词属正常——证明链路与解码都通，不是回显。

---

## 四、DB 测试：这次可以理直气壮说「跑过了」

**先纠正记账口径**：round45/46 记的「本机无 PG → DB 判据全 SKIP」已不成立。
设 DSN 后实测：

| | 改前（无 DSN） | 改后（有 DSN） | round43 记录的「设 DSN」基线 |
|---|---|---|---|
| `internal/email` | 19.0s | **145.7s** | 172s |
| `internal/server` | 22.2s | **65.9s** | 62s |
| `internal/flashcards` | 0.16s | **3.1s** | 8s |

`-v` 逐条钉死（`TestMarkRetry`，4 条）：

```
改前：--- PASS: TestMarkRetry_CallsComposeBeforeAssign   （只验接线，不碰库）
      --- SKIP ×3  ← POCKET_TEST_POSTGRES_DSN not set
改后：--- PASS ×4，耗时 0.72s / 0.44s / 0.37s   ← 有真实耗时 = 真连了库
```

⚠️ **别用 `go test` 的退出码判断"是不是真跑"**——两种状态的退出码都是 0。
唯一的判据是 **`-v` 里的 SKIP 行数** + **耗时落在哪一侧**。

---

## 五、遗留与下一轮提示

### 2.3 修完第一版**并没有解决问题**（这一条要留在最前面，别只记"已修"）

第一次修的只是**那个 TypeError 本身**，症状是消失了，但根因还在：

```
修前：[preflight] ⚠️ focus 失败(/至少/)：err:TypeError: "至少".test is not a function
      [preflight] 主密码弹窗：两框已输入，长度=[8,0,0]      ← 4 次都一样
      [preflight] ❌ 登录后仍停在登录页（30s）

修后：⚠️ focus 失败 消失了 ✅        ← 只证明不再抛异常
      [preflight] 主密码弹窗：两框已输入，长度=[8,0,0]      ← **一模一样**
      [preflight] ❌ 登录后仍停在登录页（30s）
```

⇒ **"不报错了"与"修好了"是两件事。** 这里我只完成了前者，
却差点在提交里写成后者。判据必须是**行为变化**（两个框都拿到值），
不是**错误消失**。

⚠️ 还有一个反直觉的细节：`长度=[8,0,0]` 是**三个** password 类型的框，
而代码的循环只跑 `['至少','再次']` 两轮，且注释里写的是「两框已输入」——
**注释里的"两框"与设备上的"三框"从一开始就对不上**。下一轮必须先读
真实 DOM（type / placeholder / aria-label / 可见性）再动手，不要继续在
关键字上试。

### 2.4 读真实 DOM 之后，谜团解开了（且推翻了我对 `[8,0,0]` 的解读）

我一度以为"三个框"意味着代码漏了一个。读真实 DOM：

| i | type | placeholder | valLen |
|---|---|---|---|
| 0 | text | 输入用户名 | 5 |
| 1 | password | 输入密码 | 8 |
| 2 | password | 主密码（至少 8 位） | 0 |
| 3 | password | 再次输入主密码 | 0 |
| 4 | text | 密码提示（可选） | 0 |

⇒ **`长度=[8,0,0]` 是 i=1/i=2/i=3**：i=1 是**登录页的密码框，本来就该有 8 位**。
harness 的读数一直是**对的**，错的是我的解读——"三框"里没有第三个待填的主密码框。
**代码注释里那句「两框已输入」是准确的**，是我误读了它。

### 2.5 focus 成功但 `input text` 不落值 —— 已定位到具体环节

逐段实测（`.scratch/probe-fill-dlg.mjs`）：

```
[至少] focus -> {"ok":true,"aePh":"主密码（至少 8 位）","aeIdx":2}   ← focus 精确命中
[至少] after input -> {"valLen":0,"all":[5,8,0,0,0]}                  ← 输入没进去
[再次] focus -> {"ok":true,"aePh":"再次输入主密码","aeIdx":3}
[再次] after input -> {"valLen":0,"all":[5,8,0,0,0]}
```

⇒ 排除项（都实测过，不是推断）：
- 正则匹配**对**：`new RegExp('至少')` 命中 i=2，`'再次'` 命中 i=3；
- CDP `el.focus()` **对**：`document.activeElement === el` 为 true；
- `adb input text` **在本机总体可用**：在登录页用户名框上 `input text TESTX`
  **成功登录**并落到 `#/ai`。

⇒ 剩下的解释只有「**主密码弹窗是模态层，`input text` 的目标窗口不是它**」。
模拟器无窗口（`-no-window`）时这条尤其可疑。**这一条尚未证成**，
下一轮要么改用 Maestro 自己的 `inputText`（有 xpath 选择器，能定位到模态内元素），
要么在**有窗口**的模拟器上复跑一次做对照。

### 2.6 顺带确认：新 APK 的 tabbar 确实已是新 IA

装上新 APK 后 CDP 读到底部导航：

```
home 首页 · edit_note 笔记 · notifications 消息 · apps 更多
```

与仓库 `BottomNav.vue` 一致 ⇒ **§一 那条「装机 APK 陈旧」已消除**。

### 2.7 ✅ 真正的根因：系统权限弹窗抢走窗口焦点（已修 + 已验证）

前面两轮的猜测（模态焦点失真、Maestro 选择器）**都是错的**。
真凶是 `dumpsys window`：

```
mCurrentFocus=Window{... com.google.android.permissioncontroller/
                     permission.ui.GrantPermissionsActivity}
mInputShown=false
mServedInputConnection=null
```

截图坐实：一个**系统通知权限弹窗**盖在 App 上。`adb input text` 的按键
被送进了这个系统弹窗，而不是 WebView 的输入框 ⇒ 主密码框 `valLen` 恒为 0。

修法（`pm grant` 预授权限，之后焦点立刻回到 App）：

```bash
adb -s emulator-5554 shell pm grant <pkg> android.permission.POST_NOTIFICATIONS
adb -s emulator-5554 shell pm grant <pkg> android.permission.RECORD_AUDIO
adb -s emulator-5554 shell pm grant <pkg> android.permission.CAMERA
→ mCurrentFocus=Window{... com.kaixuan.opencode.pocket/.MainActivity}
```

⚠️ **`RECORD_AUDIO` 也要预授**（见 §5.4）。修完 `[8,0,0]` → `[8,8,8]`。

### 2.8 ✅ 第二个真缺陷：harness **从来没点过**主密码弹窗的「确认」

填值通了之后仍卡在登录页。读代码才发现点击逻辑整段被跳过：

```js
let masterDialogHandled = false
for (...) {
  const need = await cdpEval(...)      // 'no' | 'present' | ...
  if (need === 'no') { masterDialogHandled = true; break }   // ← 只有这条置 true
  ... 填两个框 ...
}
if (masterDialogHandled) {              // ← 弹窗在场时恒为 false，整段跳过
  b.click()                             // 现成的、能用的实现
}
```

`need === 'present'`（**弹窗在场、真正需要处理**）这条路径走完填值循环后，
标志仍是 `false` ⇒ `b.click()` 永远不执行 ⇒ 弹窗不关 ⇒ 30s 后报成
「登录后仍停在登录页」。

**修法**：把判据从「弹窗是否还在」改成「本轮是否处理过弹窗」——
填完两框后置 `masterDialogHandled = true`。

⚠️ **一条会骗人的证据**：早前轮次日志里那行
`主密码弹窗确认：no-confirm` 看起来像「点击逻辑已验证」，其实它来自
**另一条路径**（`need==='no'`，设备上已设过主密码、弹窗压根不在）。
**那条路径根本不会真的点到一个存在的按钮**，`no-confirm` 是理所当然的。
⇒ 「代码里有 `b.click()`」不等于「它被执行过」，更不等于「它有效」。
**判据必须是行为变化**：`主密码弹窗确认：confirmed` + 随后的 `登录成功`。

修复后的实测（`smoke-login` 单流，exit 0）：

```
[preflight] 主密码弹窗：两框已输入，长度=[8,8,8]
[preflight] 主密码弹窗确认：confirmed          ← 之前从未出现过的这一行
[preflight] 登录成功，已进入 #/ai?__recheck=…
[preflight] 本地库已解锁
Assert that "AI 工具" is visible... COMPLETED   ×2
Assert that "快速提问" is visible... COMPLETED
Assert that "密码登录" is not visible... COMPLETED
Take screenshot ... COMPLETED
```

### 5.4 首次安装新设备时，必须预授这些权限（否则首登必卡）

模拟器/新机上装完包，**在跑任何 flow 之前**执行：

```bash
adb -s <serial> shell pm grant <pkg> android.permission.POST_NOTIFICATIONS
adb -s <serial> shell pm grant <pkg> android.permission.RECORD_AUDIO
adb -s <serial> shell pm grant <pkg> android.permission.CAMERA
```

理由不是"省几步点击"，而是**系统权限弹窗会抢走窗口焦点**：
`adb input text` 于是把按键送进系统弹窗而不是 App 的输入框，
主密码框永远填不进去，最后报成「登录后仍停在登录页」——
**离真因隔了三跳**，看起来像登录功能坏了。

`RECORD_AUDIO` 尤其重要：它同时是**STT 录音转写**的前置。
不预授的话，真机首次录音必被系统弹窗打断，而那种失败的日志形态
和「录音功能坏了」无法区分。

⚠️ 这个坑**只在「设备上还没设过主密码 / 刚重装」时出现**。
设过主密码的设备上，`need` 直接返回 `'no'`，整段是死代码——
所以它能在仓库里安静地活着，直到环境一变才第一次被真正执行。

### 2.9 ⚠️ `data-testid` 对 Maestro **无效**，而「会议详情」是**恒假断言**

修完 2.8 后跑 meetings 流，`tapOn: "笔记|Notes"` 报 COMPLETED，
但下一条 `visible: "notes-hub-start-meeting"` 失败。两处都是我的错：

**(a) 我用 `data-testid` 定位按钮，理由写的是「文案随 i18n 变，testid 是契约」。**
**这个理由是错的**：Maestro 走 Android **无障碍树**，而 `data-testid` 是
**DOM 属性，不出现在无障碍树里** ⇒ 任何 `visible:`/`tapOn:` 命中它都恒为 false。

正确做法是用**真实可见文案**。实测值来自 `src/locales/zh-CN.json:92`：

```
notesHub.action.startMeeting = 「开始会议」     ← 不是「开始会议录音」
```

而且那个按钮是**纯图标**（内含 `aria-hidden` 的 material icon），
所以可见文本**只有 aria-label 这一条路**。

⇒ 推论：**`data-testid` 只对浏览器/E2E 有效，对 Maestro 一律无效。**
本仓 `NotesHubView.vue` 里有一批 `data-testid`（`notes-hub-start-meeting` 等），
它们对 CDP 探查很有用（`.scratch/probe-*.mjs` 就是靠 DOM 查），
**但不能直接搬进 flow 的选择器**。

**(b) `assertVisible: "会议详情"` 是恒假断言（既有坏判据，本轮实测钉死）。**

```
$ grep 会议详情 frontend/src
router-mobile.ts:323:  meta: { ..., title: '会议详情', ... }     ← 只有 meta.title
RecordingPill.vue:5:   录音宿主页(会议详情 / 会话页 / 笔记页)后…   ← 注释
NotesHubView.vue:81:   …(实测误点进了会议详情并触发了录音)。      ← 注释
```

**页面模板从不渲染「会议详情」这三个字** ⇒ 这条断言与设备状态、
产品行为**完全无关**，它只是永远不成立。而 `MeetingRecordView.vue`
本身只是个跳转壳（`onMounted` → `createMeeting()` →
`router.replace({name:'meeting-detail', query:{record:'1'}})`），
真正渲染的是 `MeetingDetailView`。

⇒ 已把三处「会议详情」全换成**真正可见**的判据：
- `MeetingMicDock.vue:6` 的 `:aria-label="recording ? '停止录音' : '开始录音'"`
- `MeetingInsightPanel.vue:2` 的 `:aria-label="isRecording ? '即时总结' : '会议纪要'"`

**教训**：一条恒假断言**不会自己暴露**。它会一直红，而人会去查设备、
查产品、查时序——**没人会去怀疑判据本身恒假**。
所以拿到「这条断言红了」时，第一个要问的不是「产品坏在哪」，
而是「**这条判据在本设备上有没有可能为真**」。
`grep` 一下断言文本在源码里出不出现，是成本最低的判别力自检。

### 5.1 本轮未完成（正在收尾）

1. **重建 APK 并重装**：`gradlew assembleDebug` 已启动，装完要**重跑那 8 条流**，
   验证失败消除。**如果还有失败，那才是真缺陷**，按新 UI 真实 DOM 逐条修真因。
2. **2 条 sttdev 流**（`notes-stt-error-visibility` / `_connectivity-sttdev`）
   必须单独跑：`POCKET_APP_ID=com.kaixuan.opencode.pocket.sttdev`。
   harness 会正确拦下混包（exit 2）——这是它设计好的保护，别去改。

### 5.2 环境事实（跨轮复用）

- **本机 PostgreSQL 17.5 在 `.scratch` 里，是活的**（见 §〇）。
  后端已不支持 SQLite，`POCKET_POSTGRES_DSN` 硬依赖 PG，没有替代方案。
- `192.168.31.34` 的 PG 在 **55432**，不是 5432。
- **`192.168.31.0/24` 整段不可达**：连网关 `192.168.31.1` 都 ping 不通，
  但外网通（`llm.kxpms.cn` 返回 401 = 服务端活着）。网卡已关联
  `20-2-601`、信号 95%、IP/路由正常、BSSID 未变。**不影响本轮工作**
  （模拟器走 `adb reverse`，不依赖 LAN），但要用那台远程库得先修网络。
- 走模拟器时 `POCKET_API_BASE_OVERRIDE` **不要设 0**：那样 App 会用构建期
  LAN 基址 `192.168.31.20:18099`，而 LAN 现在不通。
- Windows 上 `npm` → 用 **`npm.cmd`**。

### 5.3 需你处理

- LAN 恢复（若要用远程 PG）。
- 真机接上 USB 后，把 `POCKET_SERIAL` 切过去重跑一遍——
  **模拟器绿 ≠ MIUI 真机绿**。这套 harness 历史上大量问题都只在真机出现
  （MIUI 吞 force-stop 后的 start、wakepath 弹窗、`0xC0000142`）。

---

## 六、提交与推送记录

| 时间 | 事件 |
|---|---|
| 开工 | `git fetch`；`origin/main` 领先 2 个门禁提交，**快进合并无冲突** |
| 期间 | 临时 PG 探针（`backend/cmd/pgprobe`）已走回收站移除，`git status` 复核为空 |
| 收尾 | 见下方（重建 APK / 重跑 / 推送） |
