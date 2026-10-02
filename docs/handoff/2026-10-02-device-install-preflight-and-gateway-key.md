# 真机自动化两件事：安装确认的真正机制 + 网关 key 的正确落地方式

日期：2026-10-02
对应目标：安装应用不要总弹确认框（默认在「拒绝」上，妨碍自动化）；真机配好网关 apikey。
设备：2411DRN47C / HyperOS V816 / `192.168.31.19:5555`

## 0. 结论先说

1. **安装确认的真实形态**：MIUI/HyperOS 会对**全新安装**弹确认框，不点就
   `INSTALL_FAILED_USER_RESTRICTED`。已交付并**接线**到 `scripts/device-install-preflight.mjs`，
   弹窗时自动定位「继续安装」并点击，实测装得上（`pm list` 确认）。
2. **没有任何 `settings` 键能关掉这个弹窗**——五个候选全部负控证伪，见 §1.2。
3. **网关 key**：已按 env 注入落地（根 `.env`，被 `.gitignore:8` 覆盖），
   并写进真机 workspace 实测可用。**没有写进任何源码**——仓库有零容忍卡口，见 §3。

> ⚠️ **本文件 §1 的初版结论已被推翻并重写。**
> 初版称"这台机不弹窗、`adb_install_need_confirm` 是那个开关"，那是**只在
> `adb install -r` 更新路径上测**得出的结论，把路径巧合当成了门被关上。
> 重测（§1.1）推翻了它。若你只看初版会被误导。

## 1. 安装确认：初版结论错了，错在**只测了一条路径**

### 1.1 更新路径 vs 全新安装，是两条不同的路径

初版我在 `adb install -r` 上测了四五轮，全都不弹窗，于是写下
"这台机本来就不弹"。**这是把路径巧合当成了门被关上。**

真机复测（同一台机、同一批设置）：

| 场景 | 弹窗 | 结果 |
|---|---|---|
| `adb install -r` 覆盖**已装**的 `com.kaixuan.opencode.pocket` | **不弹** | Success |
| 卸载后装**未装过**的 `maestro-server.apk` | **必弹** | 不点 → `INSTALL_FAILED_USER_RESTRICTED` |

**为什么重要**：Maestro 每次开会话都先 `uninstall` 再 `install` driver
（见 `2026-10-02-stt-maestro-flow-not-closed-on-device.md` 的实测堆栈），
所以它撞的一直是**会弹**那条路径。只在 `-r` 上测，永远看不到用户的原始问题。

⇒ 教训：**判据的覆盖面要覆盖缺陷真正发生的那条路径**，不是任一条能过的路径。

### 1.2 五个 `settings` 候选，在**真会弹的路径**上逐个证伪

初版我拿 `adb_install_need_confirm` 当解法，负控（改回 1）没转红，
但我据此推断"这个键不承重、别的机制在放行"——**这一步推断是错的**。
在**真会弹的路径**上重测，每个都回读确认写入成功：

| 候选键 | 值 | 弹窗 | 结果 |
|---|---|---|---|
| `global.adb_install_need_confirm` | 0 | **仍弹** | FAILED |
| `global.verifier_verify_adb_installs` | 0 | **仍弹** | FAILED |
| `global.package_verifier_enable` | 0 | **仍弹** | FAILED |
| `secure.install_non_market_apps` | 1 | **仍弹** | FAILED |
| `global.adb_install_enable`（= 开发者选项「USB 安装」） | 0 | **仍弹** | FAILED |

**结论：五个全部证伪，没有一个能关掉弹窗。**
初版那个"负控没转红"的观察本身没错，错在**把它解释成了"别的机制在放行"**——
真实解释是：**我测的那条路径压根不弹**，所以开关怎么改都看不出差别。

logcat 里的放行原因仍是 `(BAL_ALLOW_ALLOWLISTED_UID)` + Activity
`visibleRequested:false`；真正的门在 `/data/user/0/com.miui.securitycenter`
私有存储，**无 root 读不到**（实测 `Permission denied`，
`run-as` 也报 `package not an application`）。

⇒ 想要**彻底不弹**，只能人工在手机上开「开发者选项 → USB 安装」；
adb 侧做不到。因此脚本不宣称关掉弹窗，只做「弹了就点掉」这件被证实有效的事。

### 1.3 自动点确认：有效，且用对照实验钉死了因果

初版我说这个分支"从未在真机触发过、只做过判据级负控"。在真会弹的路径上
实跑，**它确实被触发了，而且有效**：

```
[preflight] 检测到安装确认弹窗，尝试自动确认
[preflight] 已点击确认键 "继续安装" @207,1498
[preflight] 已点击确认键 "继续安装" @207,1498
[preflight] install 结果: Success | Performing Streamed Install | Success
```

**正向**：点完 install Success，且 `pm list packages` 确认
`package:dev.mobile.maestro.test` **真的装上了**（不拿 install 的回显当证据）。

**反向对照**（钉死因果，排除"本来就能装"）：紧接着 `adb uninstall`，
重装同一 APK、不点任何东西 → **弹窗出现 + `INSTALL_FAILED_USER_RESTRICTED`**。

⇒ 成功是"点了"带来的，不是环境本来宽松。

⚠️ **推翻了另一份 handoff 的记录**：
`2026-10-02-stt-maestro-flow-not-closed-on-device.md:150` 记着
"弹窗自动点「继续安装」｜连点 2 次仍被系统撤销"。本轮实测与之**相反**——
连点两次后 install Success。两者冲突时以本轮的对照实验为准
（它有正向+反向+包状态三重证据，且是本轮现场复现）。

## 2. 脚本自身踩的两个坑（都已修，且都做了负控）

**坑 1：`exit` vs `close` 竞态。** 最初用 `child.on('exit')` 判定安装结束，
实测第一次跑出 `FAILED(undefined) | undefined` —— `out` 是空的。
`exit` 可能早于最后一批 `data` 到达，此时**一条输出都没读到**，
于是 `/Success/i.test('')` = false，被报成失败。
同一时刻手工 `adb install` 是 `Success exit=0`，**证明是我的脚本 bug，不是环境问题**。
改用 `close`（所有 stdio 关完）作为完成信号。

**坑 2：误删 `let code = null`。** 改 `exit`→`close` 时把声明一起删掉了，
运行时报 `ReferenceError: code is not defined`。
`node --check` 抓不到这类错（作用域合法，只是没声明）。

⚠️ 顺带记一条**我自己踩的假绿灯**：我为此写了个静态检查器
（比对"赋值目标 vs 声明"），跑出 PASS；再做负控（把 `let code` 删掉）——
**检查器依然 PASS**。原因：判据只认"行首缩进后紧跟标识符 ="的语句式赋值，
而 `code = c` 在箭头函数回调里、不在行首，**判据根本没看见那一处**。
两次改判据都没抓住后，放弃自制检查器，改用**真运行**作为唯一权威验证。
⇒ 这条印证了老教训：**判据必须落在缺陷真正发生的那一半上**，
"检查器是绿的"不等于"它看得见你要它看的东西"。

## 3. 网关 key：为什么不能写进源码

仓库有 `backend/internal/repohygiene/secrets_test.go`：**零容忍**，任何完整密钥
字面量进仓库即 `exit 1`，没有基线豁免、没有 `--update-baseline` 开关。
起因就写在那个文件的注释里：2026-10-01 有人把明文租户 key 写进
`opencode/config_writer.go`，**进了 git 历史、不可撤回**（`git log -S` 就能取回，
而那是把能计费的真实凭据）。所以这把 key 不能硬编码。

**落地方式（用户已确认走这条）**：
- key 写进**根 `.env`** 的 `POCKET_LLM_GATEWAY_API_KEY`，
  `.env` 已被 `.gitignore:8` 覆盖（`git check-ignore` 已验）。
- `backend/start-dev.sh:62-70` 本来就会从根 `.env` 读这两个键并 export，**无需改代码**。
- 通过 `POST /api/llm-gateway/config` 写进真机连的那个 workspace。

### 修正一条**已过期**的 handoff 结论

`2026-10-02-llm-gateway-unconfigured-and-destructive-selfheal.md` 写着
「`POCKET_LLM_GATEWAY_API_KEY` 在 `.env.example` 里不存在」。
**实测它存在**，在 `.env.example:64`；`backend/start-dev.sh` 也确实读它。
真正缺的是**本机的 `.env` 文件本身**（原先根本不存在），
不是模板缺键。照旧结论去"补模板"会做无用功。

## 4. 网关实测证据

key（`sk-6tGL…K51YV`，用户本轮提供）实测有效：

```
GET  /v1/models            -> 200, 606 个模型；9 个目标模型逐个 OK
POST /v1/chat/completions  -> 200, content=[Pong], finish=stop
```

后端侧（真机 app 实际连的实例，`adb reverse tcp:18099 → tcp:18111`）：

```
GET  /api/llm-gateway/config -> apiKeySet=True
                               baseURL=https://llm.kxpms.cn/v1
                               preferredModels=9
POST /api/llm-gateway/test   -> ok=true, status=200, models=606
```

⚠️ **一个容易把人坑成"假故障"的实测坑**：`glm-5.2` 是推理模型。
`max_tokens=32` 时返回 **HTTP 200 但 `content` 是空串**，token 全被
`reasoning_content` 吃掉（`finish_reason:"length"`）。
只看状态码会误判成"通了但模型不回话"。把预算提到 512 才拿到 `content=[Pong]`。
⇒ 验证网关必须看**正文非空**，不能只看 200。

## 5. 复现命令

```powershell
# 体检 + 调优（不装包）
node scripts\device-install-preflight.mjs

# 体检 + 真装（判据是这次安装的实测结果 + 弹窗是否被点掉）
node scripts\device-install-preflight.mjs <apk路径>

# 走 Maestro（内部已接到 device-install-preflight.mjs）
node scripts\maestro-run.mjs ...

# 网关 key 生效核对（key 从 .env 读，不回显）
#   → GET /api/llm-gateway/config 看 apiKeySet
#   → POST /api/llm-gateway/test 看 ok / status / models
```

## 6. 本轮改动清单

| 文件 | 改动 |
|---|---|
| `scripts/device-install-preflight.mjs` | **新增**。装前调优 + 盯弹窗 + 自动点「继续安装」 |
| `scripts/maestro-run.mjs:157` | **接线**：driver 安装从旧的 `adb-install-confirm.mjs` 换到 preflight，并把 stdout/stderr 打出来 |
| `scripts/adb-install-confirm.mjs` | **删除**。活代码已无引用，留两份实现必然漂移 |
| `.env`（gitignored） | 新增，网关 key 注入 |
| 本 handoff | 新增 |

## 7. 未做 / 待办

- **彻底不弹窗需要人工操作**：五个 `settings` 键全部证伪后，唯一已知的
  根治路径是手机上手动开「开发者选项 → USB 安装」。脚本不做这件事，
  也不假装做了。若要让自动化在**新机/重置后**也完全无人工介入，
  这一步必须进设备初始化清单。
- **`check-maestro-flows.mjs` 仍未接进 gates**（沿自 round8 待办 5）。
- **`maestro-run.mjs` 的完整会话未在本轮跑通**：只验证了它新接的那条
  driver 安装链路（从卸载态装到 `pm list` 确认就位）。整条 Maestro
  会话（启动 driver、跑 YAML flow、出报告）不在本轮范围内。
- **`.env` 里的 key 需轮换时**，改 `.env` 那一行后重启 pocketd，
  并重新 `POST /api/llm-gateway/config`（库里有密文副本，只改 env 不够）。
