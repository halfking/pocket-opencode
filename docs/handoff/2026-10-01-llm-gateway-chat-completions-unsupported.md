> # ⛔ 本文结论已撤回，请勿据此决策
>
> **本文第 1 节「网关不提供 /v1/chat/completions」是错误的。**
> 实测：19 次按模型分组的探测 0 超时；应用真实使用的 11 个首选模型中 10 个可用；
> 应用自身 `/api/llm/chat` 返回 **HTTP 200 / `{"content":"ok","model":"claude-fable-5"}`**。
>
> 当时的错误在于：只观察到「chat/completions 超时而 messages/responses 正常」，就跳到
> 「端点不支持、需实现协议适配」，**没有先排除模型与客户端超时的影响**。实际上网关很慢
> （`claude-sonnet-4-6` 需 13.2 秒），慢被当成了不支持。
>
> 正确结论与完整证据矩阵见 **`2026-10-01-llm-gateway-chat-completions-RETRACTED.md`**。
> 「实现 anthropic-messages / openai-responses 适配」这个决策项已撤销，不需要做。
>
> 原文保留在下方，仅供追溯当时是怎么错的。

# AI 对话在本网关下不可用 —— 根因是「只实现了 openai-chat」，而该网关不提供该端点（已证伪）

> 2026-10-01 真机审计。用户原始要求：「网关 `https://llm.kxpms.cn/v1` 设为默认并完成
> 部署与测试验证」。**配置层已按要求设好且确实生效；但对话功能在这个网关上无法完成
> 推理**，根因如下，改造面也一并列出。

## 1. 结论先说

不是网络、不是代理、不是鉴权、也不是模型名。**是这个网关不提供 OpenAI
`/v1/chat/completions`**，而 pocketd 的 LLM 客户端只实现了这一个协议。

## 2. 五组对照（全部实测，密钥从 `logs/.gateway-key` 读，不进命令行、不打日志）

模型 `claude-sonnet-4-6`：

| # | 端点形态 | 结果 |
|---|---|---|
| 1 | `POST /v1/chat/completions` | ❌ 20s 超时，**0 bytes** |
| 2 | `POST /v1/messages`（Anthropic） | ✅ **3449ms**，返回 `"Hi! How can I help you today?"` |
| 3 | `POST /v1/responses`（OpenAI Responses） | ✅ **3116ms**，同样出字 |
| 4 | `POST /v1/chat/completions` + `stream=true` | ❌ 20s 超时（回了 28 bytes 就不动） |
| 5 | **故意写错 key（负控）** | ✅ 236ms 返回 `invalid_key` / `authentication_error` |

第 5 组是关键：**网关本身健康、鉴权正常**（错 key 236ms 就回错误），所以不是网关整体故障，
也不是密钥问题；**只有 `/chat/completions` 这一条路是死的**。

另外三个模型（`gpt-5.4`、`minimax-m3`）在 `/chat/completions` 上同样 60s 超时，
不是单个模型的问题。

## 3. 代理已被排除

| 路径 | `GET /v1/models` | `POST /v1/chat/completions` |
|---|---|---|
| 裸连（`--noproxy *`） | ✅ 243ms | ❌ 25s 超时，0 bytes |
| 走代理 `http://192.168.31.34:7897` | ✅ 256ms | ❌ 25s 超时，0 bytes |
| 代理本身可用性 | — | ✅ `proxy_http_code=200` |

两条路径表现完全一致 → 与代理无关。顺带记录：环境里的
`HTTP_PROXY/HTTPS_PROXY` 仍指向 `192.168.31.34:7897`（已知废弃端口，现役是
`127.0.0.1:7900`），但它**不是**本次故障原因，别顺着这条线追。

## 4. 代码侧的真正缺口

`backend/internal/llmgateway/client.go` 硬编码了两个端点：

- L164 `POST {BaseURL}/v1/chat/completions`（非流式 Chat）
- L230 `POST {BaseURL}/v1/chat/completions`（流式 Stream）

`backend/internal/server/llm_gateway_handler.go:20-22` 的注释已登记此事：

> gatewayFormats 是设置页「消息格式」下拉框的可选项……**pocketd 客户端当前仅实现
> openai-chat**；其余值先存储与展示，对话链路适配登记后续。

即：设置页给你三个选项（`openai-chat` / `anthropic-messages` / `openai-responses`），
但对话链路只认第一个。**所以单纯把配置的 `format` 改成 `anthropic-messages` 不会修好**
——值会存下来、界面会显示，请求仍然打向 `/chat/completions`。

顺带一提，这个挂死是**已知症状**：`client.go:49-52` 的注释记录了
「实测 2026-08-31：llm.kxpms.cn 在 preferred 模型里有两个 model 对
/v1/chat/completions 既不返结果也不返错误（连接挂死）」，当时只加了
`ResponseHeaderTimeout: 30s` 兜底，没有换端点。2026-10-01 实测**所有**模型都挂，
范围比当时记录的更大。

## 5. 改造面（供排期，未实施）

要支持本网关，需要在 `internal/llmgateway` 增加按 `format` 分派的适配：

- **请求翻译**：`messages` + `system` + `tools` + 图片附件
  → Anthropic 的 `system` 顶层字段 / `content` 块数组；
  → Responses 的 `input` 结构。工具定义的 JSON Schema 形状两边不同。
- **响应翻译**：`choices[0].message.content` ← Anthropic `content[].text`
  / Responses `output[].content[].text`；tool_call 的 id/arguments 形状也不同。
- **流式解析**：Anthropic 是具名事件（`message_start` / `content_block_delta` /
  `message_delta` / `message_stop`），Responses 是
  `response.output_text.delta` 一族；两者都不是 OpenAI 的 `choices[].delta`，
  `stream.go` 需要按 format 分派。
- **鉴权头**：Anthropic 用 `x-api-key` + `anthropic-version`，
  不是 `Authorization: Bearer`（本次实测已验证该形态可用）。

工作量是「协议适配」级别，不是改个 URL；且选实现哪几种形态属产品决策。

## 6. 一条被排除的伪缺陷（留档，避免下一个人重走）

审计中途一度以为「应用第二次调 `/api/llm/models` 丢了 token、拿到 401」。
**这是探针自己的错**：包装 `window.fetch` 的探针把自己那次**无凭据**的直连
fetch 也一并记了下来，随后被我误读成应用的第二次调用。

带 header 捕获的重测结论：观察窗内 8 个 API 请求**全部**带
`Authorization: Bearer …`（298 字符），token 全程稳定（4 次采样均 len 291）。
**应用鉴权正常，没有缺陷。**

教训（与本项目其他两条一致）：相对 URL 探针（如 `fetch('/api/llm/models')`）
在 WebView 里会解析到 `https://localhost` 并返回 index.html
（200 + `text/html`），看起来像「后端把 API 返回成了 HTML」的严重故障，
其实只是探针没带 API base。**用坏探针报缺陷，等于凭空制造缺陷。**

## 7. 复现脚本

- `scripts/llm-endpoint-shapes.mjs` —— 五组端点形态对照（含负控）**← 决定性证据在这一个**
- `scripts/llm-proxy-isolate.mjs` —— 代理 vs 网关分离
- `logs/llm-shapes.json` —— 本次五组对照的原始输出

另有 `scripts/llm-chain-check.mjs`（pocketd 侧 models/chat/stream 三段链路）与
`scripts/llm-direct-probe.mjs`（裸连三个模型）在本轮跑过后被并行会话的分支切换卷走
（2026-10-01 当天第二次发生，见 `docs/handoff/2026-10-01-shared-tree-hazard.md`），
需要时按本文第 2/3 节的参数重建即可；关键结论已由上面两个脚本独立复现。
