# 真机自动化两件事：安装确认的真正机制 + 网关 key 的正确落地方式

日期：2026-10-02
对应目标：安装应用不要总弹确认框（默认在「拒绝」上，妨碍自动化）；真机配好网关 apikey。
设备：2411DRN47C / HyperOS V816 / `192.168.31.19:5555`

## 0. 结论先说

1. **安装确认**：这台机上 `adb install` **本来就不弹窗**，弹窗兜底逻辑已收进
   `scripts/device-install-preflight.mjs`（新文件）。但**没有任何一个 `settings`
   开关是这件事的开关**——负控已证，见 §1。
2. **网关 key**：已按 env 注入落地（根 `.env`，被 `.gitignore:8` 覆盖），
   并写进真机 workspace 实测可用。**没有写进任何源码**——仓库有零容忍卡口，见 §3。

## 1. 安装确认：`adb_install_need_confirm` 不是那个开关（负控实证）

我最初按直觉去调 `settings put global adb_install_need_confirm 0`，装包确实不弹。
**但这不能证明是它干的**，于是做了负控：

| 轮次 | 设置值 | 观测到的弹窗 | install |
|---|---|---|---|
| 基线 | `adb_install_need_confirm=0` | 无 | Success |
| **负控** | 改回 `adb_install_need_confirm=1` | **仍然无** | Success |

**负控转红失败 ⇒ 那个键在这台机上根本不承重。** 如果当时只看"设成 0 之后不弹了"
就收工，会把一个无效开关当成解决方案报出去——而它哪天被重置成 1（出厂/升级/换机），
自动化就会突然开始卡住，而没人知道为什么。

### 真正在放行的是什么

抓 `adb logcat` 抓到了决定性一行：

```
START u0 {cmp=com.miui.securitycenter/com.miui.permcenter.install.AdbInstallActivity ...}
  callers: ...PmInjector.installVerify:65 PackageManagerServiceImpl.verifyInstallFromShell:1227
  (BAL_ALLOW_ALLOWLISTED_UID) result code=0

WindowManager: ActivityRecord{... AdbInstallActivity t683} init visibleRequested:false
```

两点合起来才是完整解释：

- **`BAL_ALLOW_ALLOWLISTED_UID`** —— 放行原因是 **MIUI 的 UID 白名单**，
  不是那个 settings 键。
- **`visibleRequested:false`** —— Activity **被启动了，但 MIUI 判定后没让它显示**。
  所以抓窗口焦点只会看到 app 本身，看不到任何"弹窗出现过"的痕迹。

⇒ 「弹不弹」的开关在 `/data/user/0/com.miui.securitycenter` 下（私有 DB / prefs），
**无 root 读不到**（实测 `Permission denied`）。

### 因此脚本的判据改成「实测安装」而不是「开关状态」

`scripts/device-install-preflight.mjs`：
- 阶段 1 照写那 4 个开关，但**明说它们只是"尽量不弹"**，每条都回读确认，
  写不进去就如实打印，不假装成功；
- 阶段 2 **真跑一次 `adb install` 并全程盯窗口焦点**：
  没弹 → 静默通过；弹了 → `uiautomator dump` 定位确认键并点击 → 成功则 exit 0。

**这是刻意不赌单个开关的设计**：判据落在"这次安装到底成没成、弹没弹"上，
对任何一台 HyperOS 都成立，换机/重置也不失效。

弹窗分支的定位逻辑刻意**不盲发回车**：MIUI 那个框的默认焦点在「取消」上，
盲发回车等于点"拒绝"。必须先 dump 出层次结构、按文案/资源 id 定位到真正的
确认键再点；定位不到就如实打印"不盲点"并继续等，不乱按。

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

# 体检 + 真装（判据是这次安装的实测结果）
node scripts\device-install-preflight.mjs <apk路径>

# 网关 key 生效核对（key 从 .env 读，不回显）
#   → GET /api/llm-gateway/config 看 apiKeySet
#   → POST /api/llm-gateway/test 看 ok / status / models
```

## 6. 未做 / 待办

- **没有改 `scripts/adb-install-confirm.mjs`**：它作为独立兜底仍可用，
  新脚本里已内联同等（且更严）的逻辑。等确认新脚本稳定后再决定是否合并，
  避免同一件事两份实现漂移。
- **`check-maestro-flows.mjs` 仍未接进 gates**（沿自 round8 待办 5）。
- **换机/重置后的行为未验证**：`BAL_ALLOW_ALLOWLISTED_UID` 是 MIUI 白名单态，
  新机默认可能真的会弹。脚本对那种情况会走"弹窗 → 自动点确认"分支，
  但**该分支本轮没有被真机触发过**（这台机不弹），只做过判据级负控。
  ⇒ 该分支算"已实现且判据已验"，不算"已在真机跑通"。
