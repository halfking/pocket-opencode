# RSS 每日摘要 + 内置学习库（2026-10-03）

## 1. 需求原文与拆解

> 需要到 github 或其它的知名网站上寻找 IT、财经及实事相关的 RSS 订阅源，并将它们加入到初始的数据中，
> 可以每天收到一份全部信息的摘要和订阅的列表。同时，需要针对闪卡这类的语言或某专业方面的学习准备
> 初始的数据库及字典，供我们在闲时进行学习操作。如 AI 相关的知识点与智能体、大模型的相关的技术学习，
> 英语的单词、常用句子及发音等。我们还可以将这些消息中常见得不错的一键分享到微博中或微信的朋友圈中。

拆成 5 条可验收的子需求：

| # | 子需求 | 落地形态 |
|---|--------|----------|
| R1 | 找 IT / 财经 / 实事 的知名 RSS 源，加入初始数据 | 内置推荐源目录 + 一键批量导入（幂等） |
| R2 | 每天收到一份"全部信息"的摘要 | 每日摘要聚合 + 定时生成 + 通知中心推送 + 应用内日报页 |
| R3 | 订阅列表 | 已有 `/api/rss/sources`；本次补 category 分类与日报分组联动 |
| R4 | 闪卡/学习初始数据库与字典 | 四套内置牌组（AI / 智能体与大模型 / 英语单词+发音 / 英语常用句），共 320 张卡，编译进二进制 |
| R5 | 一键分享到微博 / 微信朋友圈 | 系统分享面板（文本 + 卡片图两种形态），不碰任何非官方接口 |

## 2. 开工前的实测结论（不是拍脑袋）

现状核查（2026-10-03，只读）：

- `rss` / `flashcards` / `learning` / `wecom` 四个后端包**早已存在**，前端
  `features/rss|flashcards|study` 也在，微博/朋友圈的分享链路（`rssApi.share` +
  `pocket-native.share` + 后端分享卡）也早就打通了。
- 但 **全仓没有任何"初始数据"**：没有 RSS 种子目录文件、没有内置闪卡牌组。
  唯一的种子是 `server_rss.go` 里 5 条硬编码、**全是科技/设计**（hnrss、36氪、
  少数派、阮一峰、Smashing），用户要的**财经与实事一条都没有**。
- RSS 侧**没有日报**：路由只有 sources / items / filters / share / share-card。
- 真实库里 `rss_sources`、`rss_items`、`flashcard_notes`、`flashcard_cards` **全是 0 行**。
- **一个会直接毁掉 R2 的真 bug**：`cmd/pocketd/main.go` 里
  `rss.NewScheduler(rssStore, fetcher, rss.Scope{UserID: "local", WorkspaceID: "default"})`
  把作用域写死成 `local/default`，而订阅源是通过 API 按**真实 JWT 用户 + 工作区**写入的。
  结果是：**订阅看起来成功了，后台一条都拉不回来**，日报自然永远是空的。

结论：这不是"从零加功能"，而是**补初始数据 + 补日报 + 修好让前两者生效的那条链**。

## 3. 方案

### 3.1 R1 内置推荐源目录

- 数据落在 `backend/internal/rss/catalog.go` 的 `StarterFeeds`，按 `it / finance / news`
  三类分组，每条带 `URL / Title / SiteURL / Language / FetchInterval / Note`。
- **入目录的门槛**：必须先真实 HTTP 探测通过（200 + XML content-type + 能解析出
  item/entry）。本次探测了 **103 个候选**，三轮，原始结果落
  `docs/handoff/evidence/rss-feed-probe/round{1,2,3}.json`（**已提交**：logs/ 与
  test-evidence/ 都在 .gitignore 里，放那里等于没人能复核），最终收录 **34 条**。
  被淘汰的典型：Yahoo Finance / Investing / Barron's（403）、
  BBC 中文 / 联合国新闻 / FT中文 / 知乎热榜（本机出口超时或不可达）、机器之心（200 但返回
  HTML、0 条）、华尔街见闻（200 但 content-type 是 text/html、0 条）。
  塞死链进去，用户看到的就是一排永远空白的订阅。
- `rss_sources` 新增 `category` 列（`ALTER TABLE ... ADD COLUMN IF NOT EXISTS`），
  导入时按目录分类写入 —— 日报分组直接靠它。
- 导入走 `INSERT ... ON CONFLICT (user_id,workspace_id,url) DO NOTHING`：
  幂等，且**不覆盖**用户自己改过的标题/间隔（`TestImportStarterSourcesIsIdempotent`
  专门验证了这一点）。

### 3.2 R2 每日全部信息摘要

- `rss_digest.go`：按 `[当天 00:00, 次日 00:00)` 半开区间聚合（用 BETWEEN 会把次日
  00:00:00 那一条算进前一天），按 category 分组、每节限条数，产出 `headline` + `body`
  （`body` 就是可直接发微博/朋友圈的纯文本）。
- 落库到新表 `rss_digests`，`(user, workspace, date)` 唯一 → 同一天重复生成是 upsert，
  重复打开看到同一份。**不同用户同一天互不覆盖**（有测试）。
- `rss/digest_service.go`：每天定时为**每一个有订阅的用户作用域**生成并投递；
  单个作用域失败不影响其它作用域，投递失败也不影响日报本身。
- 投递出口是 `notifycenter`（前台 WS 立刻到 + 进 inbox），实现见
  `cmd/pocketd/rss_digest_notifier.go`。通知体只放概览，全文留给日报页与分享。
- HTTP：`GET /api/rss/digest`（当天没有就按需生成并落库）、`POST /api/rss/digest/run`、
  `GET /api/rss/digests`。
- 配置：`POCKET_RSS_DIGEST_{ENABLED,HOUR,MINUTE,MAX_PER_SECTION,INCLUDE_SUMMARY,STARTUP_RUN}`，
  默认每天 08:30 生成。

### 3.3 修 scheduler 作用域

`rss.Scheduler` 改为按 `ListActiveScopes()` 扫库里真实存在的作用域，构造时传入的作用域
作为兜底保留并去重。规则也改成**按作用域读**（过滤规则同样是按 user+workspace 存的，
拿别人的规则过滤别人的源等于没过滤）。

### 3.4 R4 内置学习库

- 四套牌组数据在 `backend/internal/flashcards/starter_data/*.json`，`go:embed` 打进二进制
  → 离线可用，不依赖网络或外部文件。
  - `starter-ai-basics` 70 张、`starter-agent-llm` 70 张、
    `starter-english-words` 120 张、`starter-phrases` 60 张，合计 **320 张**。
  - 英语两套的每张卡背面都带 **IPA 音标 + 中文释义 + 例句**（例句加引号，供朗读抽取）。
- 导入 `ImportStarterDecks` 幂等：note/card 主键由 `(userID, 内置卡 id)` 稳定派生。
  **这里踩到一个真 bug 并修掉了**：一开始直接把内置卡 id 当 note 主键，而
  `flashcard_notes.id` 是**全局**主键（不按 user_id 分区），于是**第二个用户导入时
  每条都撞第一个用户的行、拿到 0 张卡且不报错**。现在派生 id 带用户短哈希，
  并有测试专门用"第二个用户"当负控。
- 端点：`GET /api/flashcards/starter`（目录）、`POST /api/flashcards/starter/import`（幂等导入）。
  刻意**没有**塞进 `services/flashcards.ts`：那个文件的导出签名被
  `flashcards.contract.test.ts` 逐个锁定，内置库不属于闪卡同步契约。

### 3.5 R5 一键分享

- 走**系统分享面板**（Capacitor `Share.share`），微信/微博/朋友圈都在原生选择器里，
  不碰任何非官方接口。
- 两种形态：分享文本（微博/朋友圈正文）与分享卡片图（1080×1350 PNG）。
- 卡片图在前端用 **canvas** 画，不用后端那张 `/api/rss/items/{id}/share-card`：
  后端那张是纯 stdlib 的 5×7 ASCII 点阵字体，**中文会整段变成 "?"**
  （`sharecard.go` 的 `truncateASCII`/`wrapASCII` 对 `r > 127` 一律替换），
  而日报标题几乎全是中文，用它发出去就是发一堆问号。
- `pocket-native.ts` 的 `share` 新增 `files` 通道：Android 分享图片必须走
  `files`，只传 `url` 会被静默丢掉（iOS 才是 url 生效）。

## 4. 验证

### 4.1 RSS 源目录
- 真实 HTTP 探测 103 个候选，原始结果已提交到
  `docs/handoff/evidence/rss-feed-probe/round{1,2,3}.json`。
- `TestStarterCatalogCoversThreeCategories` / `TestStarterCatalogEntriesAreWellFormed`
  （URL 合法、无重复、标题/站点/间隔非空、语言合法）。

### 4.2 导入与日报（真实 PostgreSQL，`POCKET_TEST_POSTGRES_DSN`，独立 schema + 用后 DROP）
- 重复导入 created=0 / skipped=N，源数量不涨，用户改过的标题不被覆盖。
- 分类过滤 + 每类上限生效。
- 日报跨天边界：前一天 23:00 与次日 00:00 的条目都不进当天。
- 同一天重复生成 upsert、跨用户互不覆盖、历史列表倒序。
- 单个作用域构建失败不影响其它作用域；通知失败不影响日报落库。

### 4.3 闪卡
- 数据本身：四个文件全部通过结构/ID 连续/无重复正面/无空背面/无 BOM 校验；
  英语两套 180 张卡全部以 `IPA: /` 开头。
- `TestImportStarterDecksIsIdempotent`：二次导入建 0 张卡，且**不覆盖已复习状态**
  （state/reps/due 原样保留）；第二个用户导入拿到完整 70 张。
- `TestStarterCardsAreDueForReview`：导入后 120 张都进入待复习。

### 4.4 前端
- `digest-share.test.ts` 10 项：分享文本上限与"另有 N 条"提示、空日报不留空行、
  卡片条目上限、dataURL 拆解、截断、例句抽取（只取引号内英文，不把中文释义念出来）。
- `vue-tsc --noEmit` 与仓库门禁（`npm run gates`）。

## 5. 明确没做的事（避免下次误以为已完成）

- 没有接微博开放平台 / 微信开放平台的**代发**接口。分享是"生成内容 + 拉起系统面板"，
  发布动作由用户在原生选择器里完成。这是刻意选择：代发需要用户授权第三方凭据，
  且微博/朋友圈没有面向个人自建应用的稳定免审发布接口。
- 没有把 RSS 条目自动推成闪卡（`learning` 模块里有把来源加入学习的通道，但没和
  本次日报联动）。
- 目录里没有放聚合站/镜像站（如 RSSHub 实例）：实测多数在本机出口不可达或不稳定，
  而收录死链会直接损害"订阅列表"的可用性。
