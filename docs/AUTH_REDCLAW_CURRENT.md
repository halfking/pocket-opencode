# RedClaw 认证当前实现基线

> 更新时间：2026-09-05
>
> 本文档已按当前主线审计更新；历史描述不得作为部署配置直接复制。

## 1. 当前状态

RedClaw Auth Agent 当前提供两条登录链路：

**通用 OIDC 授权码流程**（原有）：

1. `GET /api/v1/sso/login` 生成 `state` 和 `nonce`，重定向到配置的授权端点。
2. `GET /api/v1/sso/callback` 使用授权码调用 `TokenExchanger`。
3. Auth Agent 验证返回的 `id_token` 的签名、issuer、audience、过期时间、subject、nonce 和可信租户声明。
4. 使用 `SessionIssuer` 持久化会话，再签发短时效 RedClaw 平台 JWT。
5. 受保护请求由 JWT middleware 验证，并可绑定 PostgreSQL 会话以支持撤销。

**中国身份平台适配器**（本次新增，已接入运行时）：

1. `GET /api/v1/sso/{provider}/login` 按 provider 生成单次使用的 CSRF state，302 跳转到对应平台的授权页。
2. `GET /api/v1/sso/{provider}/callback` 验证 state 后调用适配器换取外部身份，经统一 `SessionIssuer` 持久化会话并签发平台 JWT，返回 JSON `{jwt, session_id, claims, next}`。
3. provider 取值：`wechat`（微信开放平台）、`wecom`（企业微信）、`dingtalk`（钉钉）、`feishu`（飞书）、`alipay`（支付宝）。
4. 仅当对应提供商的环境变量凭据齐全时才挂载该 provider；配置不完整时记警告并跳过，不影响启动。
5. 租户绑定只来自 `<PROVIDER>_TENANT_ID` 环境变量（缺省回退平台默认租户），绝不使用外部平台返回的 `unionid/openid/userid` 充当租户。
6. 平台 JWT 与 OIDC 流程同构：`redclaw` + `redclaw-api` 双 audience、15 分钟有效期、`session_id` 绑定、`auth_method` 为 `sso:<provider>`。

**当前仍存在的集成边界**：

- Admin Console 仍请求 `/api/v1/auth/sso/login`，而 Auth Agent 当前路由为 `/api/v1/sso/login`；两端契约需要统一。
- 回调返回 JSON `{jwt, claims, next}`，不是浏览器 hash 重定向；前端回调页面需要相应调整。
- PKCE 已实现：钉钉支持 PKCE（授权 URL 携带 `code_challenge`/`code_challenge_method=S256`，换码提交 `codeVerifier`）；微信、企业微信、飞书、支付宝的平台协议不支持 PKCE，Adapter 以单次 CSRF state 防护作为补偿。
- 支付宝已支持响应验签：配置 `ALIPAY_PUBLIC_KEY` 后对两次网关响应的业务节点做 RSA2 验签，缺失/篡改即拒绝（fail closed）。
- OIDC Discovery 与 Auth Agent 内建 JWKS 获取/轮换未实现。
- SAML 只有配置字段，没有可用的 SAML ACS/解析/验签实现。
- JWT middleware 的 RS256 分支已修复：`JWTConfig.RSAPublicKey` 提供验签公钥；RS256 不再回退到 HS256 共享密钥，未配置公钥时 fail closed。
- 生产环境要求持久化 session；数据库不可用时不挂载 provider 路由（请求 404），不会降级为无绑定 JWT。
- provider 配置当前来自环境变量（每进程每个平台一个实例），按租户动态多实例配置需要 provider 配置表。
- CSRF state 存储为进程内存实现（单次消费 + TTL 清理）：多副本部署时 login 与 callback 可能落到不同实例，需要会话粘滞或外置存储；state 表随请求速率 × TTL 增长，网关层应对 login 做限流。

## 2. Provider adapter 层

目录：`services/platform-go/internal/authagent/providers/`

- `Provider` 接口：`Name`、`AuthorizationURL`、`ExchangeCode`、`ValidateConfig`。
- `ExternalIdentity`：统一外部主体（`ProviderSubject`、`UnionID`、`OpenID`、`UserID`、`Email`、`Name`、`TenantKey`）。
- `Registry`：校验并索引启用的 provider，拒绝重复、缺租户绑定和非法配置。
- `StateGuard`：provider 维度的单次使用 CSRF state 存储（默认 5 分钟 TTL）。
- `Handler`：统一 login/callback 路由、会话签发和平台 JWT mint。
- `HTTPClient`：context 超时、响应体大小限制、非 2xx 检查；不记录任何 secret 或 token。

平台账号标识规则：外部主键为 `provider:provider_subject`（例如 `dingtalk:union_xxx`），写入会话的 subject；`unionid/openid` 等原始标识保存在会话 metadata 中。

## 3. 已实现的协议适配器

| 提供商 | 文件 | 当前覆盖 |
|---|---|---|
| 微信开放平台 | `providers/wechat.go` | 网站扫码授权 URL、code 换 token、userinfo、openid/unionid 标准化 |
| 支付宝 | `providers/alipay.go` | OAuth 授权 URL、RSA2 请求签名、PKCS#1/PKCS#8 私钥解析、token/user info 网关调用 |
| 钉钉 | `providers/dingtalk_wecom.go` | OAuth 授权 URL、user access token、当前用户接口、unionId/openId 标准化 |
| 企业微信 | `providers/dingtalk_wecom.go` | 企业 OAuth 授权 URL、corp token、用户身份接口、userid/openid 标准化 |
| 飞书/Lark | `providers/feishu.go` | 三步交换（app access token → user access token → userinfo）、union_id/open_id/user_id 标准化 |

## 4. 环境变量

| 提供商 | 变量 |
|---|---|
| 微信 | `WECHAT_APP_ID`、`WECHAT_SECRET`、`WECHAT_REDIRECT_URL`、`WECHAT_TENANT_ID` |
| 企业微信 | `WECOM_CORP_ID`、`WECOM_AGENT_ID`、`WECOM_SECRET`、`WECOM_REDIRECT_URL`、`WECOM_TENANT_ID` |
| 钉钉 | `DINGTALK_CLIENT_ID`、`DINGTALK_SECRET`、`DINGTALK_REDIRECT_URL`、`DINGTALK_TENANT_ID` |
| 飞书 | `FEISHU_APP_ID`、`FEISHU_SECRET`、`FEISHU_REDIRECT_URL`、`FEISHU_TENANT_ID` |
| 支付宝 | `ALIPAY_APP_ID`、`ALIPAY_PRIVATE_KEY`（PEM）、`ALIPAY_PUBLIC_KEY`（可选，启用响应验签）、`ALIPAY_REDIRECT_URL`、`ALIPAY_TENANT_ID` |

示例见 `deploy/local-stack/config/deploy.env.example`。密钥必须经 Secret/KMS 注入，禁止写入 ConfigMap 或提交到版本库。

## 5. 测试状态

在 Go 模块目录 `services/platform-go` 内：

```bash
go test ./internal/authagent/... ./internal/platform/middleware/... ./internal/platform/sessionissuer/...
```

全部通过。provider 测试覆盖：

- 各平台授权 URL 必需参数
- 授权码交换成功与业务错误、非 2xx 响应
- 飞书三步交换、支付宝 RSA2 签名字段与私钥解析
- Handler：登录 302 与 state 签发、回调签发租户绑定 JWT 与会话、state 单次使用、伪造 state 拒绝、跨域 redirect_uri 拒绝、provider 错误 502、无会话存储时 fail-closed 503、Registry 校验

## 6. 上生产前仍需完成

1. 外部身份绑定表：保存 `provider + provider 租户/组织 + provider subject` 与平台账号的绑定关系，不能仅按 email 绑定。
2. 按租户的 provider 配置存储（数据库 + Secret 引用），替代单实例环境变量配置。
3. 统一 Admin Console 与 Auth Agent 的路径和 callback 响应协议。
4. OIDC Discovery/JWKS 接入（JWKS 已实现，Discovery 自动化仍待补齐）。
5. 真实沙箱/测试租户的端到端登录验证；日志不得输出 token 或密钥。

## 7. 相关文件

- `services/platform-go/internal/authagent/providers/`（registry.go、handler.go、各平台适配器）
- `services/platform-go/internal/authagent/sso/sso.go`
- `services/platform-go/internal/authagent/sso/handlers.go`
- `services/platform-go/cmd/authagent/main.go`（provider 环境变量装配）
- `services/platform-go/internal/platform/middleware/jwt.go`
- `services/platform-go/internal/platform/sessionissuer/issuer.go`
- `services/platform-go/internal/orchestrator/session/sql_store.go`
- `deploy/local-stack/config/deploy.env.example`
