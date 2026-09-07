# 任务详情：当前/历史会话（2026-09-08）

消费端 SSOT。存储分层以 companion `docs/SESSION_STORAGE_DESIGN.md` 方案 D 为准，本文只定 **openpocket 任务详情如何读**。

## 1. 权威

| 层 | 系统 | 任务详情用途 |
|---|---|---|
| 索引 | ACC `task_id` ↔ `agent_session_id` ↔ `gw_session_id` | 列出该任务全部会话 |
| 当前正文 | 本机原生文件，经 agent-companion native API | 仍在磁盘/内存的全文 |
| 历史正文+成本 | llm-gateway-go Session V2 | 轮次、prompt/cache/completion、延迟 |
| 历史提炼 | Memora L2 | 摘要；不存全文 |
| 降级 | pocketd `task_session_links` | ACC 空时手工附加 |

当前 vs 历史：ACC 行能被 companion 打开（native 文件或内存 SessionState）→ 当前；否则 `gw_session_id` → 网关；再否则 Memora 摘要。

## 2. 闸门（2026-09-08 实测）

- pocketd `acc_report_session` 曾 `task_id: ""`，索引对不上任务。上报必须带真实 task_id。
- Node ACC 校验 `api_keys.key_value` 静态 Bearer（≤256）。JWT 在 Node/nginx 上常 401/400；客户端必须回退 raw key。
- 本机已把 pocketd 的 MCP key 写入 `acc_db.api_keys`（`pocketd-local`）。真值只在容器 env + PG，不进 git。
- NetBird mesh 仍被阿里云 SG 挡 TCP 10000/33080。域名继续 SSH 反向，不阻塞本功能。
- companion Runtime `:4101` 404。本轮注册以 Node ACC 为准。

## 3. HTTP 契约（pocketd BFF）

前端只打 pocketd，不直连 companion/ACC/网关。

- `GET /api/tasks/:id/session-bundle` → `{current[], historical[], usageTotals}`
- `GET /api/tasks/:id/sessions/:sid` → 元数据 + durationMs + tokens `{input, cacheRead, output}`
- `GET /api/tasks/:id/sessions/:sid/transcript?types=user,assistant,tool,thinking`
- `POST /api/tasks/:id/sessions/:sid/extract-title`
- `POST /api/tasks/:id/sessions/:sid/summarize`

消息：`{id, ts, type, role, name?, text, tokens?}`。`type` = `user|assistant|tool|thinking|other`。思考不得只揉进 text。

Token：历史用网关 `session_turns`；当前用原生/companion 能拿到的用量；缺则 `unknown`，不编造。

环境（占位符）：`POCKET_COMPANION_URL`、`POCKET_COMPANION_SECRET`、网关 admin 基址与 key。

## 4. companion

现 `GET /api/v1/sessions` 只读内存。新增只读 native（对照 pocketd `backend/internal/adapter/disk`：peek、不拷 >64MB sqlite、locator 白名单）：

- `GET /api/v1/native/sessions?kind=zcode|cursor|opencode|claude|codex`
- `GET /api/v1/native/sessions/:id?kind=`
- `GET /api/v1/native/sessions/:id/transcript?kind=&types=`

部署：`agent-companion/deploy-local.sh`，网络 `shared-infra`。

## 5. ACC（Node 在请求路径上）

- `acc_report_session` 增可选：`agent_kind`, `agent_session_id`, `gw_session_id`, `session_path`, `dispatch_id`
- 新增 `acc_list_sessions`：过滤 `task_id`（必填或强过滤），limit 默认 50 上限 200
- 写入现有 `sessions` 表可空列；acc-go `acc_session_reports` 契约对齐但不切流量

## 6. 前端

拆 `TaskDetailView.vue`（已超 300 行）：

- `TaskSessionPanel.vue`：当前/历史列表（标题、agent、时长、token）
- `SessionKindFilter.vue`：user / 模型回复 / 工具 / 思考
- `TaskSessionTranscript.vue`：复用 RoundTimeline；BFF 必须给出独立 `thinking`

操作：提取标题、刷新总结。不再只显示 `sessionId.slice(0,16)`。

## 7. 部署顺序

1. companion native API + `deploy-local.sh`
2. Node ACC MCP 工具（不重建 PG）
3. openpocket `start.sh --backend-only` 热更 pocketd；frontend dist 进容器
4. H5 任务详情；真机走 `https://pocket.itestu.cn`（无 adb reverse）

## 8. 验收

- ACC 上带 `task_id` 的会话出现在任务详情
- 本机 zcode/cursor 能开全文并四类过滤
- 仅网关/Memora 的会话走历史段，有总结与 token
- 原生文件不被改写

## 9. 明确不做

不新建会话微服务；不全量正文进 Memora；不以 pocket-opencode 为 UI SSOT；不把明文写进 `deploy-local.sh`；本轮不部署 acc-go Runtime。
