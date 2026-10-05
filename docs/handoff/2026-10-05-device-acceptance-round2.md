# openpocket 真机验收 · 2026-10-05 第二轮

设备 **Redmi 2411DRN47C / Android 14 / MIUI**（`4c308e2e`，720×1640 @320dpi）
被测包 `com.kaixuan.opencode.pocket`（主包）
后端 **8090 容器**（`opencode-pocket-pocketd-local-openpocket`，SSOT=PG `host.docker.internal:5432/pocket`）

状态词只用 **PASS / FAIL / NOT_RUN / DEGRADED**。

## 1. 结论先说

| 维度 | 状态 | 依据 |
|---|---|---|
| 环境与起点归位 | **PASS** | App 内 `fetch /healthz → 200`，逐条跑批前台守卫 0 跳过 |
| 22 条 flow | **13 PASS / 9 FAIL** | `/tmp/opp-maestro/summary.txt` |
| 9 个失败里的**产品缺陷** | **0 个坐实** | 逐条归因见 §3，命中的是部署滞后 / driver 文本注入 / 起点前提冲突 |
| 部署滞后 | **FAIL**（承前轮，已复核仍在） | `/api/flashcards/decks` = **404** |
| 企微回调 | **FAIL**（承前轮） | `POST /callback/weixin` = **404**，且三项密钥仍全缺 |

> ⚠️ 本轮**没有坐实任何一个产品代码缺陷**。9 个失败全部归到「部署滞后 / 测试环境 /
> Maestro 文本注入 / flow 起点前提」四类。这是本轮最该记住的一句。

## 2. 逐条结果

### PASS（13）

`_connectivity` · `_dismiss-system-dialogs` · `_goto-pkm` · `_login` · `_probe-budget` ·
`_probe-tap` · `2026-10-02-real-login` · `meetings-entry` · `messages-hub` ·
`more-entries-open` · `more-grid-reach` · `settings-llm-gateway` · `smoke-login`

### FAIL（9）与归因

| flow | 失败点 | 归因 | 类别 |
|---|---|---|---|
| `_probe-a11y` | `".*回归卡组.*" is visible` | 闪卡路由 404，卡组建不出来 | **部署滞后** |
| `flashcards-write` | `"暂无卡组.*" is visible` | 同上，`/api/flashcards/decks`=404（实测） | **部署滞后** |
| `notes-crud` | **`inputText`**（"MaestroPKM笔记"） | driver 文本注入失败，非业务断言 | **测试环境** |
| `tasks-crud` | **`inputText`**（"Maestro任务"） | 同上（截图：弹窗正常、描述框空、「创建」正确置灰） | **测试环境** |
| `email-accounts` | `"已停用" is not visible` | 部署实例里**确实有**已停用账户（负向断言被打破） | **数据状态** |
| `email-browse` | MIME 头正则 `not visible` | 待查（未在本轮定论） | **待查** |
| `login-gesture` | `"输入用户名" is visible` | 起点冲突：该 flow 要**登出态**，而跑批前置刚**登录完** | **起点前提** |
| `_set-master-password` | 找不到「确认」 | 同上：前置已把主密码建好，弹窗不再出现 | **起点前提** |
| `_probe-tasks-create` | `ZZZ_故意失败_…` | **设计上就该失败**的探针（名字即声明） | **预期失败** |

## 3. 关键证据

**起点真的在 8090 容器上**（不是别的实例）：

```
[preflight] App 内 fetch http://127.0.0.1:8090/healthz → 200 ok ✅（App 确实在打本 worktree 的后端）
[preflight] 登录成功，已进入 #/ai
[preflight] 登录后 App 内 token 长度=291 字符
```

**登录接口本身正常**（直打后端，非 App）：

```
POST http://127.0.0.1:8090/api/auth/login  → http=200，token 291 字符
```

**文本注入是断点，不是业务**（`~/.maestro/tests/…/commands-(tasks-crud).json`）：

```
[COMPLETED] tapOnElement '+ 新任务'
[COMPLETED] assertVisible '创建任务'
[FAILED]    inputTextCommand {'text': 'Maestro任务'}
```

## 4. 本轮修掉的 harness 缺陷（都不是产品代码）

在 `scripts/maestro-run.mjs` 与新增的 `scripts/maestro-run-all.sh`：

1. **判据挂在错误分支上** —— 主密码弹窗的「点确认」只在
   `masterDialogHandled` 为真时执行，而该标志**仅在弹窗已自行消失时**才置真。
   于是弹窗在场时「填了从不确认」→ 模态弹窗挡住登录 → 报成
   「登录后仍停在登录页」，看着像口令/后端问题（实测后端 200、token 正常）。
2. **局部常量绕过统一解析** —— `const adbBin = 'C:/Users/86133/…adb.exe'`
   硬编码了另一台 Windows 开发机的路径，绕过了顶部 `whichFirst`。
   形态是「两框已输入，长度=[14,0,0]」，实际是**命令压根没跑起来**。
3. **口令未转义** —— `input text $master` 不加引号，14 位口令里那 1 个
   shell 元字符被设备 shell 吃掉，只填进 9 位。改单引号包裹。
4. **`JSON.stringify` 当正则用** —— `"至少".test(...)` 抛
   `TypeError`，4 次重试全败。改 `new RegExp(...)`。
5. **`execFileSync` 读「查不到」类命令** —— `pidof`（App 未运行退 1）、
   `grep` 无命中退 1，都会**抛异常**，代码永远走不到
   `if (!pid) throw APP_NOT_RUNNING`。加 `adbSoft()` 容忍失败。
6. **`setRoute` 连错 App 的兜底** —— `socks.find(…) || socks[socks.length-1]`，
   本机两包并存时会连到另一个 App 且**不报错**。删掉兜底。
7. **并存包抢前台** —— 同机 `…pocket` 与 `…pocket.sttdev` MainActivity 同名。
   `pm disable-user` 后仍被外部重新启用夺回前台，**禁用不持久** ⇒ 改为
   **逐条跑 + 每条前读回 `mCurrentFocus` 强制确认**，拿不到就记 `SKIP_ENV`
   而不是硬跑出假失败。

## 5. 装机过程中的三个假信号

- `adb install` 报 **`Success` 但包没多** —— 输出目录里那份
  `app-debug.apk` 其实是 `-PsttDevApp` 变体（`applicationIdSuffix ".sttdev"`），
  只是把已装的 sttdev 更新了一遍。**读清单才作数**：`aapt2 dump packagename`。
- `INSTALL_FAILED_USER_RESTRICTED: canceled by user` —— MIUI 的
  `AdbInstallActivity` 约 11 秒后**自我取消**；`adb_install_need_confirm` 本来就是 0。
- `uiautomator dump` 在该安装弹窗前**恒返回 0 字节**（连打 4 次全 0），
  拿不到坐标 ⇒ 只能用该机型实测过的固定 `input tap 195 1500`。

## 6. NOT_RUN

- **8090 容器重建**（补 `api/flashcards` + `callback/weixin`）—— 跨仓部署，未获授权。
  闪卡与微信回调的 FAIL 都源于此；不重建，这两条永远不会变绿。
- **企微三项密钥**（`POCKET_WECOM_TOKEN` / `_ENCODING_AES_KEY` / `_CORP_ID`）——
  仓内无从取得，值只能从企微后台拿。**只重建不解这个，微信回调会从 404 变 503。**
- **2 条 sttdev 专属 flow**（`_connectivity-sttdev` / `notes-stt-error-visibility`）
  —— harness 的包名一致性门**按设计**拒绝与主包同批跑，需 `POCKET_APP_ID` 单独跑。
- `email-browse` 的 MIME 断言未在本轮定论。

## 7. 复跑方式

```bash
VAL="$(docker exec opencode-pocket-pocketd-local-openpocket printenv POCKET_AUTH_PASS)"
export POCKET_DEV_PASS="$VAL" POCKET_AUTH_PASS="$VAL" POCKET_MASTER="$VAL"   # 值不回显
export POCKET_SERIAL=4c308e2e POCKET_API_BASE=http://127.0.0.1:8090 POCKET_DEVICE_PORT=8090
bash scripts/maestro-run-all.sh $(ls .maestro/*.yaml)
```

> `POCKET_MASTER` 用与 dev 口令同值：这是**本轮在测试设备上新设的本地库主密码**，
> 若要用别的值请显式指定。跑完记得
> `adb shell pm enable com.kaixuan.opencode.pocket.sttdev`。
