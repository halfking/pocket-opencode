# 04 · 网关配置推送契约加固（2026-09-07 晚）

对应 HANDOFF §4.1.1 复核结论的落地：上游 config 契约改为 stock OpenCode 官方端点，
推送成功判定加固（Content-Type/响应体校验），消灭 200 text/html「假成功」。

## 1. 背景与设计决策

2026-09-07 下午复核发现：pocketd 推送与实例配置代理均对接 `PUT /api/config/models`
+ `POST /api/config/reload`，而 stock opencode 没有这两个端点——其 SPA 兜底对任意
未知路径返回 `200 text/html`，旧实现只校验状态码会假成功。

**决策：不做虚构契约的实现（不为 opencode-plugin 加 HTTP 配置服务），而是把 pocketd
全部切到 stock OpenCode 固定契约（docs/opencode-contract.md §3.1）已有的官方端点：**

| 旧（虚构契约） | 新（stock 官方契约） |
|---|---|
| `PUT /api/config/models`（推全量） | `PATCH /global/config`（merge 语义，只提交 provider 子文档） |
| `POST /api/config/reload` | 不需要——PATCH 即时生效并持久化；ReloadConfig 以回读校验代替 |
| `GET /api/config/models` | `GET /global/config`，映射 ConfigV1 → ModelConfig |
| `POST /api/config/models/test` | 回读 /global/config 校验 provider/model 存在性 |

opencode-plugin **无需**实现配置契约：stock opencode 原生支持 /global/config，
插件保持 WS 注册/心跳/远程命令职责不变。

merge 语义取舍：合并不会删除旧 key，上游缩减模型列表时残留已下线模型条目
（可接受——整篇覆盖会连带清掉用户手工配置的其他 provider）。

## 2. 契约实测（本机 stock opencode 1.14.33 :4096）

```
$ curl -i http://127.0.0.1:4096/global/config
HTTP/1.1 200 OK
Content-Type: application/json
{"$schema":...,"provider":{"mockgw":{...}},"model":"mockgw/gpt-4o"}

$ curl -i -X PUT -H 'Content-Type: application/json' -d '{"config":{}}' \
    http://127.0.0.1:4096/api/config/models
HTTP/1.1 200 OK
Content-Type: text/html; charset=utf-8     ← SPA 兜底，旧实现会假成功
<!doctype html>...

$ curl -X PATCH -H 'Content-Type: application/json' \
    -d '{"provider":{"pocket-push-verify":{...}}}' http://127.0.0.1:4096/global/config
HTTP/1.1 200 OK  Content-Type: application/json
→ GET 回读 provider keys = ["mockgw","pocket-push-verify"]（merge 语义、立即生效）
→ PATCH 是深度合并：再 PATCH 原 doc 不会删除新增 key；provider.<key>: null 不支持（400）
→ 磁盘 ~/.config/opencode/opencode.json 同步持久化（实测确认）
```

测试残留清理：临时 provider 已从磁盘文件移除（文件已还原与测试前一致）；运行中
实例的内存配置里残留 `pocket-push-verify`（指向 127.0.0.1:9，惰性、不会被任何
会话选用），opencode 下次重启自然消失。

## 3. 代码变更

| 文件 | 变更 |
|---|---|
| `backend/internal/server/llm_gateway_handler.go` | `pushConfigToOpenCode` 改走 `PATCH /global/config`；新增 `patchGlobalConfigWithAuth`（2xx + Content-Type=application/json + 响应体可解析 JSON 三重校验）；删除 `putJSONWithAuth`/`postWithAuth`；出站 client 由硬禁私网的 `safeOutboundHTTPClient` 换为 `gatewayHTTPClient` |
| `backend/internal/adapter/opencode_config.go` | 四方法（Get/Update/Reload/TestModel）全部切到 /global/config 官方契约 + 同套 JSON 响应校验；请求按 `POCKET_OPENCODE_CONFIG_TOKEN` 附带 Bearer；对外 ModelConfig/代理 API 形状不变（前端 `client.ts` 类型零改动） |
| `backend/internal/server/llm_gateway_push_test.go` | 新增：happy path（PATCH 语义 + Bearer + provider 合并断言）、SPA 兜底 200 HTML 必须失败、401 状态码透传 |
| `backend/internal/adapter/opencode_config_test.go` | 新增：GET 映射、PATCH 提交体、reload 回读、TestModel 存在性、SPA 兜底四方法全拒 |

**出站 client 说明**：推送目标来自实例注册表，本地/内网部署形态下常驻 loopback/私网，
`safeOutboundHTTPClient` 硬禁私网会使推送在任何本地形态下都无法工作。改用
`gatewayHTTPClient`（与修复 #3 的 `validateGatewayURL` 同一开关语义）：
`POCKET_LLM_GATEWAY_ALLOW_PRIVATE=true` 显式放行；云元数据端点始终拦截；
DNS 重绑定防护保留。

## 4. 验证

- `go build ./...` / `go vet ./...` 通过；`go test ./...` 46 包全部通过（含 8 条新用例）。
- 契约行为实测见 §2（对真实 stock opencode 1.14.33 执行）。
- 代理 API 面兼容性：`frontend/src/api/client.ts` 的 ModelConfig/Provider 类型与
  adapter JSON 标签逐一比对一致，前端零改动。

## 5. 生产启用清单（HANDOFF §6.1）

1. pocketd 侧设置 `POCKET_OPENCODE_CONFIG_TOKEN=<实例鉴权 token>`。
2. 实例在内网/本机时同时设 `POCKET_OPENCODE_CONFIG_TOKEN` 于实例侧 + pocketd 侧
   `POCKET_LLM_GATEWAY_ALLOW_PRIVATE=true`。
3. 实例侧若开启鉴权（/auth 签发 Bearer），需保证 token 与 pocketd 侧一致。
4. 观察日志 `[llm-gateway] push config to opencode instances failed (non-fatal)`：
   加固后不再出现"假成功"，任何失败都会带状态码/Content-Type 原因落日志。
