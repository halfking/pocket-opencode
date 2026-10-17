# 48 小时修正任务审计（2026-10-17）

范围：`origin/main` 自 `a1f385f1` 起的 57 个提交（2026-10-07 ~ 10-17）+ 本地工作树。
方法：拉取最新 main → 建立编译/测试基线 → 逐个未合并分支做有效性判定 → 逐条批判审计
→ 实机复现数据库层缺陷 → 实施并验证修复。

---

## 一、结论速览

| 项 | 结果 |
|----|------|
| main 拉取 | 快进 57 个提交，无冲突 |
| 基线编译/测试 | Go build/vet/test **全绿**；vue-tsc **全绿**；node 测试 **2 个真实失败**（此前被提交者的「退出码 0」掩盖） |
| 未合并分支 | 3 个 → 1 个已合入可删、1 个被 main 完全超越可删、1 个有效已合入 |
| 实修缺陷 | **6 类**（2 个门禁自身失效、2 个部署层观测缺失、1 个运行日志盲区、1 个自检工具从未生效） |
| 验证强度 | 每个修复均用**变异测试**或**实机复现**证明判据仍在咬 |

---

## 二、48 小时的提交：批判与审计

57 个提交按性质分四类。**总体评价是：本轮工作的主要缺陷不在「写错了」，
而在「门禁的前提随实现推进而失效，却没人回头修门禁」**。

### 2.1 门禁体系扩容（`2ea2f140`、`89e2e08a`、`875dfde9`）

新增 6 道门并接入 CI，方向正确。**但新门普遍带一个致命结构**：
把「判据在咬」建立在「仓里恰好存在某个反例」之上。

- `c6ac2a84`（maestro-flows 加分组下限）—— 承认「总条数下限挡不住整组删光」，补了分组下限
- `f6364754`（smart-quotes 换掉恒真自指用例）—— 承认自指用例是装饰
- `b12e65d8`、`cfc07e1e` —— 给覆盖面下限抬数

**审计意见**：这批提交自己已经识别出「恒真用例」这个病并开了药方，但药方只治了
「用例恒真」，没治「**前提恒假**」。后者更隐蔽：`capabilities-stub-gate` 就栽在这里
（见 §3.1）。下一道门应当继承的教训是：

> 负控必须能在**与仓内状态无关**的前提下证明判据在咬。合成夹具是唯一可靠做法。

### 2.2 文档盘点与数字校准（`99d9f5bc`、`64ca6de8`、`c4196327` 等）

反复核对「30 域/48 模块」这类计数，并诚实记录了「ls 实测」的来源。
这一类的**自我怀疑是本轮最值得肯定的部分**——多处主动推翻自己上一轮的读数
（`3d729a3b`「我把一条自己的债归成了别人的」、`d85b570b`、`eb4c1443`）。

**审计意见**：无缺陷。但 `1.79MB` 的设计文档（`cbb53ce1`）入库需要留意 git 仓库膨胀。

### 2.3 Maestro / harness 修复（`994d37d4` … `9d3c0925`，6 个提交）

**这 6 个提交全部已在 main 上。** 远端 `fix/maestro-login-assertions` 是同一批修复的
旧副本，见 §4.2 的 patch-id 比对。

**审计意见**：修复本身扎实（重试预算、三态查询、哨兵异常不再裸奔）。但**分支没删**，
导致同一修复在仓库里存在两个来源，是本轮「48 小时后仍有不活跃分支」的直接成因。

### 2.4 STT 网关统一入口（`d929b5e1`、`df4116ee`）

把网关通道收敛到 `/v1/audio/transcriptions` 统一入口，方向对。
**审计意见**：这个提交**留了一笔未结的账**——它给价表加了 `glm-asr` / `minimax-asr-1.0`
两档，却没同步价表门禁的 GOLDEN 名册，导致 `stt-price-catalog-load-bearing` 在 main 上
**从 2026-10-08 起一直红**。见 §3.2。

---

## 三、实修缺陷（6 类）

### 3.1 `capabilities`：Sherpa 已实现却仍登记为「未实现」

**根因**：`feat/local-asr-sherpa-phase4` 把 SherpaPlugin 换成 678 行真实实现
（sherpa-onnx 双引擎、模型下载、端点检测），但**没同步**
`frontend/src/lib/shell/capabilities.ts` 的 `NOT_IMPLEMENTED_PLUGINS`。

**后果**：不是崩溃，是**静默降级**。`anyPlugin()` 对登记在册的名字一律返回 false
⇒ `recognition` 能力恒为 false ⇒ **本地 ASR 明明可用却被判不可用，流量白白流向云端**。
Phase 4 落地的那次特性收益，等于被一张过期的表吃掉了。

**为什么门禁抓到了却没修**：`capabilities-stub-gate` 其实**抓到了**（指纹消失即转红），
但它转红的原因不是「判据失效」，而是「**前提消失**」——
量具自证硬要求 `marked >= 1`、负控硬编码「抽掉 Sherpa 的登记」。
门最后在逼人把一个真实现继续当骨架登记着。**这是「门反过来绑架实现」的典型形态。**

**改法**：
1. 登记表移除 Sherpa（5/6 个 `@PluginMethod` 已实现；唯一仍 reject 的
   `extractEmbedding` 是标注清楚的 Phase 5 占位，有 web 兜底且不参与能力判定）
2. 判据抽为**纯函数** `markedAsPlaceholder` / `missedRegistrations`，不再遍历真实目录
3. 量具自证与负控改用**合成夹具**（幽灵插件），与仓内是否恰好有骨架彻底无关
4. ① 不再要求登记表非空；完整性由 ①+② 合起来保证
5. `capabilities-detect` 的 §179 不变量保留，但拆成「事实」（Sherpa 应报 model）
   与「不变量」（临时登记合成档必须压回 none）

### 3.2 `stt-price-catalog`：门禁与自己的设计说明互相矛盾

**根因**：两条声明不可能同时为真——
- GOLDEN 注释：「其余 gateway 条目不在 §579 的论证链上，**故意不钉**」
- `checkRoster`：`unknown.length === 0`（一条不多不少）

**实测事实**（`backend/internal/stt/target.go`，14 条有价档）：external 9 条全在册，
gateway 5 条全不在册。2026-10-08 网关加档后这道门**一直红**，至今没人修——
因为它每次红的修复动作都是「把新网关档抄进 GOLDEN」，纯记账、不含信息量。

> 一道每次产品正常生长就变红、且修复手段是机械抄写的门，最终只会被关掉。

**改法**：按 Group 分组判「多」，另加一条真正承重的不变量——
**任何一档（含 gateway）不得比 GOLDEN 基线最低价更便宜**。
核对过 §579 结论未受影响：基线仍是 $0.012/h，新加的 $0.38/$0.50 都更贵。

**实现中踩到并修掉的真 bug**：`parseCatalog` 会吐出 `USDPerHour` 缺失的档，
而 JS 里 **`null < 0.012` 求值为 true** ⇒ 不过滤则「击穿基线」恒红。
这个 bug 是新写的负控⑤ 自己抓出来的。

### 3.3 慢查询观测此前完全不存在

**根因**：`docker-compose.db.yml` 的 postgres 服务**没有任何**慢查询配置。
`log_min_duration_statement` 默认 `-1`（关闭）⇒ 一条慢 SQL 都不落日志；
`pg_stat_statements` 未安装 ⇒ 累计统计无从查询。

**而 `docs/guides/DEPLOYMENT_GUIDE.md` 却让运维执行**
`SELECT * FROM pg_stat_statements ORDER BY mean_exec_time DESC LIMIT 10;`
—— 该语句在默认配置下必然报 `relation does not exist`。
**文档承诺的排障手段在默认部署里根本不存在。**

**改法**：compose 开 `shared_preload_libraries=pg_stat_statements`、
`log_min_duration_statement=${OPP_PG_SLOW_QUERY_MS:-500}`、`logging_collector`；
新增 `deploy/bin/pg-init-slow-query.sql` 装扩展并建 `pocket_slow_statements` 视图
（按**总**耗时排序——跑得多的才是真问题）；更新部署指南。

### 3.4 健康检查对「库根本没建」是瞎的

**根因**：`pg_isready` 只探服务端是否应答，**不校验凭据、不校验库是否存在**。实测：

```
$ pg_isready -h 127.0.0.1 -U nonexistent_role_xyz -d nonexistent_db_xyz
127.0.0.1:5432 - accepting connections        # exit=0
```

**真实故障链**（macOS + Docker Desktop 实机复现）：
1. 宿主数据目录属主 uid 501，镜像内 postgres 是 uid 70
2. initdb 的临时 `pg_ctl` 以 postgres 身份起不来 → `wrong ownership`
3. 镜像入口放弃重试，看到 `PG_VERSION` 就「目录已有数据库，**跳过初始化**」
4. ⇒ **创建 `POSTGRES_DB` 的步骤被整个跳过**，`pocket` 库根本不存在
5. 而 healthcheck 只跑 `pg_isready` ⇒ 容器照样报 **healthy**

**改法**：healthcheck 换成真的把连接建起来
（`psql -U $POSTGRES_USER -d $POSTGRES_DB -tAc 'SELECT 1'`），
失败时把排查线索直接打进 healthcheck 输出。

> 这与 §3.1 是**同一类病**：探测绿灯、功能全无。本轮一次修掉两处，
> 说明这不是孤立事故，而是**缺一条「探测必须能失败」的纪律**。

### 3.5 `doctor.sh` 从未真正工作过

**这是本轮最重要的发现，而且是顺手捡到的。**

给 `doctor.sh` 加一条前置检查时，发现它的输出在第一条之后就断了。追下去：

```
$ ./doctor.sh; echo $?
  ⚠️  .env 不存在（cp .env.example .env）      ← 全部 15+ 条里只有这 1 条
1                                          ← 恒定失败
```

**「我们有服务自检」这件事本身就是假的。** 而且因为它永远报失败，
使用者很快就学会了忽略它 —— 一个恒坏的自检工具比没有自检更糟：
它占着「我们有自检」的位置，让真正的配置问题（比如上面 §3.4 的建库失败）
无人发现。

**根因**：三个计数器写作 `((PASS++))`。`((expr))` 的退出码取决于表达式求值，
值为 0 时退出码 1；而 `((VAR++))` 取的是**自增前**的值 ⇒ 第一次调用时计数器为 0。
配合本文件统一的 `[[ 条件 ]] && check_pass || check_warn` 写法，
`check_warn` 作为**函数调用**位于 AND-OR 列表末尾 ⇒ 它返回的 1 被 `set -e`
当成脚本失败，脚本当场终止。

机制细节经过三级实测更正（前两版都写错了，已记在代码注释里）：

1. 单独写 `((C++))` **不会**触发 `set -e` —— 这是最初的误判
2. 触发点是「函数调用作为 AND-OR 列表的最后一个命令」
3. 且必须是**函数**：同样的 `((C++))` 放进 brace group `{ …; }` **不会**终止
   （组内命令被 AND-OR 豁免罩住）—— 这个差别不实测就会验错路径

**改法**：计数器改 `VAR=$((VAR+1))`（赋值语句退出码恒为 0，与 AND-OR 规则无关）。
修复后执行全部 12 条检查，无失败时返回 0。

> **元教训**：这是本轮唯一一个「被上一轮工作主动制造出来」的缺陷。
> 它和 §3.1、§3.4 是同一个家族 —— **失效的东西因为「一直在输出」而没人怀疑**。
> 一个恒坏的检查工具，比一个明显坏掉的检查工具危险得多。

### 3.6 运行日志对「失败得很快」的请求是瞎的

**根因**：`loggingMiddleware` 的全部逻辑只有一句
`if duration > 500ms { log("[SLOW] ...") }` —— 只记「慢」。

于是**一次瞬间 500**（连库失败、反序列化炸了、handler 直接报错）
**不产生任何日志行**，因为它快。而需要事后追查的恰恰是这类请求：
慢请求查得到，真正在报错的请求查不到。

次生缺口两个：

- **没有关联 ID**。`requestIDMiddleware` 在最外层，ctx 里本来就有 request_id，
  但没打印 ⇒ 日志与客户端看到的 `X-Request-ID` 对不上，只能靠时间戳猜
- **阈值硬编码 500ms**，运维改不了；而桌面本地跑 ASR 和生产网关不是一个量级

**改法**：5xx 一律记 `[ERROR]` 且**先于** SLOW 判定返回（又慢又 5xx 只记一行，
否则按请求统计错误数会翻倍）；日志带 `req=<id>`，取不到时退化成 `-`
（空串会让 `grep 'req='` 匹配到所有行）；阈值改为 `POCKET_SLOW_REQUEST_MS`，
非法值回落默认并打 `[WARN]`。

保持只记「慢」与「错」、不记常规 200 —— 全量打日志会把真正的错误淹掉。

---

## 四、分支判定

### 4.1 `fix/stt-gateway-unified-transcriptions` → 删除
tip `df4116ee` 已是 main 的祖先（`merge-base` == tip），完全合入。

### 4.2 `fix/maestro-login-assertions` → 删除
6 个提交全部在 main 上有对应物。patch-id 逐条比对：5 条精确等价；
第 6 条（`f20c4e74`）patch-id 不同，但**逐行 diff 显示两者完全一致**，
仅有两处差异且都是 main 更完整：

- hunk 头偏移（main 上方多 13 行，属后续提交叠加）
- `notVisible: "更多功能"` vs `notVisible: "更多功能|MORE FEATURES"`
  —— main 版本多了英文 locale（来自 `63b4c246` 多语言双轨断言）

⇒ 分支是**旧且不完整**的副本，删除无损。

### 4.3 `feat/ai-work-mess-rebrand` → 合入后删除
品牌重塑，158 文件 / +564 −347。判定为**有效**，理由：

- 身份标识全部保留（实测确认）：`appId` / `com.kaixuan` / npm 包名 / Go module
  **均未改动**；`POCKET_*` 环境变量只改了展示值，没有变量被改名
- 替换彻底：320 处旧品牌 → 350 处新品牌；9 个 locale 里 main 残留的 27 处
  旧品牌该分支**全部清掉**
- 32 个图标真实重渲染（含 Android/iOS/鸿蒙全套）
- 与 main 自动合并**无冲突**，且已验证我的改动未被覆盖
- 合并后 Go build/vet/gofmt/test、node 3571 用例、vue-tsc **全绿**

> ⚠️ 判定的保留意见：这是**产品级身份变更**（9 个 locale + 应用商店名）。
> 技术上无缺陷，但已发布用户会看到应用名变化。若产品侧尚未拍板，
> 应可 `git revert 577ba0d8..1d85b98a` 之外的合并提交来回退。

---

## 五、遗留风险

| 风险 | 影响 | 状态 |
|------|------|------|
| macOS bind mount 的 uid 错配未修 | 需运维决策 | **只保证「出问题一定报红」，未保证「一定不发生」**。改用 Docker 命名卷已实测可正常建库 |
| 已初始化实例不会重跑 init 脚本 | 存量环境仍无慢查询观测 | 需手工 `CREATE EXTENSION` + 重启，已写进部署指南 |
| 两个测试豁免项仍在豁免名单 | 覆盖缺口 | `flashcardIo.test.ts`（ESM 解析问题）、`recordingRuntimeMimeFallback.test.mjs`（STT 遗留空占位，自称应由 `recorderMime.test.mjs` 覆盖） |
| `auto_explain` 在 `postgres:17-alpine` 镜像中不存在 | 无法用 auto_explain 做单条慢查询剖析 | 需换 `postgres:17`（非 alpine）或手动编译 contrib |
| Go module 下载需绕过环境代理 | CI/本地可能复现 | 本机 `HTTP(S)_PROXY` 指向不可达的 `192.168.31.20:7897`，直连正常 |
| 品牌重塑属产品级变更 | 用户可见 | 见 §4.3 保留意见 |
| `POCKET_SLOW_REQUEST_MS` 故意不走 `internal/config` | check-env-example 门会列入 note 一栏 | 该门对 note 不判红；留在此处是因它是**日志阈值**而非业务配置 |
| 慢查询视图 `pocket_slow_statements` 只按总耗时排 | 均值高但只跑过一次的语句会被排后 | 已同时给出 `calls` 列，可自行按 `mean_exec_time` 复核 |
| `auto_explain` 不在 alpine 镜像内 | 无法单条慢查询剖析 | 需换非 alpine 镜像或自行编译 contrib |

---

## 六、下一轮提示词

见本轮交付消息末尾。核心方向：
1. 把「负控必须状态无关」写成门禁准入纪律，并回扫现有 7 道新门
2. 拍板 macOS 数据目录方案（命名卷 vs 授权 chown），修 §3.4 的根因
3. 处理两个测试豁免项
4. 清理 `.maestro` / `scripts` 下 `append-handoff-*.mjs` 这类一次性脚本（约 20 个）
