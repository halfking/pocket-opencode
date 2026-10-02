# 需求 6「设备本地执行」分阶段方案（路线 A）

> 状态：**待用户拍板**。本文只做方案与验收口径设计，**不含任何实现**。
> 日期：2026-10-02　分支：`email-pipeline-snapshot-2026-10-01`（HEAD `5928260`）
> 证据来源：`docs/handoff/2026-09-30-email-pipeline-verify.md` §7cb / §7cc / §7cd / §7ce

---

## 0. 一句话

把「邮件处理」从**全在服务端**改成**全在设备端**：
IMAP/POP3 用 Java 重写（≈1700 行），纯逻辑复用现有 Go 代码编成 wasm（≈9800 行），
存储落在设备本地 SQLite，按需求 8 做 LWW 同步。

---

## 1. 为什么是这个技术栈（全部有实测依据，不是推测）

| 结论 | 依据 | 关键证据 |
|---|---|---|
| **Go 不能编到 WASM 去跑 IMAP** | §7cb | `syscall/net_js.go` 里 `Socket`/`Connect`/`Sendto` 全部 `return ENOSYS`；`net/fd_js.go` 注释写明是 *Fake networking … intended to allow tests*。实测 `GOOS=js GOARCH=wasm go build` **exit 0 产出 8MB wasm** —— 编译期完全看不出来，运行时必挂 |
| **设备端唯一能跑真实 TCP 的是 Java** | §7cb | `javax.net.ssl.SSLSocket`；现有 `EmailFetchRunner.java` 类注释第一句就是「设备不直连 IMAP」 |
| **纯逻辑搬进 wasm 行为完全一致** | §7cc | 三套探针 native vs wasm **逐字节 IDENTICAL**（rules 2131 chars / email 1699 / email-fetcher 84） |
| **包体不是决策依据** | §7cd | 主动把 `net`/`crypto-tls`/`go-imap` 拉进产物，体积 8.56MB vs 纯逻辑 8.69MB —— **socket 代码在 wasm 下是空壳，本来就不占地方** |
| **必须 Java 化的面只有 3 个文件** | §7cb | 全包 grep `net.Dial\|tls.Client\|imapclient.Dial\|pop3.\|smtp.Dial` 只命中 `fetcher.go`(1070) / `pop3_fetcher.go`(661) / `mime.go` 的 `fetchRawByTextproto` 一个函数 |
| **边界成本未知** | §7ce | 探针**没跑通**，一个数字都没测出来。不宣称 |

### 一条重要的反转

我一度认为「搬纯逻辑比搬全包省 3.84MB」，§7cd 用反向对照推翻了它：
socket 代码在 wasm 下是 ENOSYS 空壳，**拉进来几乎不增加体积**。
所以包体不必作为取舍依据；真正的分界只有一条 —— **wasm 里的 socket 一调用就 ENOSYS**。

---

## 2. 现状：设备端已经是什么形状

```
EmailFetchPlugin.java   38 行   configure / schedule / runNow 三个 @PluginMethod
EmailFetchRunner.java   72 行   两个 HTTP POST：/api/emails/sync、/api/emails/classify
EmailFetchReceiver.java 59 行   AlarmManager 定时（cc6753d 已修开机重排）
email-fetch-host.ts            bindNative() → configure + schedule，前台 kick 逻辑
connectivity.ts:71             启动时 startEmailFetchHost()
```

**注意**：`frontend/android/app/src/main/java/.../plugins/` 下已有 13 个 Java 文件，
实测**共 1470 行**。邮件这块只占 **169 行**（`EmailFetchPlugin` 38 +
`EmailFetchReceiver` 59 + `EmailFetchRunner` 72）—— 说明**Java 插件这条路在本项目里
是被走通过的**，不是从零开一条新路。这是路线 A 最大的隐性优势。

---

## 3. 阶段划分

每个阶段都要求**独立可验收、可回退**，且**不破坏现有服务端链路**（当前实现继续可用，
直到新链路在真机验证通过）。

### 阶段 0：可归因基线（不改任何生产代码）

**做什么**：在真机上量一次当前链路的真实耗时与失败模式，作为后续对比基准。
- `EmailFetchRunner.run()` 一次 `runNow` 的端到端耗时（分 sync / classify 两段）
- 服务端 `/api/emails/sync` 的实际处理耗时
- 网络失败 / token 失效 / 服务不可达各耗时多久

**为什么必须先做**：没有基线，后面所有「快了/慢了」都是感觉。

**验收**：产出三组数字 + 失败模式清单，写进文档。**不写任何代码改动。**

---

### 阶段 1：Java IMAP 只读层（不碰存储、不碰分类）

**做什么**：Java 侧实现 IMAP 登录 + 列举 + 取原文，产出与 `fetcher.go` 同构的「邮件原文」数据。
- 参照 `EmailFetchRunner` 的形态新增 `EmailImapClient.java`
- **必须照搬的三处已修过的坑**（这些是 Go 侧踩出来的，Java 重写会重新踩）：
  1. `net.Dialer.Timeout` 只管三次握手 —— 建连后**必须自己夹 deadline**（`mime.go:187-190` 的原注释）
  2. POP3 回退要拿到**同一份时间预算**，不是另起一个（`bb21c6d` / `6c78fbb`）
  3. IMAP partial 与 literal 之间的空格（`§7be`）
- 只读三件套：不发 STORE / MOVE / COPY / EXPUNGE

**验收**（可测）：
- 对 5 个真实账户各取 1 封，比对 Java 取到的原文与现有库里 `emails.body_path` 缓存**逐字节一致**
- 单封耗时**与阶段 0 量出的 Go 侧基线可比**（阈值在阶段 0 之后才定 —— 现在写死
  任何数字都是臆断；§7be 那个 150s 是**修复前**的挂死时长，不是修复后的水平）
- 断网 / 密码错 / 服务器不回话三种情况**都在 30s 内返回**（不能挂死）

**回退**：删掉新文件即可，现有链路不动。

---

### 阶段 2：wasm 纯逻辑层

**做什么**：把可复用的纯逻辑编成一个 wasm，由设备端 JS 调用。
- 搬运范围：分类归一化、重要性归一化、垃圾规则（`rules` 293 行）、发票日期解析、
  内容哈希、发票命名、汇总
- **不含**：`ExportInvoiceGrid`（写文件，wasm 下不可用，需求 5 要另找实现）

**验收**（可测）：
- 用 `scripts/wasmprobe-run.mjs` 的同一套探针，在**真机 WebView** 里跑出与 native **逐字节一致**的结果
  （当前只在 Node 宿主验过，**WebView 未验** —— 这是本阶段的主要风险）
- 冷启动到 wasm 可用的耗时：**先量出真值再定阈值**。wasm 体积已知是 8.69MB
  （§7cd），但「下载 + 实例化 + 首次编译」在真机上要多久**没有数据**

**回退**：wasm 加载失败时回落到服务端分类。

---

### 阶段 3：设备本地存储 + LWW 同步（需求 8）

**做什么**：设备端 SQLite（`sql-wasm.wasm` 已有先例）承载邮件/发票/账户配置，
并与服务端按**最后修改时间**双向同步。

**验收**（可测）：
- 离线改配置 → 联网后服务端被更新
- 服务端改配置 → 设备端拉到新值
- **双向同时改** → 以最后修改时间为准，且能构造出「设备新 / 服务端旧」与反向两种用例
- 需求 8 的归属语义（归 admin 还是归登录用户）**尚未拍板**，本阶段的 schema 设计依赖它

**风险**：这是 A 路线里**唯一需要新写领域逻辑**的部分。存储层实测
`store.go` 2077 行 + `invoice_store.go` 335 行（§7cd 更正过的数字；
早期文档里那个「LWW 4140 行」是分组估算，**没有逐文件量过，不引用**）。
它无法靠 wasm 复用 —— wasm 不碰设备存储。

---

### 阶段 4：端到端切换

**做什么**：把 `EmailFetchRunner.run()` 从「两个 HTTP POST」换成「本地流水线」，
前端 `email-fetch-host.ts` 相应调整。

**验收**（可测）：
- 真机关飞行模式 24h → 开飞行模式期间 0 次服务端请求，恢复后数据完整
- 需求 1（定时）、2（垃圾）、3（发票）、4（提醒）四条在真机各跑通一次
- **需求 2 的 IMAP MOVE 单独授权后验证**（不可逆）

---

## 4. 明确不做的事

- **不重写邮件协议本身**：IMAP 是 RFC 3501 已有的，Java 侧照着 `fetcher.go` 的行为对齐即可
- **不把 1.2 万行全搬到设备**：只有触 socket 的 ≈1700 行必须 Java 化
- **不在阶段 1 就动 IMAP MOVE**：垃圾清理是破坏性操作，独立授权
- **不删现有服务端实现**：新链路验证通过前它是回退路径

---

## 5. 已知风险与未知

| 项 | 状态 | 影响 |
|---|---|---|
| **wasm 在真机 WebView 的行为** | **未验**（只在 Node 宿主验过） | 阶段 2 的主要风险。`wasm_exec.js` 的宿主 API 在 WebView 里不同 |
| **wasm 边界成本** | **未测出**（§7ce 探针没跑通） | 已知架构下每封至少两次跨界；量级未知，但对比 LLM 的秒级往返很可能可忽略 —— **这是推断不是实测** |
| 需求 8 归属语义 | **未拍板** | 阻塞阶段 3 的 schema 设计 |
| 阶段 3 的存储实现 | 无法复用 wasm | A 路线里唯一需要新写领域逻辑的部分 |
| 需求 5 的 A4 网格导出 | wasm 下不可用 | 需要 Java 或 JS 的 PDF 实现，本方案未定 |

---

## 6. 我的建议

**先只做阶段 0**。它零代码改动、零风险，却能给后面四个阶段提供可归因的基准。
在真机基准出来之前就动阶段 1，会有和 §7bo 那次一样的风险：
「数据库/环境变了还是我改错了」分不清。

阶段 1 与阶段 2 之间没有依赖，可以并行；但两者都需要真机，
而真机验证目前**还没有任何一条链路被端到端验过**（需求 1-4 在这 120 封真实数据上
零执行证据，见 §7by）。
