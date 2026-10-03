# 撤回：「AI 对话需要协议适配」是错误结论 —— 网关与应用链路本就可用

> 日期：2026-10-01 17:20，北京时间
> 涉及：`scripts/probe-chat-endpoint-by-model.mjs`、`probe-preferred-models.mjs`、
> `verify-app-llm-chat.mjs`
> 关联旧文档：`2026-10-01-llm-gateway-chat-completions-unsupported.md`（**该文档结论已被本篇推翻**）

## 一句话

网关 `llm.kxpms.cn` 与应用的 `/api/llm/chat` **都正常**。此前判定「chat/completions
端点不支持、必须实现 anthropic-messages / openai-responses 协议适配（几百行）」是错的，
那个决策项应当撤销。

## 错在哪

旧结论建立在一个观察上：`POST /v1/chat/completions` 20s 超时、0 字节；而
`/v1/messages`（3449ms）与 `/v1/responses`（3116ms）正常。

从「同一网关上一个端点超时、另两个正常」跳到「端点不支持、需做协议适配」，这一步
**没有先排除模型与请求形态的影响**。于是把一个未定位的现象当成了协议层面的结论，
并据此向你提了一个几百行的工作量决策。

## 实测证据

### 1. 端点层：19 次探测，0 超时

`scripts/probe-chat-endpoint-by-model.mjs`（8 个常用模型）：

| 模型 | 结果 | 耗时 |
|---|---|---|
| gpt-4o-mini | ✅ 200 | 3948ms |
| gpt-4o | ✅ 200 | 2506ms |
| gpt-4.1-mini | ✅ 200 | 4508ms |
| claude-sonnet-4-5 | ✅ 200 | 5807ms |
| claude-3-5-haiku-latest | ❌ 400 `not supported by this gateway` | 182ms |
| gemini-2.5-flash | ❌ 503 `No available provider` | 414ms |
| deepseek-chat | ❌ 503 `No available provider` | 808ms |
| qwen-max | ❌ 503 `No available provider` | 311ms |

负控（错密钥）：**401 / 29ms**，网关正确拒绝——说明它不是静默挂起。

失败全是**带原因的快速错误**，没有一个超时。

### 2. 应用实际使用的模型清单：10/11 可用

`scripts/probe-preferred-models.mjs`（直接取
`opencode_pocket.llm_gateway_configs.preferred_models`）：

```
✅ claude-fable-5     200    5290ms
✅ claude-opus-4-8    200    5789ms
✅ claude-sonnet-4-6  200   13158ms
✅ claude-sonnet-5    200    2624ms
❌ gpt-5.6            503    1227ms  No available provider
✅ gpt-5.5            200    3533ms
✅ gpt-5.4            200    4126ms
✅ glm-5.2            200    1338ms
✅ minimax-m3         200    1829ms
✅ deepseek-v4-pro    200    2157ms
✅ mimo-v2.5-pro      200    2215ms
```

唯一失败的 `gpt-5.6` 是 503 `no_candidate`，而应用**本来就有针对它的回退**
（`llmbff_provider_adapters.go`：`dynamicGatewayBFFProvider` 识别 503 `no_candidate`
后切到下一个 preferred model）。所以连这个唯一的失败点也是设计内被兜住的。

注意 `claude-sonnet-4-6` 要 **13.2 秒**——网关是慢的。若客户端超时设得比这短，
表现就是「超时、0 字节」，看起来像端点不支持。**这大概就是当初误判的来源**：
慢被当成了不支持。

### 3. 应用层：真实对话 200

`scripts/verify-app-llm-chat.mjs` 打 main 编出的 pocketd：

```
登录成功 (57ms)
应用自报 llm_gateway: enabled=true configured=true
模型列表 606 个
POST /api/llm/chat → HTTP 200  用时 3682ms
{"content":"ok","model":"claude-fable-5"}
```

应用自行解析配置、选中首个可用首选模型、返回真实补全。**端到端通过。**

## 顺带查清的两件事

**网关主机没问题**。我曾怀疑活跃配置指向的 `llmgo.kxpms.cn` 有问题，实测两个主机
都正常（`llm.kxpms.cn` 200/3026ms、`llmgo.kxpms.cn` 200/2707ms，同一密钥）。这个
假设也被证伪了。

**配置表确实有污染，但它不是本次故障的原因**。`llm_gateway_configs` 里 17 行、只有
id=3 是 `is_active=t`，而它：

- 属于工作区 `ws_user-admin_`（末尾多一个下划线），不是 `default`
- `api_key_encrypted` 长度为 **0**，即没有密钥
- 指向 `https://llmgo.kxpms.cn/v1`

同时 `llm_gateway_nodes` 里有测试残留且仍 enabled 的节点：
`PROBE-NODE-948776-RENAMED`（指向 `probe-948776.invalid.test`）以及三个
`E2E-GW-*`。**这些仍然没有清理**（不擅自删用户数据），但它们与 AI 对话可用性无关。

## 该怎么记这条教训

一个端点超时，先把**模型、请求形态、客户端超时**这三个变量排掉，再谈端点层面的结论。
「A 端点超时、B/C 端点正常」不足以推出「A 端点不支持」——尤其当 A 恰好也是最慢的那个。
提出「要写几百行适配」这种量级的结论前，必须先有按模型分组的实测矩阵。

## 现在的状态

- 网关部署：**已完成并验证**（应用层 200，真实补全）
- 协议适配：**不需要**，该决策项撤销
- 待清理（未动，需你授权）：活跃配置指向错误工作区且无密钥、`PROBE-NODE-948776-RENAMED`
  与三个 `E2E-GW-*` 测试残留节点
