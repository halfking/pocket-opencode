# 邮箱首页：AI 归类 / 批量伪删除 / 当前类搜索

## 背景

收件箱 `/email`（`EmailInboxView`）已有分类 chip：全部、重要、垃圾、工作、账单、私人、通知。
导航栏已有发票 / 清垃圾 / 设置。邮件已有 `category` / `importance`，kxmemory 能分类，但 IMAP 同步后**不会主动归类**；用户也无法在当前列表里多选伪删除或搜索。

## 锁定需求

### 1. 归类按钮（导航栏）

- 图标 `label`，`aria-label="归类"`。
- 只处理**未归类**邮件：`category` 为空。
- 由后端调 kxmemory，**逐封**识别并回写 `category` / `importance` / `ai_summary`。
- 类别白名单（与 chip 对齐）：`work` 工作、`bill` 账单、`personal` 私人、`notification` 通知、`marketing` 广告、`spam` 垃圾。
- 「重要」不是类别，是 `importance=high`（可与任意类别并存）。
- 广告沿用已有 `marketing`（chip 文案从「营销」改为「广告」）。
- 增加 chip「未分类」。
- 进度：`正在归类 3/12`；可取消后续批次。失败不中断整批。
- 每批上限 20 封，剩余自动续跑直到没有未归类或用户取消。

### 2. 删除按钮 + 多选（伪删除）

- 导航栏 `delete` 进入选择模式；列表左侧出现勾选框。
- 再点删除：确认后伪删除选中项。
- **伪删除**：行保留；正文缓存删除且禁止再从 IMAP 拉回；`snippet` 置空；保留 `subject` 与 `ai_summary`（若无摘要则先把 snippet 收成摘要再清空）。
- 不 MOVE/EXPUNGE IMAP。收件箱默认不展示已删。
- 详情页展示标题 + 摘要 +「正文已清除」。

### 3. 搜索（做）

- 导航栏 `search` 展开当前 **chip 列表内** 搜索，不跨类、不打 IMAP。
- 关键字匹配发件人 / 标题 / 摘要；可再限发件人、标题、时间范围。

### 4. 导航栏布局

默认：`search` `label` `delete` `more_vert`（发票 / 清垃圾 / 设置）。
选择模式：`close` + `删除 (n)`。

## 非目标

不改 IMAP 真删、不做已删回收站、不重训 kxmemory、不做跨账户全局搜索。
WebView 不直连 IMAP。自动/手动收信委托 pocketd（原生进程 / 服务端调度器），收信后异步归类。

## 结构变更影响（rule 57）

`emails` / `local_emails` 增列 `deleted_at`、`body_purged`。

| 路径 | 变化 |
|---|---|
| List* / cleanup | `deleted_at IS NULL` |
| GET body | `body_purged` 则空正文，不回源 |
| Insert/upsert | 已删行不复活、不回填 snippet |
| 发票 / 规则 / 摘要 | 不读已删 |
