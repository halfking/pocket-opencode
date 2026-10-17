# RSS 内置目录扩展：AI / 新产品 / 好价促销（2026-10-17）

**状态**: 现行（增量落地）
**前置**: [2026-10-03-rss-digest-and-starter-library.md](../handoff/2026-10-03-rss-digest-and-starter-library.md)（内置目录 it/finance/news 三类 + 每日摘要 + 一键分享；2026-10-03 当时 34 条，后增至 38）
**证据**: [rss-feed-probe/round4.json](../handoff/evidence/rss-feed-probe/round4.json)

## 1. 需求原文与完善

> 请到网上搜索热门的关于 IT、AI、财经、新产品及硬件产品销售活动等相关的 Rss 资源，将这些资源作为
> 数据库的初始化数据中，默认内置到应用中。请完成后进行部署测试与验证。相当于给所有用户提供的一个
> 基础的 rss 资源，可以由用户点击选择需要查看的 rss。由后台服务自动搜索下载最新的信息。

拆成 5 条可验收子需求与现状对照：

| # | 子需求 | 现状（2026-10-17 前） | 本次动作 |
|---|--------|----------------------|----------|
| C1 | 搜集 IT/AI/财经/新产品/硬件促销 热门 RSS | 目录 34 条，仅 it/finance/news；**AI、新产品、促销完全缺失**（OpenAI 混在 IT 里） | round4 探测 30 个候选（含基线复测），新增 14 条 + OpenAI 自 it 迁 ai，目录 38 → 52 条六类 |
| C2 | 内置到应用（初始化数据） | 目录编译进二进制（catalog.go），导入幂等 | 沿用；无需新表 |
| C3 | 用户点击选择要看的 RSS | 添加页只挂 5 条旧 seeds，未接分类目录 | RssAddSource 重写为分类目录页（chips + 分组 + 一键按类导入） |
| C4 | 后台自动拉取最新信息 | 已修（scheduler 按 ListActiveScops 扫真实用户作用域） | 沿用 |
| C5 | 部署测试与验证 | — | 本地起 pocketd + 隔离 schema + curl 全链路（§5） |

**「数据库初始化数据」的实现取向**（沿用 2026-10-03 决策，不在本次改变）：目录是编译期内置
（零迁移、离线可用），入库发生在**用户点选导入**那一刻（幂等 ON CONFLICT），而不是启动时给
所有用户无差别灌订阅——避免替用户做主、也避免多作用域重复拉取同一 feed。

## 2. Round4 探测（2026-10-17，本机真实 HTTP）

方法与前三轮一致：`GET` + `User-Agent: OpenPocket-RSS/1.0`，20s 超时，判定 200 +
XML/Atom content-type + 能数出 `<item>/<entry>`。30 个候选（含 1 条既有条目基线复测）中新增收录 14 条：

### 收录（14 新增 + OpenAI 迁类）

| 分类 | 源 | 条目 | 备注 |
|---|---|---|---|
| ai | 量子位 `qbitai.com/feed` | 10 | 中文 AI 资讯 |
| ai | Hugging Face Blog | 880 | |
| ai | Google AI Blog | 20 | |
| ai | MarkTechPost | 10 | |
| ai | MIT Tech Review AI | 10 | |
| ai | Ars Technica AI | 20 | |
| ai | OpenAI Blog（自 it 迁入） | 1299 | URL 保留旧路径保幂等，现 301 → /news/ |
| product | Product Hunt | 50 | |
| product | 爱范儿 | 20 | |
| product | Engadget | 20 | |
| product | Tom's Hardware | 50 | |
| product | ServeTheHome | 6 | 服务器/存储硬件 |
| deals | 什么值得买·好价 `http://feed.smzdm.com` | 30 | **仅 http 可用**（https 403）；时效短，15 分钟拉取 |
| deals | Slickdeals GPU 促销 | 25 | 搜索型 RSS（q=gpu） |
| deals | Slickdeals SSD 促销 | 25 | 同上（q=ssd） |

### 淘汰（关键教训）

- 机器之心 `/rss`：200 但返回 HTML、0 条（与 round1 结论一致，勿再试）
- VentureBeat AI：429（反爬，不稳定，不收）
- Launching Next / NEXT 产品榜 / 超能网：200 但 HTML、0 条
- Slickdeals feedburner 双源 / DealNews：404（feed 已死）
- mydealz / hotukdeals / smzdm-haitao / smzdm-https：403

## 3. 变更清单

**后端**（`backend/internal/rss/`）
- `catalog.go`：新增 `CategoryAI/CategoryProduct/CategoryDeals` 常量；`StarterCategories()`
  扩为六类有序 `[it, ai, finance, product, deals, news]`；`StarterCategoryLabel` 增补
  「AI 动态 / 新产品 / 好价·促销」；OpenAI Blog 自 it 迁 ai（URL 不变，老用户导入幂等）；
  新增 14 条 round4 验证源（另有 OpenAI 迁类）。**目录总数 38 → 52**（基线 it 20 + finance 11 + news 7；OpenAI 迁出后 it 19，新增 ai 7 中的 6 条 + product 5 + deals 3 = 14 行）。
- `catalog_test.go`：分类覆盖断言从三类扩到六类（任一类清空即红）；锁
  `StarterCategories()` 长度；OpenAI 迁类唯一性专测。

**前端**（`frontend/src/features/rss/RssAddSource.vue` 重写）
- 旧：5 条无分类 seeds 列表 + 手动 URL。
- 新：分类 chips（全部 + 六类）→ 按分类分组目录（标题/推荐语/语言）→ 每组
  「一键订阅本类」（`import-starter` 幂等，回显 created/skipped）→ 单条点击仍走
  discover→confirm 流程；手动 URL 入口保留。
- `api/rss.ts`：仅注释更新（六类）；`listStarter/importStarter` 早已存在。

**无需改动**（自动受益）
- 日报（digest）分组与标签走 `StarterCategoryLabel`，新分类自动出现「AI 动态」等节。
- scheduler 跨作用域拉取、分享卡、筛选器均与分类正交。

## 4. 配置

新增一项（默认 false，fail-closed）：既有相关项：`POCKET_RSS_ENABLED/FETCH_INTERVAL/MAX_CONCURRENCY`
（调度）、`POCKET_RSS_DIGEST_*`（日报）。

- **`POCKET_RSS_ALLOW_BENCHMARK_IPS`**（默认 `false`）：仅放开 RFC2544/5180
  基准段（v4 `198.18.0.0/15`、v6 `2001:2::/48`）的 SSRF 拦截。用途见 §5 的
  fake-ip 教训：开发机透明代理把所有域名解析进基准段，默认防护会拦下一切外联。
  loopback/内网/CGNAT/链路本地/元数据端点等其余防护在开关打开时**依然生效**
  （有专测锁定）。生产 DNS 正常，永远不需要打开。
- 顺带修正：v6 基准段此前漏拦（只拦了 v4），本次补齐对称。
- smzdm 15 分钟间隔写在目录条目里，导入后即按该间隔调度，无需 env。

## 5. 部署测试与验证（2026-10-17，本机真实执行）

**环境**：本地 Docker `llm-gateway-pg`（127.0.0.1:5432，隔离 schema
`rss_catalog_verify`，验毕 drop）；验证实例 = `GOOS=linux go build` 的 pocketd
跑在 `alpine:3.20` 容器（`POCKET_DEV_AUTH` 本地旁路登录，
`POCKET_SCHEDULER_ENABLED=false` 排除定时器干扰，逐端点手动触发）。

| # | 步骤 | 结果 |
|---|---|---|
| 1 | `go build ./cmd/pocketd`（darwin + linux/arm64） | ✅ |
| 1b | `go test ./internal/server/ -run TestRSSStarter` | 首轮 ✗（旧三类 default 断言把新分类判为回归）→ 已升级六类断言，复审 ✅ |
| 2 | `go test ./internal/rss/`（六类覆盖 / OpenAI 迁类唯一 / 幂等键 / SSRF 开关专测） | ✅ 全过 |
| 3 | PG 集成测试打真库（导入幂等 / 分类过滤+上限 / 非法 scope） | ✅ 3/3 |
| 4 | `POST /api/auth/login`（dev 旁路） | ✅ JWT 291B |
| 5 | `GET /api/rss/sources/starter` | ✅ categories=`[it,ai,finance,product,deals,news]`，52 源（19/7/11/5/3/7） |
| 6 | `POST import-starter {deals}` → created=3；**重复导入** → created=0/skipped=3；跨实例（host→容器）再导 → created=7/skipped=3（幂等键=URL 全程成立） | ✅ |
| 7 | `POST /sources/{smzdm}/refresh` | ✅ **真实出网拉回 30 条**，`GET /items` 返回真实好价文案 |
| 8 | `POST /sources/{MarkTechPost}/refresh` / `{Product Hunt}/refresh` | ✅ 10 条 / 50 条入库 |
| 9 | 清理：容器删除、`drop schema rss_catalog_verify`、PG 密码还原 | ✅ |

**过程中发现的真问题（fake-ip 教训）**：首次容器内 refresh 报
`resolved address is not allowed`——本机透明代理（Clash fake-ip）把**所有**
域名（含 `dig @223.5.5.5`）解析进 RFC2544 基准段 `198.18.0.0/15`，rss 包的
SSRF 防护按设计拒连。生产 DNS 正常不受影响；为让透明代理环境可开发/可验证，
加了默认关闭的 `POCKET_RSS_ALLOW_BENCHMARK_IPS` 逃生阀（见 §4），并顺手补齐
v6 基准段漏拦。量子位 feed 在该代理链路下偶发 EOF（python 直连探测 200/10 条正常），
属于本机代理抖动；入目录判定以 python 直连探测为准，Go 链路在生产 DNS 环境不受此影响。

## 6. 维护规则（沿袭 round1-3）

- 新增目录条目前**必须**真实 HTTP 探测过（200 + XML + 有条目），结果随手落
  `round{N}.json`——HTML 页、403、429、404 一律不收，宁缺毋滥。
- 改分类只动 `Category*` 字段；**不要改已收录条目的 URL**（导入幂等键是 URL）。
- smzdm 只能走 `http://`；若未来 https 放开，先探测再切换。
