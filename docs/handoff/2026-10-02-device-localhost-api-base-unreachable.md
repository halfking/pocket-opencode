# 缺陷 11：装机包的构建默认值 `localhost:18099` 在真机上不可达

日期：2026-10-02
分支：`feat/mail-config-deploy`
提交：`f9fe343`
状态：**已修**（判定逻辑有 17 例 + 3 路负控覆盖；未重装 APK 做修复后复验，见文末）

---

## 一句话

装机包把 `VITE_API_BASE=http://localhost:18099` 烘进了产物，而设备上的
`localhost` 是**手机自己**。App 在模拟器上一切正常只是因为一直挂着
`adb reverse` 这根开发拐杖；真机上它不存在，整个 App（**含邮箱配置**）
连不上任何后端。这是「把邮箱配置部署到真机」的结构性阻塞。

---

## 对照实验（先证伪"其实能通"，再下结论）

不是推理，是在设备上实测。

```
$ adb -s emulator-5556 reverse --remove-all
$ adb -s emulator-5556 reverse --list
（空）

$ # 从设备发起
localhost:18099 -> nc: connect: Connection refused
10.0.2.2:18099   -> HTTP/1.0 200 OK ... ok
```

模拟器宿主别名 `10.0.2.2` 直通；`localhost` 只有 `adb reverse` 才通。
**真机上 `10.0.2.2` 也不成立**（那是模拟器专有别名），必须用局域网 IP。

> 这也意味着：本轮之前所有"App 在模拟器上跑通"的结论，**都隐含依赖
> `adb reverse host-22 tcp:18099 tcp:18099`**。这不是缺陷，是复现前提，
> 但记录在此以免日后误当成"真机也验证过了"。

---

## 顺带更正一条长期错误归因

旧 handoff 记的是「登录页 Custom backend URL 丢失、回落 Build default」。

**地址其实一直存得好好的。** 从设备 WebView 的 localStorage 里挖出来：

```
http://localhost  分区 ->  pocket_api_base = "http://10.0.2.2:18099"   ✅ 存在
https://localhost 分区 ->  （无 pocket_api_base）                        ❌
```

而装机包的 `assets/capacitor.config.json` 里 `androidScheme: "https"`
（`capacitor.config.ts` 的默认值，除非构建时显式设 `CAP_ANDROID_SCHEME=http`），
所以页面 origin 是 `https://localhost`。

**localStorage 按 origin 分区。换壳 scheme = 换一个全新的存储空间。**
所以不是落盘丢值，是压根没读到那份 override。旧归因「落盘没写进去」是错的。

> 顺带说明：这一条也意味着换 scheme 会连带丢掉 token、加密配置、语言、
> 主题等**全部** localStorage 状态。彻底解决需要把关键配置挪到原生存储
> （Preferences/SharedPreferences），属架构改动，本轮未做。

---

## 修法

| 位置 | 改动 |
|---|---|
| `config/api-base.ts` | 新增 `isLoopbackApiBase()`：识别 `localhost` / 整个 `127.0.0.0/8` / `[::1]` / `0.0.0.0` |
| `config/api-base.ts` | 新增 `resolveApiBaseWithSource()`：解析时带出来源（`override` / `build` / `origin`） |
| `config/api-base.ts` | **只**丢弃「构建默认值」里的 loopback 并换用 `PRODUCTION_API_BASE`；用户显式填的自定义地址仍尊重 |
| `config/api-base.ts` | `displayApiBase()` 改走 `resolveRuntimeApiBase()` |
| `servers/server-select-logic.ts` | `previewServerBase()` 的 build/origin 两档复用同一解析器 |
| `servers/ServerSelectView.vue` | 如实提示「构建默认值在本设备不可达，已改用 X」 |

**为什么只针对构建默认值**：用户在「后端服务器」页显式填 `localhost` 是
`adb reverse` 开发流的正当用法，不能一刀切禁掉。既有测试
`api-base.test.ts:153` 也明确断言显式覆写要被尊重。

**为什么 `previewServerBase` 也要改**：不改的话「保存前预览显示 localhost、
保存后实际是生产入口」——预览就在骗人。

**为什么提示要上屏**：不加提示的话，登录页底部地址会突然变掉，用户会以为
自己的设置又丢了（正是本缺陷最初被误记的原因）。

i18n：新增 `settings.buildDefaultUnreachable`，9 个语言包齐平，各语言含
`{url}` / `{fallback}` 占位。

---

## 验证

新增 `frontend/src/config/api-base-loopback-device.test.ts`（17 例）。

**3 路负控，全部实测转红后恢复：**

| 负控 | 注入 | 结果 |
|---|---|---|
| NEGCTL-1 | 摘掉 loopback 丢弃逻辑（退回修复前） | **6 处转红** |
| NEGCTL-2 | 过度修复：连显式覆写也一并丢弃 | **恰好 1 处转红**（"显式 localhost 仍被尊重"） |
| NEGCTL-3 | 摘掉 ServerSelectView 的提示渲染 | **恰好 1 处转红**（接线护栏） |

> NEGCTL-2 第一次跑**不干净**：上一轮的 NEGCTL-1 破坏逻辑还留在文件里，
> 成了两者叠加（7 处红而不是 1 处）。从备份还原后重跑才拿到干净的隔离结果。
> 「注入是否生效」这个检查项不够，还得确认**只**注入了预期的那一处。

**回归：**
- 新旧三套（新增 + `api-base.test.ts` + `server-select-logic.test.mjs`）46 例全绿
- email / api / http / sse / websocket / connectivity 相关 41 个测试文件 339 例全绿
- `vue-tsc --noEmit` exit 0
- `check:i18n` exit 0（245 key 齐平）
- `check:i18n-translated` exit 0（未翻译欠账未增长）

---

## 没做的事（如实说明）

1. **没有重装 APK 做修复后的真机/模拟器复验。** 重装会让模拟器上的 App 立刻
   指向生产入口，打断正在进行的本地后端邮件联调。
   论证：判定逻辑是纯函数，已被 17 例覆盖；设备侧"localhost 不可达"这一事实
   已由上面的 `nc` 对照实验直接测得。两者合起来构成端到端结论，但**不是**
   "在设备上看过修复后行为"。
2. **没做架构改动**：`androidScheme` 仍可能随构建方式在 `http` / `https` 间漂移，
   一漂移就换一个 localStorage 分区、丢掉全部本地状态。要根治需把关键配置迁到
   原生存储。
3. **没动构建流程**：仍然是在构建时用环境变量注入 `VITE_API_BASE`。给真机分发时
   应注入真实可达的地址（局域网 IP 或生产域名），而不是 `localhost`。

---

## 复发检查清单

给真机构建分发包前，逐条确认：

- [ ] `VITE_API_BASE` 注入的是**设备可达**的地址，不是 `localhost` / `127.0.0.1`
- [ ] `CAP_ANDROID_SCHEME` 与上一次装机是否一致（不一致 = 换 localStorage 分区）
- [ ] 不要依赖 `adb reverse` 判定"设备能连上后端"
- [ ] 装机后先看登录页底部的「后端服务器 · xxx」，那才是真正在用的地址
- [ ] 设备上并存多个包变体时，先核对包身份（见
      `2026-10-02-email-summary-mime-and-master-key.md` 的双包陷阱）
