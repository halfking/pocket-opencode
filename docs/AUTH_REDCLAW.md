# RedClaw 认证系统文档

> **文档版本**: 1.0  
> **最后更新**: 2026-09-05  
> **维护者**: RedClaw Platform Team

## 目录

- [第一部分：架构概览](#第一部分架构概览)
- [第二部分：核心概念](#第二部分核心概念)
- [第三部分：认证流程](#第三部分认证流程)
- [第四部分：集成指南](#第四部分集成指南)
- [第五部分：数据模型](#第五部分数据模型)
- [第六部分：安全最佳实践](#第六部分安全最佳实践)
- [第七部分：运维指南](#第七部分运维指南)
- [第八部分：参考信息](#第八部分参考信息)

---

## 第一部分：架构概览

### 1.1 系统简介

RedClaw 认证系统是为**多租户 SaaS 应用**设计的企业级身份认证与授权框架，提供：

- **统一的 SSO 集成**：支持 OIDC/SAML 协议，可对接 Google、Okta、Azure AD 等主流身份提供商
- **JWT 令牌体系**：基于标准 JWT（RS256/HS256）实现无状态认证
- **持久化会话管理**：PostgreSQL 支持的会话存储，提供撤销和审计能力
- **多层安全模型**：从 JWT 验证到租户隔离，再到 RBAC 授权的纵深防御
- **多发行者信任**：支持多个服务独立签发令牌，适配微服务架构

**适用场景**：
- 需要企业级 SSO 单点登录的 B2B SaaS 平台
- 多租户隔离要求严格的应用系统
- 需要细粒度权限控制和审计追踪的业务场景

### 1.2 核心架构

RedClaw 认证系统由三个核心组件构成：

```
┌─────────────────────────────────────────────────────────────────┐
│                         客户端应用                                │
│                   (Admin Console / CLI / API)                   │
└────────────┬────────────────────────────────────────────────────┘
             │
             │ ① SSO 登录请求
             │ ② Bearer Token 认证
             ├──────────────────────────────────────────────┐
             │                                              │
             ▼                                              ▼
┌────────────────────────┐                    ┌────────────────────────┐
│   Auth Agent 认证代理   │                    │   JWT Middleware       │
│ ────────────────────── │                    │   JWT 中间件           │
│ • SSO 登录流程          │◄───────────────────│ ────────────────────── │
│ • 授权码交换            │  ③ 会话创建        │ • Bearer Token 解析    │
│ • ID Token 验证         │                    │ • 签名验证 (HS256/RS256)│
│ • 租户 SSO 绑定         │                    │ • Claims 提取           │
│ • 审批工作流            │                    │ • 租户 ID 验证          │
└────────────┬───────────┘                    └───────────┬────────────┘
             │                                            │
             │ ④ 创建会话                                 │ ⑤ 验证会话
             ▼                                            ▼
┌────────────────────────────────────────────────────────────────┐
│              Session Store 会话存储 (PostgreSQL)                 │
│ ──────────────────────────────────────────────────────────────│
│ • 持久化会话记录 (authagent.sessions 表)                        │
│ • Token Hash 存储 (SHA-256)                                    │
│ • 会话撤销与过期管理                                            │
│ • 最后访问时间跟踪                                              │
└────────────────────────────────────────────────────────────────┘
```

#### 1.2.1 Auth Agent（认证代理）

**职责**：
- 处理 OIDC/SAML SSO 登录流程
- 验证身份提供商（IdP）返回的 ID Token
- 通过 Session Issuer 创建平台会话
- 生成平台 JWT 令牌

**关键特性**：
- PKCE (Proof Key for Code Exchange) 增强安全性
- State + Nonce 参数防止 CSRF 和重放攻击
- 支持多租户 SSO 配置隔离

**代码位置**：
- `services/platform-go/internal/authagent/`
- 配置：`internal/authagent/config.go`
- SSO 实现：`internal/authagent/sso/`

#### 1.2.2 JWT Middleware（JWT 中间件）

**职责**：
- 从 HTTP 请求中提取 Bearer Token
- 验证 JWT 签名和有效期
- 解析 Claims 并注入请求上下文
- 可选的会话有效性检查

**关键特性**：
- 支持 HS256（HMAC 共享密钥）和 RS256（RSA 公私钥对）
- 多发行者信任（Issuer 验证）
- 自动排除健康检查端点（`/healthz`, `/readyz`, `/metrics`）
- 常量时间比较防止时序攻击

**代码位置**：
- `services/platform-go/internal/platform/middleware/jwt.go`
- `services/platform-go/internal/platform/middleware/jidentity.go`

#### 1.2.3 Session Store（会话存储）

**职责**：
- 持久化会话记录到 PostgreSQL
- Token Hash 存储（SHA-256，不可逆）
- 会话撤销和过期管理
- 按主体（Subject）列出会话

**关键特性**：
- Token 哈希存储，数据库不保存明文凭证
- 支持主动撤销和自动过期
- 最后访问时间（last_seen_at）跟踪
- 租户级隔离

**代码位置**：
- `services/platform-go/internal/orchestrator/session/`
- 接口定义：`session/store.go`
- SQL 实现：`session/sql_store.go`

### 1.3 技术栈

| 组件 | 技术选型 | 说明 |
|------|---------|------|
| **SSO 协议** | OIDC (OAuth2 + OpenID Connect) | 标准授权码流程，支持主流 IdP |
| **令牌格式** | JWT (JSON Web Token) | 标准 RFC 7519 |
| **签名算法** | HS256, RS256 | HMAC-SHA256 和 RSA-SHA256 |
| **会话存储** | PostgreSQL + JSONB | 关系型存储 + 灵活元数据 |
| **编程语言** | Go 1.21+ | 高性能、类型安全 |
| **JWT 库** | `github.com/golang-jwt/jwt/v5` | 成熟的 Go JWT 实现 |
| **Web 框架** | Gin | 中间件生态完善 |

---

## 第二部分：核心概念

### 2.1 多租户认证隔离

RedClaw 在每个认证层面都强制执行租户隔离：

#### 2.1.1 Tenant ID 机制

**定义**：每个 JWT Claims 中必须包含 `tenant_id` 字段，标识用户所属租户。

**隔离规则**：
1. **JWT 层**：中间件验证 Token 中的 `tenant_id` 非空
2. **会话层**：Session Store 的所有操作都必须传入 `tenant_id`
3. **业务层**：API 处理程序从上下文获取 `tenant_id`，过滤数据范围

**代码示例**：
```go
// JWT 中间件自动验证租户 ID
claims := &Claims{}
// ...
if strings.TrimSpace(claims.TenantID) == "" {
    return ErrUnauthorized
}
c.Set("tenant_id", claims.TenantID)

// 业务代码获取租户上下文
tenantID := c.GetString("tenant_id")
sessions, err := store.ListBySubject(ctx, userID, tenantID)
```

#### 2.1.2 租户级 SSO 配置

每个租户可以配置独立的 SSO 身份提供商：

- **配置表**：`tenant_sso_configs` 表关联 `tenant_id` 和 SSO Provider
- **验证流程**：Auth Agent 在 SSO 回调后，验证 IdP 是否为该租户的可信提供商
- **隔离保证**：租户 A 的用户无法通过租户 B 的 SSO 配置登录

**数据结构**：
```go
type SSOConfig struct {
    ID           string
    TenantID     string // 所属租户
    Provider     string // "google", "okta", "azure"
    ClientID     string
    ClientSecret string
    IsActive     bool
    Metadata     map[string]any // 提供商特定配置
}
```

#### 2.1.3 跨租户防护

**防护措施**：
1. **Token 绑定**：JWT 的 `tenant_id` Claims 在签发时绑定，无法伪造
2. **API 网关层**：验证请求路径中的 `tenant_id` 与 Token 中的一致
3. **数据库层**：所有查询都包含 `WHERE tenant_id = $1` 条件
4. **会话撤销**：Revoke 操作必须同时匹配 `(session_id, tenant_id)`

**错误示例（已防护）**：
```go
// ❌ 错误：未验证租户匹配
session, err := store.GetByID(ctx, sessionID, requestTenantID)
if err == nil && session.TenantID != tokenTenantID {
    // 此时已经泄露了跨租户信息！
}

// ✅ 正确：Store 内部强制验证
session, err := store.GetByID(ctx, sessionID, tokenTenantID)
// Store 实现中 WHERE session_id = $1 AND tenant_id = $2
```

### 2.2 SSO 集成

#### 2.2.1 OIDC 授权码流程

RedClaw 实现标准的 OIDC Authorization Code Flow：

```mermaid
sequenceDiagram
    participant U as 用户浏览器
    participant A as Auth Agent
    participant I as 身份提供商 (IdP)
    participant S as Session Store

    U->>A: GET /sso/login/google
    A->>A: 生成 state + nonce
    A->>U: 302 Redirect to IdP
    U->>I: GET /authorize?client_id=...&state=...&nonce=...
    I->>I: 用户认证
    I->>U: 302 Redirect to callback
    U->>A: GET /sso/callback/google?code=...&state=...
    A->>A: 验证 state 参数
    A->>I: POST /token (交换 code)
    I->>A: 返回 id_token + access_token
    A->>A: 验证 id_token 签名和 nonce
    A->>A: 检查租户 SSO 绑定
    A->>S: 创建会话
    S->>A: 返回 session_id
    A->>A: 生成平台 JWT
    A->>U: 返回 JWT (Cookie 或 JSON)
```

**关键步骤说明**：

1. **登录发起**：用户点击"通过 Google 登录"
   - Auth Agent 生成随机 `state`（32字节）和 `nonce`（16字节）
   - 存储在内存中的防重放缓存（5 分钟 TTL）

2. **IdP 重定向**：构造授权 URL
   ```
   https://accounts.google.com/o/oauth2/v2/auth?
     client_id=<CLIENT_ID>&
     response_type=code&
     scope=openid+profile+email&
     redirect_uri=https://redclaw.example.com/sso/callback/google&
     state=<RANDOM_STATE>&
     nonce=<RANDOM_NONCE>
   ```

3. **授权码回调**：IdP 验证用户后重定向
   ```
   https://redclaw.example.com/sso/callback/google?
     code=<AUTH_CODE>&
     state=<RANDOM_STATE>
   ```

4. **State 验证**：Auth Agent 验证 `state` 参数
   - 从缓存中查找并消费（防止重放）
   - 使用常量时间比较防止时序攻击

5. **Token 交换**：后端向 IdP 交换令牌
   ```http
   POST https://oauth2.googleapis.com/token
   Content-Type: application/x-www-form-urlencoded

   code=<AUTH_CODE>&
   client_id=<CLIENT_ID>&
   client_secret=<CLIENT_SECRET>&
   redirect_uri=https://redclaw.example.com/sso/callback/google&
   grant_type=authorization_code
   ```

6. **ID Token 验证**：解析和验证 JWT
   - 验证签名（HMAC 或 JWKS 公钥）
   - 验证 `iss`（Issuer）匹配配置的 IdP
   - 验证 `aud`（Audience）匹配 `client_id`
   - 验证 `exp`（过期时间）
   - 验证 `nonce` 匹配初始值

7. **租户绑定验证**：检查 IdP 是否为该租户的可信提供商
   - ID Token 中包含 `tenant` Claim（由 IdP 注入或映射）
   - 查询 `tenant_sso_configs` 表验证绑定关系

8. **会话创建**：通过 Session Issuer 创建持久化会话
   - 生成唯一 `session_id`（UUID）
   - 存储到 PostgreSQL `authagent.sessions` 表
   - Token Hash（SHA-256）存储

9. **平台 JWT 签发**：生成 RedClaw 内部使用的 JWT
   - 包含 `tenant_id`, `sub`, `email`, `session_id`, `roles`
   - 15 分钟有效期（可配置）
   - 使用平台签名密钥（HS256 或 RS256）

#### 2.2.2 PKCE 安全增强

**PKCE（Proof Key for Code Exchange）** 是针对授权码流程的安全扩展，防止授权码拦截攻击。

**实现细节**：
```go
// 1. 登录时生成 code_verifier (43-128 字节随机字符串)
codeVerifier := randomToken(64) // 生成 base64url 编码的随机串

// 2. 计算 code_challenge
sha := sha256.Sum256([]byte(codeVerifier))
codeChallenge := base64.RawURLEncoding.EncodeToString(sha[:])

// 3. 授权请求中附加 PKCE 参数
authorizeURL := fmt.Sprintf("%s?...&code_challenge=%s&code_challenge_method=S256", 
    issuer, codeChallenge)

// 4. Token 交换时提供 code_verifier
tokenRequest := url.Values{
    "code":          {authCode},
    "code_verifier": {codeVerifier},
    // ...
}
```

**安全保障**：
- 即使授权码被拦截，攻击者没有 `code_verifier` 也无法交换令牌
- `code_challenge` 是单向哈希，无法反推 `code_verifier`

#### 2.2.3 支持的身份提供商

RedClaw 通过标准 OIDC 协议支持任何兼容的 IdP：

| 提供商 | 配置示例 | 说明 |
|--------|---------|------|
| **Google** | `issuer: https://accounts.google.com` | Google Workspace 企业账户 |
| **Okta** | `issuer: https://<your-domain>.okta.com` | 企业级 IdP，支持 SAML/OIDC |
| **Azure AD** | `issuer: https://login.microsoftonline.com/<tenant-id>/v2.0` | Microsoft 365 账户 |
| **Auth0** | `issuer: https://<your-tenant>.auth0.com` | 开发者友好的 IdP |
| **Keycloak** | `issuer: https://<your-domain>/auth/realms/<realm>` | 开源 IdP，可自托管 |

**配置模板**：
```go
type SSOConfig struct {
    Provider     string   `json:"provider"`      // "google", "okta", etc.
    Issuer       string   `json:"issuer"`        // OIDC 发现端点
    ClientID     string   `json:"client_id"`
    ClientSecret string   `json:"client_secret"` // 敏感信息，存储时加密
    RedirectURL  string   `json:"redirect_url"`  // https://your-app.com/sso/callback/google
    Scopes       []string `json:"scopes"`        // ["openid", "profile", "email"]
}
```

#### 2.2.4 验证式 SSO 流程

RedClaw 实现了"验证式 SSO"（Verified SSO Flow），确保只有可信的 IdP 可以为特定租户创建会话。

**验证步骤**：
1. **IdP 可信性检查**：查询 `tenant_sso_configs` 表
2. **Tenant Claim 映射**：ID Token 必须包含 `tenant` Claim
3. **绑定关系验证**：验证 `(tenant_id, provider)` 的绑定关系有效且 `is_active = true`
4. **审计日志**：记录 SSO 登录事件，包含 IdP 信息和用户标识

**代码示例**：
```go
// HandleCallback 中的租户验证逻辑
claims, err := m.ParseIDToken(idToken)
if err != nil {
    return nil, invalidCallback(err)
}

// 验证 tenant Claim 存在
if strings.TrimSpace(claims.Tenant) == "" {
    return nil, invalidCallback(errors.New("id_token missing trusted tenant claim"))
}

// 可选：查询数据库验证 SSO 配置有效性
ssoConfig, err := dal.GetTenantSSOConfig(ctx, claims.Tenant, provider)
if err != nil || !ssoConfig.IsActive {
    return nil, errors.New("SSO provider not trusted for this tenant")
}
```

### 2.3 JWT Token 架构

#### 2.3.1 Claims 结构

RedClaw 使用以下标准化的 JWT Claims：

```go
type Claims struct {
    // 标准 Claims (RFC 7519)
    Issuer    string   `json:"iss"`       // "redclaw" 或服务特定的发行者
    Subject   string   `json:"sub"`       // 用户唯一标识 (通常是 User ID)
    Audience  []string `json:"aud"`       // ["redclaw-api", "redclaw"]
    ExpiresAt int64    `json:"exp"`       // Unix 时间戳（秒）
    IssuedAt  int64    `json:"iat"`       // Unix 时间戳（秒）
    NotBefore int64    `json:"nbf"`       // Unix 时间戳（秒）
    
    // RedClaw 扩展 Claims
    Email      string   `json:"email,omitempty"`      // 用户邮箱
    TenantID   string   `json:"tenant_id"`            // 租户 ID（必需）
    SessionID  string   `json:"session_id,omitempty"` // 会话 ID（用于撤销）
    Roles      []string `json:"roles,omitempty"`      // 角色列表 ["admin", "user"]
    AuthMethod string   `json:"auth_method,omitempty"`// "sso", "api_key", "internal"
}
```

**Claims 说明**：

| 字段 | 必需 | 说明 |
|------|------|------|
| `iss` | ✅ | 令牌发行者，用于多发行者信任验证 |
| `sub` | ✅ | 用户主体标识，必须在租户内唯一 |
| `aud` | ✅ | 令牌接收方，中间件会验证 |
| `exp` | ✅ | 过期时间，通常 15 分钟到 1 小时 |
| `iat` | ✅ | 签发时间，用于审计和调试 |
| `nbf` | 可选 | 生效时间，通常设置为 `iat - 30秒` 防止时钟偏移 |
| `tenant_id` | ✅ | RedClaw 强制要求，用于多租户隔离 |
| `session_id` | 推荐 | 用于会话撤销和审计追踪 |
| `roles` | 可选 | 用于 RBAC 授权，如 `["admin", "billing:read"]` |
| `auth_method` | 可选 | 标识认证方式，便于审计和策略控制 |

#### 2.3.2 签名方法

RedClaw 支持两种 JWT 签名算法：

##### HS256（HMAC-SHA256）

**适用场景**：单体应用或服务间共享密钥的场景

**优点**：
- 配置简单，只需一个共享密钥
- 签名和验证性能高
- 密钥管理相对简单

**缺点**：
- 密钥泄露风险高（签名和验证使用同一密钥）
- 不适合需要公开验证的场景

**配置示例**：
```bash
# 环境变量（至少 32 字节）
GATEWAY_JWT_SIGNING_KEY="your-secret-key-at-least-32-bytes-long"
GATEWAY_JWT_ISSUER="redclaw-gateway"
GATEWAY_JWT_AUDIENCE="redclaw-api"
```

**代码示例**：
```go
cfg := JWTConfig{
    SigningKey: []byte(os.Getenv("GATEWAY_JWT_SIGNING_KEY")),
    Issuer:     "redclaw-gateway",
    Audience:   "redclaw-api",
}

// 签发 Token
token, err := IssueToken(cfg, claims, 15*time.Minute)

// 验证 Token（中间件自动处理）
router.Use(JWTMiddleware(cfg))
```

##### RS256（RSA-SHA256）

**适用场景**：微服务架构，需要公钥分发的场景

**优点**：
- 私钥签名，公钥验证，安全性更高
- 适合跨服务验证，无需共享私钥
- 支持 JWKS（JSON Web Key Set）动态公钥分发

**缺点**：
- 配置复杂，需要管理公私钥对
- 签名和验证性能略低于 HS256

**密钥生成**：
```bash
# 生成 RSA 私钥（2048 位）
openssl genrsa -out private_key.pem 2048

# 提取公钥
openssl rsa -in private_key.pem -pubout -out public_key.pem

# 转换为 PKCS#8 格式（可选）
openssl pkcs8 -topk8 -inform PEM -outform PEM -in private_key.pem -out private_key_pkcs8.pem -nocrypt
```

**配置示例**：
```go
// 签发服务（Auth Agent）
privateKey, err := jwt.ParseRSAPrivateKeyFromPEM(privateKeyPEM)
token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
tokenString, err := token.SignedString(privateKey)

// 验证服务（API Gateway）
publicKey, err := jwt.ParseRSAPublicKeyFromPEM(publicKeyPEM)
cfg := JWTConfig{
    SigningKey: publicKey, // 实际使用时需要类型断言
    Issuer:     "redclaw-auth",
    Audience:   "redclaw-api",
}
```

#### 2.3.3 Token 生命周期

**典型生命周期**：
```
[签发] ────> [使用期] ────> [过期] ────> [撤销/清理]
   │           15分钟         │             │
   │                          │             │
   iat                       exp         cleanup
```

**时间参数配置**：
```go
now := time.Now()
claims := Claims{
    IssuedAt:  jwt.NewNumericDate(now),                    // 签发时间
    NotBefore: jwt.NewNumericDate(now.Add(-30*time.Second)), // 提前30秒生效（容忍时钟偏移）
    ExpiresAt: jwt.NewNumericDate(now.Add(15*time.Minute)),  // 15分钟后过期
}
```

**过期策略**：
- **短期 Token**：15 分钟（用户 Session）
- **服务间 Token**：1 小时（内部服务调用）
- **API Key Token**：24 小时（自动化脚本）

**刷新机制**：
- RedClaw 不使用 Refresh Token，而是依赖持久化会话
- Token 过期后，客户端携带 Session ID 请求重新签发
- Session Store 验证会话有效性后，签发新 Token

### 2.4 会话发行者（Session Issuer）模式

#### 2.4.1 设计理念

**Session Issuer** 是 RedClaw 认证系统的核心抽象，负责统一管理会话创建逻辑：

- **关注点分离**：Auth Agent 处理 SSO 协议，Session Issuer 处理会话持久化
- **可插拔设计**：接口驱动，可替换为不同的存储后端（SQL、Redis、内存）
- **审计友好**：所有会话创建集中记录，便于审计追踪

**核心接口**：
```go
type Issuer struct {
    store     session.Store
    jwtConfig middleware.JWTConfig
}

type Params struct {
    TenantID  string
    SubjectID string
    Issuer    string         // SSO provider or "api_key"
    TTL       time.Duration  // 会话有效期
    Metadata  map[string]any // 自定义元数据（UA、IP等）
}

type Result struct {
    SessionID string
    Token     string // 平台 JWT
    ExpiresAt time.Time
}

// Issue 创建会话并返回 JWT
func (i *Issuer) Issue(ctx context.Context, params Params, mintToken func(sessionID string, expiresAt time.Time) (string, error)) (string, Result, error)
```

#### 2.4.2 集中化会话创建

**流程图**：
```
┌─────────────┐         ┌──────────────────┐         ┌──────────────┐
│ SSO Manager │────────>│ Session Issuer   │────────>│ Session Store│
│             │         │                  │         │ (PostgreSQL) │
│ • 验证 IdP  │  调用   │ • 生成 Session ID│  持久化  │ • 存储会话   │
│ • 解析 Claims│ Issue() │ • 调用 mintToken │         │ • Token Hash │
│             │         │ • 持久化会话      │         │              │
└─────────────┘         └──────────────────┘         └──────────────┘
                                 │
                                 ▼
                          返回 JWT + Session ID
```

**代码示例**：
```go
// SSO 回调处理
jwtStr, result, err := m.SessionIssuer.Issue(ctx, 
    sessionissuer.Params{
        TenantID:  claims.Tenant,
        SubjectID: claims.Sub,
        Issuer:    "redclaw.sso",
        TTL:       15 * time.Minute,
        Metadata: map[string]any{
            "idp_session_id": claims.SessionID,
            "user_agent":     req.UserAgent(),
            "ip_address":     req.RemoteAddr,
        },
    }, 
    func(sessionID string, expiresAt time.Time) (string, error) {
        // mintToken 回调：将 session_id 注入 JWT Claims
        copy := *claims
        copy.SessionID = sessionID
        return m.mintPlatformJWT(&copy, expiresAt)
    },
)
```

**关键优势**：
1. **原子性**：会话创建和 JWT 签发在同一事务中
2. **一致性**：JWT 中的 `session_id` 必然对应数据库记录
3. **可追溯**：所有会话创建都经过 Issuer，便于审计

#### 2.4.3 多发行者信任

RedClaw 支持多个服务独立签发 JWT，同时保证互信验证：

**发行者类型**：
| 发行者 | Issuer Claim | 签名密钥 | 用途 |
|--------|-------------|---------|------|
| Auth Agent | `redclaw-auth` | `AUTH_JWT_SIGNING_KEY` | SSO 登录后签发 |
| Gateway | `redclaw-gateway` | `GATEWAY_JWT_SIGNING_KEY` | API 网关签发的临时令牌 |
| Orchestrator | `redclaw-orchestrator` | `ORCHESTRATOR_JWT_SIGNING_KEY` | 编排服务内部令牌 |
| Admin Console | `redclaw-admin` | `ADMIN_JWT_SIGNING_KEY` | 管理后台专用令牌 |

**信任配置**：
```go
// 服务 A 签发 Token
issuerACfg := JWTConfig{
    SigningKey: []byte("service-a-secret-key"),
    Issuer:     "redclaw-service-a",
    Audience:   "redclaw-api",
}
tokenA, _ := IssueToken(issuerACfg, claims, 15*time.Minute)

// 服务 B 验证 Token（需要知道 A 的签名密钥或公钥）
issuerBCfg := JWTConfig{
    SigningKey: []byte("service-a-secret-key"), // 共享密钥或 A 的公钥
    Issuer:     "redclaw-service-a",            // 验证发行者
    Audience:   "redclaw-api",
}
router.Use(JWTMiddleware(issuerBCfg))
```

**JWKS 支持**（未来）：
- 各服务暴露 `/.well-known/jwks.json` 端点
- 验证方动态获取公钥，无需预配置

#### 2.4.4 Token 轮换和撤销

**轮换机制**（Token Rotation）：
```go
// 客户端检测到 Token 即将过期（如剩余 < 5 分钟）
if time.Until(tokenExpiresAt) < 5*time.Minute {
    // 携带当前 Token 请求刷新
    newToken, err := client.RefreshToken(ctx, currentToken)
    // 刷新成功后，旧 Token 在数据库中标记为已轮换（可选）
}
```

**撤销机制**（Token Revocation）：
```go
// 主动撤销（用户登出）
err := store.Revoke(ctx, sessionID, tenantID, "self", "logout")

// 管理员强制撤销
err := store.Revoke(ctx, sessionID, tenantID, "admin:12345", "security_incident")

// 中间件在每次请求时检查会话状态
if cfg.SessionValidator != nil {
    revoked, err := ValidateSession(ctx, validator, sessionID, tenantID, subject)
    if revoked {
        return http.StatusUnauthorized
    }
}
```

**撤销传播**：
- Session Store 的 Revoke 操作立即生效
- 中间件在下一次请求时检测到撤销状态
- 可选：通过 Redis Pub/Sub 或 Notification 系统实时广播撤销事件

### 2.5 多层安全模型

RedClaw 实现了纵深防御的五层安全架构：

```
┌────────────────────────────────────────────────────────────┐
│ L5: Ed25519 双签名 (关键控制命令)                            │
│ ─────────────────────────────────────────────────────────  │
│ • 资源删除、权限变更等高风险操作                              │
│ • 要求客户端使用私钥签名请求体                                │
│ • 服务端验证公钥签名 + JWT                                   │
└────────────────────────────────────────────────────────────┘
                             ▲
┌────────────────────────────────────────────────────────────┐
│ L4: 基于角色的访问控制 (RBAC)                                │
│ ─────────────────────────────────────────────────────────  │
│ • RequireRole() 中间件检查 JWT Claims.Roles                 │
│ • 细粒度权限：如 "billing:read", "users:write"              │
│ • 租户级角色隔离                                             │
└────────────────────────────────────────────────────────────┘
                             ▲
┌────────────────────────────────────────────────────────────┐
│ L3: 会话有效性检查 (可选)                                    │
│ ─────────────────────────────────────────────────────────  │
│ • 查询 Session Store 验证 session_id 未被撤销                │
│ • 检查 last_seen_at 是否超出最大空闲时间                      │
│ • 应对 Token 盗用场景                                        │
└────────────────────────────────────────────────────────────┘
                             ▲
┌────────────────────────────────────────────────────────────┐
│ L2: 租户 ID 验证                                            │
│ ─────────────────────────────────────────────────────────  │
│ • 验证 JWT Claims.TenantID 非空                             │
│ • 业务层所有数据查询过滤 tenant_id                           │
│ • 防止跨租户数据泄露                                         │
└────────────────────────────────────────────────────────────┘
                             ▲
┌────────────────────────────────────────────────────────────┐
│ L1: JWT 认证                                                │
│ ─────────────────────────────────────────────────────────  │
│ • 验证 JWT 签名（HS256/RS256）                              │
│ • 验证 Issuer、Audience、ExpiresAt                          │
│ • 常量时间比较防止时序攻击                                    │
└────────────────────────────────────────────────────────────┘
```

**各层详解**：

#### L1: JWT 认证
- **防护目标**：防止未认证访问
- **实现方式**：JWT 中间件自动验证
- **失败处理**：返回 401 Unauthorized

#### L2: 租户 ID 验证
- **防护目标**：防止跨租户数据泄露
- **实现方式**：Claims 中 `tenant_id` 必填，业务层查询过滤
- **失败处理**：返回 401 或 403

#### L3: 会话有效性检查
- **防护目标**：防止 Token 被盗用后持续使用
- **实现方式**：查询 Session Store，检查撤销状态
- **失败处理**：返回 401，强制重新登录

#### L4: RBAC 授权
- **防护目标**：防止越权访问
- **实现方式**：`RequireRole()` 中间件检查 `roles` Claims
- **失败处理**：返回 403 Forbidden

#### L5: Ed25519 双签名
- **防护目标**：防止高权限操作被 Token 盗用者执行
- **实现方式**：客户端使用 Ed25519 私钥签名请求体，服务端验证
- **失败处理**：返回 403，记录安全事件

**代码示例（分层防护）**：
```go
// L1 + L2: JWT 认证和租户验证（自动）
router.Use(JWTMiddleware(cfg))

// L3: 会话有效性检查（可选）
cfgWithSession := cfg
cfgWithSession.SessionValidator = sessionStore
router.Use(JWTMiddleware(cfgWithSession))

// L4: RBAC 授权
adminRoutes := router.Group("/admin")
adminRoutes.Use(RequireRole("admin", "manager"))

// L5: 双签名验证（业务层）
func DeleteUserHandler(c *gin.Context) {
    // 获取 JWT Claims（L1+L2 已验证）
    claims, _ := GetClaims(c)
    
    // 验证角色（L4）
    if !claims.HasRole("admin") {
        c.JSON(403, gin.H{"error": "forbidden"})
        return
    }
    
    // 验证 Ed25519 签名（L5）
    signature := c.GetHeader("X-Signature")
    body, _ := c.GetRawData()
    if !VerifyEd25519Signature(claims.Subject, body, signature) {
        c.JSON(403, gin.H{"error": "invalid signature"})
        return
    }
    
    // 执行删除操作
    // ...
}
```

---

## 第三部分：认证流程

### 3.1 SSO 登录流程

完整的 SSO 登录流程包含以下步骤：

```mermaid
sequenceDiagram
    autonumber
    participant User as 用户浏览器
    participant AdminUI as Admin Console
    participant AuthAgent as Auth Agent
    participant IdP as 身份提供商 (Google/Okta)
    participant SessionStore as Session Store (PostgreSQL)
    
    User->>AdminUI: 点击"SSO 登录"
    AdminUI->>AuthAgent: GET /sso/login/google?origin=admin
    AuthAgent->>AuthAgent: 生成 state (32字节) + nonce (16字节)
    AuthAgent->>AuthAgent: 存储到防重放缓存 (TTL 5分钟)
    AuthAgent->>User: 302 Redirect to IdP
    Note over User,IdP: https://accounts.google.com/o/oauth2/v2/auth?<br/>client_id=...&state=...&nonce=...
    
    User->>IdP: GET /authorize (302 跳转)
    IdP->>User: 显示登录页面
    User->>IdP: 输入凭证并授权
    IdP->>IdP: 验证用户身份
    IdP->>User: 302 Redirect to callback
    Note over IdP,User: https://redclaw.example.com/sso/callback/google?<br/>code=AUTH_CODE&state=STATE
    
    User->>AuthAgent: GET /sso/callback/google?code=...&state=...
    AuthAgent->>AuthAgent: 验证 state 参数 (防 CSRF)
    AuthAgent->>AuthAgent: 从缓存消费 nonce
    AuthAgent->>IdP: POST /token (交换授权码)
    Note over AuthAgent,IdP: code=AUTH_CODE&client_id=...&client_secret=...
    IdP->>AuthAgent: 返回 id_token + access_token
    
    AuthAgent->>AuthAgent: 验证 id_token 签名
    AuthAgent->>AuthAgent: 验证 iss, aud, exp, nonce
    AuthAgent->>AuthAgent: 提取 Claims (sub, email, tenant)
    AuthAgent->>AuthAgent: 检查租户 SSO 绑定
    
    AuthAgent->>SessionStore: 创建会话
    Note over AuthAgent,SessionStore: session_id, tenant_id, subject_id,<br/>token_hash, expires_at, metadata
    SessionStore->>AuthAgent: 返回 session_id
    
    AuthAgent->>AuthAgent: 生成平台 JWT
    Note over AuthAgent: Claims: {tenant_id, sub, session_id,<br/>email, roles, auth_method: "sso"}
    AuthAgent->>User: 返回 JWT (Set-Cookie 或 JSON)
    User->>AdminUI: 携带 JWT 访问受保护资源
    AdminUI->>AuthAgent: API 请求 (Authorization: Bearer JWT)
```

**关键节点说明**：

**步骤 1-4**：登录发起
- Admin Console 构造登录 URL：`/sso/login/google?origin=admin`
- Auth Agent 生成随机 `state` 和 `nonce`，存储到进程内存（5 分钟 TTL）
- 重定向到 Google OAuth 授权页面

**步骤 5-8**：用户授权
- 用户在 IdP 页面输入凭证（邮箱、密码、2FA 等）
- IdP 验证通过后，重定向回 callback URL，附带 `code` 和 `state`

**步骤 9-13**：Token 交换和验证
- Auth Agent 验证 `state` 防止 CSRF 攻击
- 后端向 IdP 的 `/token` 端点交换授权码，获取 `id_token`
- 验证 `id_token` 的签名、Issuer、Audience、过期时间、Nonce

**步骤 14-15**：租户绑定验证
- 从 `id_token` 提取 `tenant` Claim（由 IdP 注入或通过映射规则）
- 查询 `tenant_sso_configs` 表，验证该 IdP 是否为该租户的可信提供商

**步骤 16-17**：会话持久化
- 调用 Session Issuer 创建会话记录
- 生成唯一 `session_id`，计算 Token Hash（SHA-256）
- 存储到 `authagent.sessions` 表

**步骤 18-20**：JWT 签发和返回
- 构造平台 JWT Claims，包含 `session_id`
- 使用平台签名密钥签发 JWT（HS256 或 RS256）
- 通过 Cookie 或 JSON 响应返回给客户端

### 3.2 API 请求认证流程

用户持有 JWT 后，每次 API 请求都经过以下认证流程：

```mermaid
sequenceDiagram
    autonumber
    participant Client as 客户端
    participant Middleware as JWT 中间件
    participant SessionValidator as 会话验证器 (可选)
    participant Handler as 业务处理程序
    
    Client->>Middleware: HTTP Request<br/>Authorization: Bearer <JWT>
    
    alt 健康检查端点
        Middleware->>Handler: 跳过认证 (/healthz, /readyz, /metrics)
    else 业务端点
        Middleware->>Middleware: 提取 Bearer Token
        alt Token 缺失
            Middleware->>Client: 401 Unauthorized<br/>{"error": "missing bearer token"}
        else Token 存在
            Middleware->>Middleware: 解析 JWT
            Middleware->>Middleware: 验证签名 (HS256/RS256)
            alt 签名无效或过期
                Middleware->>Client: 401 Unauthorized<br/>{"error": "invalid token"}
            else 签名有效
                Middleware->>Middleware: 提取 Claims
                Middleware->>Middleware: 验证 tenant_id, sub 非空
                alt Claims 缺失必需字段
                    Middleware->>Client: 401 Unauthorized<br/>{"error": "token missing required claims"}
                else Claims 有效
                    Middleware->>Middleware: 注入上下文<br/>c.Set("tenant_id", claims.TenantID)<br/>c.Set("subject", claims.Subject)
                    
                    opt 启用会话验证
                        Middleware->>SessionValidator: ValidateSession(session_id, tenant_id, subject)
                        SessionValidator->>SessionValidator: 查询数据库<br/>WHERE session_id = $1 AND tenant_id = $2
                        alt 会话已撤销或过期
                            SessionValidator->>Middleware: revoked = true
                            Middleware->>Client: 401 Unauthorized<br/>{"error": "session revoked"}
                        else 会话有效
                            SessionValidator->>Middleware: revoked = false
                        end
                    end
                    
                    Middleware->>Handler: c.Next() (传递给业务处理程序)
                    Handler->>Handler: 获取租户上下文<br/>tenantID := c.GetString("tenant_id")
                    Handler->>Handler: 执行业务逻辑
                    Handler->>Client: 200 OK + 业务响应
                end
            end
        end
    end
```

**代码示例（客户端）**：
```go
// Go 客户端示例
token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."
req, _ := http.NewRequest("GET", "https://api.redclaw.example.com/v1/sessions", nil)
req.Header.Set("Authorization", "Bearer "+token)

resp, err := http.DefaultClient.Do(req)
if err != nil {
    // 处理网络错误
}
defer resp.Body.Close()

switch resp.StatusCode {
case 200:
    // 认证成功，处理响应
case 401:
    // Token 无效或过期，需要重新登录
case 403:
    // Token 有效但权限不足
}
```

```javascript
// JavaScript 客户端示例
const token = localStorage.getItem('jwt_token');

fetch('https://api.redclaw.example.com/v1/sessions', {
    method: 'GET',
    headers: {
        'Authorization': `Bearer ${token}`,
        'Content-Type': 'application/json'
    }
})
.then(response => {
    if (response.status === 401) {
        // Token 过期，重定向到登录页
        window.location.href = '/login';
    }
    return response.json();
})
.then(data => {
    // 处理响应数据
});
```

### 3.3 会话撤销流程

RedClaw 支持主动撤销会话，用于以下场景：
- 用户主动登出
- 管理员强制登出用户
- 检测到异常行为（如 Token 盗用）

```mermaid
sequenceDiagram
    autonumber
    participant Client as 客户端
    participant API as API 端点
    participant SessionStore as Session Store
    participant Notification as 通知系统 (可选)
    participant Middleware as JWT 中间件
    
    alt 场景1: 用户主动登出
        Client->>API: POST /auth/logout<br/>Authorization: Bearer JWT
        API->>API: 提取 session_id from Claims
        API->>SessionStore: Revoke(session_id, tenant_id, "self", "logout")
    else 场景2: 管理员强制登出
        Client->>API: POST /admin/users/{user_id}/sessions/{session_id}/revoke<br/>Authorization: Bearer ADMIN_JWT
        API->>API: 验证管理员权限
        API->>SessionStore: Revoke(session_id, tenant_id, "admin:{admin_id}", "security_incident")
    end
    
    SessionStore->>SessionStore: UPDATE authagent.sessions<br/>SET revoked_at = NOW(),<br/>    revoked_by = $1,<br/>    revoke_reason = $2<br/>WHERE session_id = $3<br/>  AND tenant_id = $4
    
    alt 会话不存在或已撤销
        SessionStore->>API: ErrNotFound / ErrExpired
        API->>Client: 404 Not Found 或 410 Gone
    else 撤销成功
        SessionStore->>API: 返回已撤销的 Session 对象
        
        opt 启用通知系统
            SessionStore->>Notification: 发布撤销事件<br/>{session_id, tenant_id, revoked_at}
            Notification->>Notification: 广播到所有服务实例
        end
        
        API->>Client: 200 OK<br/>{"message": "session revoked"}
    end
    
    Note over Client,Middleware: 后续请求使用该 JWT
    Client->>Middleware: GET /api/protected<br/>Authorization: Bearer JWT
    Middleware->>SessionStore: ValidateSession(session_id, tenant_id, subject)
    SessionStore->>SessionStore: SELECT revoked_at FROM sessions<br/>WHERE session_id = $1 AND tenant_id = $2
    alt revoked_at IS NOT NULL
        SessionStore->>Middleware: revoked = true
        Middleware->>Client: 401 Unauthorized<br/>{"error": "session revoked"}
    end
```

**代码示例（服务端）**：
```go
// 用户主动登出
func LogoutHandler(c *gin.Context) {
    claims, _ := middleware.GetClaims(c)
    
    err := sessionStore.Revoke(
        c.Request.Context(),
        claims.SessionID,
        claims.TenantID,
        "self",
        "logout",
    )
    
    if err != nil {
        if errors.Is(err, session.ErrNotFound) {
            c.JSON(404, gin.H{"error": "session not found"})
            return
        }
        c.JSON(500, gin.H{"error": "internal server error"})
        return
    }
    
    c.JSON(200, gin.H{"message": "logged out successfully"})
}

// 管理员强制撤销
func AdminRevokeSessionHandler(c *gin.Context) {
    adminClaims, _ := middleware.GetClaims(c)
    if !adminClaims.HasRole("admin") {
        c.JSON(403, gin.H{"error": "forbidden"})
        return
    }
    
    sessionID := c.Param("session_id")
    tenantID := c.Param("tenant_id")
    
    err := sessionStore.Revoke(
        c.Request.Context(),
        sessionID,
        tenantID,
        fmt.Sprintf("admin:%s", adminClaims.Subject),
        "admin_forced_logout",
    )
    
    // 错误处理...
}
```

### 3.4 Token 刷新流程

RedClaw 不使用传统的 Refresh Token 机制，而是依赖持久化会话进行 Token 刷新：

```mermaid
sequenceDiagram
    autonumber
    participant Client as 客户端
    participant API as /auth/refresh 端点
    participant SessionStore as Session Store
    participant JWTIssuer as JWT 签发器
    
    Note over Client: Token 即将过期<br/>(检测 exp claim)
    
    Client->>API: POST /auth/refresh<br/>Authorization: Bearer OLD_JWT
    API->>API: 解析 OLD_JWT<br/>(即使已过期也接受，使用 leeway)
    API->>API: 提取 session_id, tenant_id, subject
    
    API->>SessionStore: GetByID(session_id, tenant_id)
    SessionStore->>SessionStore: SELECT * FROM sessions<br/>WHERE session_id = $1<br/>  AND tenant_id = $2
    
    alt 会话不存在
        SessionStore->>API: ErrNotFound
        API->>Client: 401 Unauthorized<br/>{"error": "session not found, please login"}
    else 会话已过期或撤销
        SessionStore->>API: Session with revoked_at != NULL
        API->>Client: 401 Unauthorized<br/>{"error": "session revoked, please login"}
    else 会话有效
        SessionStore->>API: 返回 Session 对象
        API->>API: 验证 session.SubjectID == claims.Subject
        alt Subject 不匹配
            API->>Client: 401 Unauthorized<br/>{"error": "token subject mismatch"}
        else Subject 匹配
            API->>JWTIssuer: IssueToken(claims, 15*time.Minute)
            JWTIssuer->>JWTIssuer: 生成新 JWT<br/>• 新的 iat, exp<br/>• 保留 session_id, tenant_id, sub
            JWTIssuer->>API: 返回 NEW_JWT
            
            opt 更新最后访问时间
                API->>SessionStore: UPDATE sessions<br/>SET last_seen_at = NOW()<br/>WHERE session_id = $1
            end
            
            API->>Client: 200 OK<br/>{"token": "NEW_JWT", "expires_in": 900}
        end
    end
```

**客户端自动刷新逻辑**：
```javascript
// JavaScript 客户端示例
class AuthClient {
    constructor() {
        this.token = localStorage.getItem('jwt_token');
        this.refreshThreshold = 5 * 60 * 1000; // 过期前 5 分钟刷新
        this.startTokenRefreshTimer();
    }
    
    startTokenRefreshTimer() {
        setInterval(async () => {
            const expiresAt = this.getTokenExpiry();
            const timeUntilExpiry = expiresAt - Date.now();
            
            if (timeUntilExpiry < this.refreshThreshold && timeUntilExpiry > 0) {
                await this.refreshToken();
            }
        }, 60 * 1000); // 每分钟检查一次
    }
    
    getTokenExpiry() {
        if (!this.token) return 0;
        const payload = JSON.parse(atob(this.token.split('.')[1]));
        return payload.exp * 1000; // 转换为毫秒
    }
    
    async refreshToken() {
        try {
            const response = await fetch('https://api.redclaw.example.com/auth/refresh', {
                method: 'POST',
                headers: {
                    'Authorization': `Bearer ${this.token}`
                }
            });
            
            if (response.ok) {
                const data = await response.json();
                this.token = data.token;
                localStorage.setItem('jwt_token', data.token);
                console.log('Token refreshed successfully');
            } else if (response.status === 401) {
                // 会话过期，重定向到登录页
                window.location.href = '/login';
            }
        } catch (error) {
            console.error('Token refresh failed:', error);
        }
    }
}

const authClient = new AuthClient();
```

**服务端刷新端点**：
```go
func RefreshTokenHandler(c *gin.Context) {
    // 使用宽松的解析器，允许已过期的 Token（leeway 5 分钟）
    cfg := middleware.JWTConfig{
        SigningKey: jwtSigningKey,
        Issuer:     "redclaw",
        Audience:   "redclaw-api",
        Leeway:     5 * time.Minute,
    }
    
    parser := jwt.NewParser(
        jwt.WithLeeway(cfg.Leeway),
        jwt.WithIssuer(cfg.Issuer),
        jwt.WithAudience(cfg.Audience),
    )
    
    tokenStr := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
    claims := &middleware.Claims{}
    _, err := parser.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
        return cfg.SigningKey, nil
    })
    
    if err != nil {
        c.JSON(401, gin.H{"error": "invalid token"})
        return
    }
    
    // 验证会话有效性
    sess, err := sessionStore.GetByID(c.Request.Context(), claims.SessionID, claims.TenantID)
    if err != nil {
        if errors.Is(err, session.ErrNotFound) || errors.Is(err, session.ErrExpired) {
            c.JSON(401, gin.H{"error": "session expired, please login"})
            return
        }
        c.JSON(500, gin.H{"error": "internal server error"})
        return
    }
    
    // 验证 Subject 匹配
    if sess.SubjectID != claims.Subject {
        c.JSON(401, gin.H{"error": "token subject mismatch"})
        return
    }
    
    // 签发新 Token
    newToken, err := middleware.IssueToken(cfg, *claims, 15*time.Minute)
    if err != nil {
        c.JSON(500, gin.H{"error": "failed to issue token"})
        return
    }
    
    c.JSON(200, gin.H{
        "token":      newToken,
        "expires_in": 900, // 15 分钟 = 900 秒
    })
}
```

---

## 第四部分：集成指南

### 4.1 环境配置

#### 4.1.1 必需的环境变量

RedClaw 认证系统需要以下环境变量配置：

##### JWT 签名密钥配置

每个服务需要独立的 JWT 签名密钥（**生产环境必须至少 32 字节**）：

```bash
# Auth Agent (SSO 登录后签发)
AUTH_JWT_SIGNING_KEY="your-auth-service-secret-at-least-32-bytes-long"
AUTH_JWT_ISSUER="redclaw-auth"
AUTH_JWT_AUDIENCE="redclaw-api"

# Gateway (API 网关)
GATEWAY_JWT_SIGNING_KEY="your-gateway-secret-at-least-32-bytes-long"
GATEWAY_JWT_ISSUER="redclaw-gateway"
GATEWAY_JWT_AUDIENCE="redclaw-api"

# Orchestrator (编排服务)
ORCHESTRATOR_JWT_SIGNING_KEY="your-orchestrator-secret-at-least-32-bytes-long"
ORCHESTRATOR_JWT_ISSUER="redclaw-orchestrator"
ORCHESTRATOR_JWT_AUDIENCE="redclaw-api"

# Admin Console (管理后台)
ADMIN_JWT_SIGNING_KEY="your-admin-secret-at-least-32-bytes-long"
ADMIN_JWT_ISSUER="redclaw-admin"
ADMIN_JWT_AUDIENCE="redclaw-api"

# RedClaw Facade (外观服务)
REDCLAW_FACADE_JWT_KEY="your-facade-secret-at-least-32-bytes-long"
```

##### SSO 提供商配置

```bash
# OIDC 通用配置
OIDC_ISSUER="https://accounts.google.com"  # 或 Okta/Azure AD 的 Issuer URL
OIDC_CLIENT_ID="your-client-id-from-idp"
OIDC_CLIENT_SECRET="your-client-secret-from-idp"
OIDC_REDIRECT_URL="https://your-domain.com/sso/callback/google"
OIDC_SCOPES="openid,profile,email"

# Google OAuth 示例
SSO_GOOGLE_CLIENT_ID="123456789.apps.googleusercontent.com"
SSO_GOOGLE_CLIENT_SECRET="GOCSPX-xxxxxxxxxxxxx"

# Okta 示例
SSO_OKTA_DOMAIN="your-domain.okta.com"
SSO_OKTA_CLIENT_ID="0oa1234567890abcdef"
SSO_OKTA_CLIENT_SECRET="your-okta-secret"

# Azure AD 示例
SSO_AZURE_TENANT_ID="your-tenant-id"
SSO_AZURE_CLIENT_ID="your-client-id"
SSO_AZURE_CLIENT_SECRET="your-client-secret"
```

##### 数据库连接配置

```bash
# PostgreSQL 连接
REDCLAW_DB_HOST="localhost"
REDCLAW_DB_PORT="5432"
REDCLAW_DB_USER="platform_app"
REDCLAW_DB_PASSWORD="your-secure-password"
REDCLAW_DB_NAME="redclaw_platform"
REDCLAW_DB_SSL_MODE="require"  # 生产环境使用 require 或 verify-full

# 连接池配置
REDCLAW_DB_MAX_CONNS="25"
REDCLAW_DB_MIN_CONNS="5"
REDCLAW_DB_MAX_CONN_LIFETIME="1h"
REDCLAW_DB_MAX_CONN_IDLE_TIME="30m"
```

##### 会话配置

```bash
# 会话默认 TTL
SESSION_TTL="15m"  # 15 分钟

# 会话空闲超时
SESSION_MAX_IDLE_TIME="1h"  # 1 小时无活动自动撤销

# 是否强制要求持久化会话
REQUIRE_PERSISTED_SESSIONS="true"
```

##### 审批和安全配置

```bash
# 审批 HMAC 密钥（用于审批流程签名）
APPROVAL_HMAC_KEY="your-approval-hmac-key-at-least-32-bytes"

# 内部服务令牌（服务间认证）
DAL_INTERNAL_TOKEN="your-dal-internal-token"
GATEWAY_INTERNAL_TOKEN="your-gateway-internal-token"
```

#### 4.1.2 密钥生成示例

**生成 32 字节随机密钥**：
```bash
# 使用 OpenSSL
openssl rand -base64 32

# 使用 Python
python3 -c "import secrets; print(secrets.token_urlsafe(32))"

# 使用 Node.js
node -e "console.log(require('crypto').randomBytes(32).toString('base64'))"

# 使用 Go
go run -c 'package main; import ("crypto/rand"; "encoding/base64"; "fmt"); func main() { b := make([]byte, 32); rand.Read(b); fmt.Println(base64.StdEncoding.EncodeToString(b)) }'
```

**生成 RSA 密钥对（用于 RS256）**：
```bash
# 生成私钥
openssl genrsa -out jwt_private_key.pem 2048

# 提取公钥
openssl rsa -in jwt_private_key.pem -pubout -out jwt_public_key.pem

# 查看私钥（PEM 格式）
cat jwt_private_key.pem

# 转换为环境变量格式（单行，\n 替换为实际换行）
awk 'NF {sub(/\r/, ""); printf "%s\\n",$0;}' jwt_private_key.pem
```

#### 4.1.3 配置文件示例

**docker-compose.yml**：
```yaml
version: '3.8'

services:
  auth-agent:
    image: redclaw/auth-agent:latest
    environment:
      - AUTH_JWT_SIGNING_KEY=${AUTH_JWT_SIGNING_KEY}
      - AUTH_JWT_ISSUER=redclaw-auth
      - AUTH_JWT_AUDIENCE=redclaw-api
      - OIDC_ISSUER=${OIDC_ISSUER}
      - OIDC_CLIENT_ID=${OIDC_CLIENT_ID}
      - OIDC_CLIENT_SECRET=${OIDC_CLIENT_SECRET}
      - OIDC_REDIRECT_URL=https://your-domain.com/sso/callback/google
      - REDCLAW_DB_HOST=postgres
      - REDCLAW_DB_PORT=5432
      - REDCLAW_DB_USER=platform_app
      - REDCLAW_DB_PASSWORD=${REDCLAW_DB_PASSWORD}
      - REDCLAW_DB_NAME=redclaw_platform
      - SESSION_TTL=15m
      - REQUIRE_PERSISTED_SESSIONS=true
    ports:
      - "8092:8092"
    depends_on:
      - postgres

  gateway:
    image: redclaw/gateway:latest
    environment:
      - GATEWAY_JWT_SIGNING_KEY=${GATEWAY_JWT_SIGNING_KEY}
      - GATEWAY_JWT_ISSUER=redclaw-gateway
      - GATEWAY_JWT_AUDIENCE=redclaw-api
      - DAL_INTERNAL_TOKEN=${DAL_INTERNAL_TOKEN}
    ports:
      - "8080:8080"

  postgres:
    image: postgres:15
    environment:
      - POSTGRES_USER=platform_app
      - POSTGRES_PASSWORD=${REDCLAW_DB_PASSWORD}
      - POSTGRES_DB=redclaw_platform
    volumes:
      - postgres_data:/var/lib/postgresql/data
    ports:
      - "5432:5432"

volumes:
  postgres_data:
```

**Kubernetes ConfigMap + Secret**：
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: redclaw-auth-secrets
type: Opaque
stringData:
  jwt-signing-key: "your-auth-service-secret-at-least-32-bytes-long"
  oidc-client-secret: "your-oidc-client-secret"
  db-password: "your-database-password"

---
apiVersion: v1
kind: ConfigMap
metadata:
  name: redclaw-auth-config
data:
  jwt-issuer: "redclaw-auth"
  jwt-audience: "redclaw-api"
  oidc-issuer: "https://accounts.google.com"
  oidc-client-id: "123456789.apps.googleusercontent.com"
  oidc-redirect-url: "https://your-domain.com/sso/callback/google"
  session-ttl: "15m"
  db-host: "postgres-service"
  db-port: "5432"
  db-name: "redclaw_platform"
  db-user: "platform_app"

---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: auth-agent
spec:
  replicas: 3
  selector:
    matchLabels:
      app: auth-agent
  template:
    metadata:
      labels:
        app: auth-agent
    spec:
      containers:
      - name: auth-agent
        image: redclaw/auth-agent:latest
        ports:
        - containerPort: 8092
        env:
        - name: AUTH_JWT_SIGNING_KEY
          valueFrom:
            secretKeyRef:
              name: redclaw-auth-secrets
              key: jwt-signing-key
        - name: OIDC_CLIENT_SECRET
          valueFrom:
            secretKeyRef:
              name: redclaw-auth-secrets
              key: oidc-client-secret
        - name: REDCLAW_DB_PASSWORD
          valueFrom:
            secretKeyRef:
              name: redclaw-auth-secrets
              key: db-password
        - name: AUTH_JWT_ISSUER
          valueFrom:
            configMapKeyRef:
              name: redclaw-auth-config
              key: jwt-issuer
        - name: AUTH_JWT_AUDIENCE
          valueFrom:
            configMapKeyRef:
              name: redclaw-auth-config
              key: jwt-audience
        - name: OIDC_ISSUER
          valueFrom:
            configMapKeyRef:
              name: redclaw-auth-config
              key: oidc-issuer
        # ... 其他配置
```

### 4.2 服务集成步骤

#### 4.2.1 引入 JWT 中间件

**步骤 1：导入依赖包**

```go
import (
    "github.com/gin-gonic/gin"
    "codeup.aliyun.com/kaixuan/FreshLab/RedClaw/services/platform-go/internal/platform/middleware"
)
```

**步骤 2：配置 JWT 中间件**

```go
func main() {
    // 从环境变量读取配置
    jwtSigningKey := os.Getenv("SERVICE_JWT_SIGNING_KEY")
    if len(jwtSigningKey) < 32 {
        log.Fatal("JWT_SIGNING_KEY must be at least 32 bytes")
    }
    
    jwtConfig := middleware.JWTConfig{
        SigningKey: []byte(jwtSigningKey),
        Issuer:     os.Getenv("SERVICE_JWT_ISSUER"),     // "redclaw-service"
        Audience:   os.Getenv("SERVICE_JWT_AUDIENCE"),   // "redclaw-api"
        Leeway:     30 * time.Second,                    // 允许 30 秒时钟偏移
    }
    
    // 创建 Gin 路由
    router := gin.Default()
    
    // 应用 JWT 中间件到所有路由
    router.Use(middleware.JWTMiddleware(jwtConfig))
    
    // 注册业务路由
    router.GET("/api/v1/users", ListUsersHandler)
    router.POST("/api/v1/users", CreateUserHandler)
    
    // 启动服务
    router.Run(":8080")
}
```

**步骤 3：在业务处理程序中获取身份信息**

```go
func ListUsersHandler(c *gin.Context) {
    // 获取 JWT Claims
    claims, ok := middleware.GetClaims(c)
    if !ok {
        c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
        return
    }
    
    // 提取租户 ID 和用户 ID
    tenantID := claims.TenantID
    currentUserID := claims.Subject
    
    // 使用租户 ID 过滤数据
    users, err := db.ListUsers(c.Request.Context(), tenantID)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "database error"})
        return
    }
    
    c.JSON(http.StatusOK, gin.H{
        "users": users,
        "meta": gin.H{
            "tenant_id": tenantID,
            "current_user_id": currentUserID,
        },
    })
}
```

#### 4.2.2 配置服务特定的 JWT 密钥

**多服务场景**：

每个服务应该有独立的签名密钥，但需要能够验证其他服务签发的 Token。有两种架构模式：

##### 模式 1：共享密钥（简单部署）

所有服务共享同一个 JWT 签名密钥（HS256）：

```bash
# 所有服务使用相同的密钥
export SHARED_JWT_SIGNING_KEY="same-secret-key-for-all-services"
```

**优点**：配置简单，服务间互信自动
**缺点**：任一服务密钥泄露影响全局

##### 模式 2：多发行者信任（推荐生产环境）

每个服务有独立密钥，服务间通过公钥或密钥交换实现互信：

```bash
# Auth Agent 签发密钥
export AUTH_JWT_SIGNING_KEY="auth-service-private-key"

# Gateway 签发密钥
export GATEWAY_JWT_SIGNING_KEY="gateway-service-private-key"

# Gateway 需要验证 Auth 签发的 Token
export AUTH_JWT_PUBLIC_KEY="auth-service-public-key-or-shared-secret"
```

**实现示例**：
```go
// Gateway 配置多发行者验证
func setupJWTMiddleware() gin.HandlerFunc {
    // Gateway 自己的签发配置
    gatewayCfg := middleware.JWTConfig{
        SigningKey: []byte(os.Getenv("GATEWAY_JWT_SIGNING_KEY")),
        Issuer:     "redclaw-gateway",
        Audience:   "redclaw-api",
    }
    
    // 支持验证 Auth Agent 签发的 Token
    authPublicKey := os.Getenv("AUTH_JWT_PUBLIC_KEY")
    
    return func(c *gin.Context) {
        tokenStr := extractBearerToken(c)
        if tokenStr == "" {
            c.AbortWithStatusJSON(401, gin.H{"error": "missing token"})
            return
        }
        
        // 先尝试用自己的密钥验证
        claims, err := verifyToken(tokenStr, gatewayCfg)
        if err != nil {
            // 再尝试用 Auth 的公钥验证
            authCfg := middleware.JWTConfig{
                SigningKey: []byte(authPublicKey),
                Issuer:     "redclaw-auth",
                Audience:   "redclaw-api",
            }
            claims, err = verifyToken(tokenStr, authCfg)
            if err != nil {
                c.AbortWithStatusJSON(401, gin.H{"error": "invalid token"})
                return
            }
        }
        
        c.Set("claims", claims)
        c.Next()
    }
}
```

#### 4.2.3 设置认证排除路径

某些端点应该跳过认证（健康检查、监控指标）：

**方法 1：中间件内置排除（推荐）**

JWT 中间件已内置排除以下路径：
- `/healthz`：存活探针
- `/readyz`：就绪探针
- `/metrics`：Prometheus 指标

**方法 2：路由分组**

```go
router := gin.Default()

// 公开路由（无需认证）
public := router.Group("/public")
{
    public.GET("/health", HealthCheckHandler)
    public.GET("/version", VersionHandler)
}

// 受保护路由（需要认证）
api := router.Group("/api")
api.Use(middleware.JWTMiddleware(jwtConfig))
{
    api.GET("/v1/users", ListUsersHandler)
    api.POST("/v1/users", CreateUserHandler)
}

// 管理员路由（需要认证 + 角色验证）
admin := router.Group("/admin")
admin.Use(middleware.JWTMiddleware(jwtConfig))
admin.Use(middleware.RequireRole("admin", "manager"))
{
    admin.GET("/users", AdminListUsersHandler)
    admin.DELETE("/users/:id", AdminDeleteUserHandler)
}
```

#### 4.2.4 集成会话验证器

启用会话有效性检查（可选，但推荐生产环境）：

```go
import (
    "codeup.aliyun.com/kaixuan/FreshLab/RedClaw/services/platform-go/internal/orchestrator/session"
)

func main() {
    // 连接 PostgreSQL
    dbPool, err := pgxpool.New(context.Background(), os.Getenv("DATABASE_URL"))
    if err != nil {
        log.Fatal("Failed to connect to database:", err)
    }
    defer dbPool.Close()
    
    // 创建 Session Store
    sessionStore := session.MustNewSQLStore(dbPool)
    
    // 配置 JWT 中间件，启用会话验证
    jwtConfig := middleware.JWTConfig{
        SigningKey:       []byte(os.Getenv("JWT_SIGNING_KEY")),
        Issuer:           "redclaw-service",
        Audience:         "redclaw-api",
        SessionValidator: sessionStore, // 启用会话验证
    }
    
    router := gin.Default()
    router.Use(middleware.JWTMiddleware(jwtConfig))
    
    // ... 注册路由
}
```

**会话验证器工作原理**：
1. JWT 中间件提取 `session_id` Claim
2. 调用 `SessionValidator.Validate(ctx, sessionID, tenantID, subject)`
3. 查询数据库验证会话未被撤销且未过期
4. 如果会话无效，返回 401 Unauthorized

**性能优化**：
- 在 Session Store 前加 Redis 缓存层
- 缓存会话状态 1-5 分钟
- 撤销事件通过 Pub/Sub 实时失效缓存

### 4.3 代码示例

#### 4.3.1 Go 代码：添加 JWT 中间件

**完整示例**：
```go
package main

import (
    "context"
    "log"
    "net/http"
    "os"
    "time"

    "github.com/gin-gonic/gin"
    "github.com/jackc/pgx/v5/pgxpool"
    
    "codeup.aliyun.com/kaixuan/FreshLab/RedClaw/services/platform-go/internal/orchestrator/session"
    "codeup.aliyun.com/kaixuan/FreshLab/RedClaw/services/platform-go/internal/platform/middleware"
)

func main() {
    // 1. 读取配置
    jwtSigningKey := os.Getenv("SERVICE_JWT_SIGNING_KEY")
    if len(jwtSigningKey) < 32 {
        log.Fatal("JWT_SIGNING_KEY must be at least 32 bytes")
    }
    
    dbURL := os.Getenv("DATABASE_URL")
    if dbURL == "" {
        log.Fatal("DATABASE_URL is required")
    }
    
    // 2. 连接数据库
    dbPool, err := pgxpool.New(context.Background(), dbURL)
    if err != nil {
        log.Fatalf("Failed to connect to database: %v", err)
    }
    defer dbPool.Close()
    
    // 3. 创建 Session Store
    sessionStore := session.MustNewSQLStore(dbPool)
    
    // 4. 配置 JWT 中间件
    jwtConfig := middleware.JWTConfig{
        SigningKey:       []byte(jwtSigningKey),
        Issuer:           os.Getenv("SERVICE_JWT_ISSUER"),
        Audience:         os.Getenv("SERVICE_JWT_AUDIENCE"),
        Leeway:           30 * time.Second,
        SessionValidator: sessionStore, // 可选：启用会话验证
    }
    
    // 5. 创建 Gin 路由
    router := gin.Default()
    
    // 公开端点（无需认证）
    router.GET("/healthz", func(c *gin.Context) {
        c.JSON(200, gin.H{"status": "ok"})
    })
    
    // 受保护的 API 路由
    api := router.Group("/api/v1")
    api.Use(middleware.JWTMiddleware(jwtConfig))
    {
        api.GET("/users", ListUsersHandler(sessionStore))
        api.GET("/users/:id", GetUserHandler(sessionStore))
        api.POST("/users", CreateUserHandler(sessionStore))
        api.DELETE("/users/:id", DeleteUserHandler(sessionStore))
    }
    
    // 管理员专用路由
    admin := router.Group("/admin")
    admin.Use(middleware.JWTMiddleware(jwtConfig))
    admin.Use(middleware.RequireRole("admin"))
    {
        admin.GET("/sessions", ListAllSessionsHandler(sessionStore))
        admin.POST("/sessions/:id/revoke", RevokeSessionHandler(sessionStore))
    }
    
    // 6. 启动服务
    port := os.Getenv("PORT")
    if port == "" {
        port = "8080"
    }
    log.Printf("Starting server on port %s", port)
    if err := router.Run(":" + port); err != nil {
        log.Fatalf("Failed to start server: %v", err)
    }
}

// ListUsersHandler 列出租户下的用户
func ListUsersHandler(store *session.SQLStore) gin.HandlerFunc {
    return func(c *gin.Context) {
        // 从中间件获取 Claims
        claims, ok := middleware.GetClaims(c)
        if !ok {
            c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
            return
        }
        
        // 使用租户 ID 过滤数据
        tenantID := claims.TenantID
        
        // 模拟数据库查询
        users := []map[string]any{
            {"id": "user1", "email": "user1@example.com", "tenant_id": tenantID},
            {"id": "user2", "email": "user2@example.com", "tenant_id": tenantID},
        }
        
        c.JSON(http.StatusOK, gin.H{
            "users": users,
            "meta": gin.H{
                "tenant_id":       tenantID,
                "current_user_id": claims.Subject,
                "timestamp":       time.Now().Unix(),
            },
        })
    }
}

// RevokeSessionHandler 管理员撤销会话
func RevokeSessionHandler(store *session.SQLStore) gin.HandlerFunc {
    return func(c *gin.Context) {
        // 获取管理员身份
        adminClaims, _ := middleware.GetClaims(c)
        
        // 获取参数
        sessionID := c.Param("id")
        tenantID := c.Query("tenant_id")
        
        if sessionID == "" || tenantID == "" {
            c.JSON(http.StatusBadRequest, gin.H{"error": "session_id and tenant_id required"})
            return
        }
        
        // 撤销会话
        revokedSession, err := store.Revoke(
            c.Request.Context(),
            sessionID,
            tenantID,
            "admin:"+adminClaims.Subject,
            "admin_forced_logout",
        )
        
        if err != nil {
            c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to revoke session"})
            return
        }
        
        c.JSON(http.StatusOK, gin.H{
            "message": "session revoked successfully",
            "session": gin.H{
                "id":         revokedSession.ID,
                "tenant_id":  revokedSession.TenantID,
                "subject_id": revokedSession.SubjectID,
                "revoked_at": revokedSession.RevokedAt.Unix(),
                "revoked_by": revokedSession.RevokedBy,
            },
        })
    }
}
```

#### 4.3.2 Go 代码：提取用户身份

```go
// 基础用法：获取 Claims
func MyHandler(c *gin.Context) {
    claims, ok := middleware.GetClaims(c)
    if !ok {
        c.JSON(401, gin.H{"error": "unauthorized"})
        return
    }
    
    log.Printf("User %s from tenant %s accessed the resource", 
        claims.Subject, claims.TenantID)
    
    // 业务逻辑...
}

// 高级用法：从 Context 直接获取特定字段
func MyHandler2(c *gin.Context) {
    tenantID := c.GetString("tenant_id")
    subject := c.GetString("subject")
    
    if tenantID == "" || subject == "" {
        c.JSON(401, gin.H{"error": "missing identity"})
        return
    }
    
    // 业务逻辑...
}

// 角色检查
func MyHandler3(c *gin.Context) {
    claims, _ := middleware.GetClaims(c)
    
    // 检查是否有特定角色
    if !claims.HasRole("billing:read", "billing:write") {
        c.JSON(403, gin.H{"error": "insufficient permissions"})
        return
    }
    
    // 执行计费相关操作...
}
```

#### 4.3.3 Go 代码：实现 RBAC 检查

```go
// 定义权限常量
const (
    RoleAdmin       = "admin"
    RoleManager     = "manager"
    RoleUser        = "user"
    
    PermBillingRead  = "billing:read"
    PermBillingWrite = "billing:write"
    PermUsersRead    = "users:read"
    PermUsersWrite   = "users:write"
)

// RequirePermission 自定义中间件：检查细粒度权限
func RequirePermission(permissions ...string) gin.HandlerFunc {
    return func(c *gin.Context) {
        claims, ok := middleware.GetClaims(c)
        if !ok {
            c.AbortWithStatusJSON(401, gin.H{"error": "unauthorized"})
            return
        }
        
        // 检查是否有任一权限
        hasPermission := false
        for _, perm := range permissions {
            if claims.HasRole(perm) {
                hasPermission = true
                break
            }
        }
        
        if !hasPermission {
            c.AbortWithStatusJSON(403, gin.H{
                "error": "forbidden",
                "required_permissions": permissions,
                "user_roles": claims.Roles,
            })
            return
        }
        
        c.Next()
    }
}

// 使用示例
func setupRoutes(router *gin.Engine, jwtCfg middleware.JWTConfig) {
    api := router.Group("/api/v1")
    api.Use(middleware.JWTMiddleware(jwtCfg))
    
    // 计费路由：需要计费权限
    billing := api.Group("/billing")
    {
        billing.GET("/invoices", 
            RequirePermission(PermBillingRead, RoleAdmin), 
            ListInvoicesHandler)
        
        billing.POST("/invoices", 
            RequirePermission(PermBillingWrite, RoleAdmin), 
            CreateInvoiceHandler)
    }
    
    // 用户管理路由：需要用户管理权限
    users := api.Group("/users")
    {
        users.GET("", 
            RequirePermission(PermUsersRead, RoleManager, RoleAdmin), 
            ListUsersHandler)
        
        users.POST("", 
            RequirePermission(PermUsersWrite, RoleAdmin), 
            CreateUserHandler)
        
        users.DELETE("/:id", 
            RequirePermission(RoleAdmin), // 仅管理员可删除
            DeleteUserHandler)
    }
}
```

#### 4.3.4 Go 代码：生成服务 Token

```go
// 服务间调用：生成短期 Token
func GenerateServiceToken(targetService string) (string, error) {
    cfg := middleware.JWTConfig{
        SigningKey: []byte(os.Getenv("SERVICE_JWT_SIGNING_KEY")),
        Issuer:     "redclaw-gateway",
        Audience:   "redclaw-api",
    }
    
    claims := middleware.Claims{
        Subject:    "service:gateway",
        TenantID:   "system", // 系统级租户
        Roles:      []string{"internal_service"},
        AuthMethod: "service_token",
    }
    
    // 服务间 Token 通常有效期较长（1 小时）
    token, err := middleware.IssueToken(cfg, claims, 1*time.Hour)
    if err != nil {
        return "", fmt.Errorf("failed to issue token: %w", err)
    }
    
    return token, nil
}

// 调用下游服务
func CallDownstreamService(ctx context.Context, endpoint string) (*http.Response, error) {
    token, err := GenerateServiceToken("downstream-service")
    if err != nil {
        return nil, err
    }
    
    req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
    if err != nil {
        return nil, err
    }
    
    req.Header.Set("Authorization", "Bearer "+token)
    req.Header.Set("Content-Type", "application/json")
    
    return http.DefaultClient.Do(req)
}
```

### 4.4 客户端集成

#### 4.4.1 Authorization 头格式

标准 Bearer Token 格式：
```
Authorization: Bearer <JWT_TOKEN>
```

**示例**：
```http
GET /api/v1/users HTTP/1.1
Host: api.redclaw.example.com
Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ1c2VyMTIzIiwidGVuYW50X2lkIjoidGVuYW50NDU2Iiwicm9sZXMiOlsidXNlciJdLCJleHAiOjE2OTQ1MjAwMDB9.signature
Content-Type: application/json
```

#### 4.4.2 Token 存储最佳实践

##### Web 浏览器

**推荐：HttpOnly Cookie（最安全）**
```javascript
// 服务端设置 Cookie
res.cookie('jwt_token', token, {
    httpOnly: true,      // 防止 JavaScript 访问（XSS 防护）
    secure: true,        // 仅 HTTPS 传输
    sameSite: 'strict',  // CSRF 防护
    maxAge: 900000       // 15 分钟（毫秒）
});

// 浏览器自动在请求中携带 Cookie
fetch('/api/v1/users', {
    method: 'GET',
    credentials: 'include' // 发送 Cookie
});
```

**备选：localStorage（灵活但需注意 XSS）**
```javascript
// 存储 Token
localStorage.setItem('jwt_token', token);

// 发送请求
const token = localStorage.getItem('jwt_token');
fetch('/api/v1/users', {
    method: 'GET',
    headers: {
        'Authorization': `Bearer ${token}`
    }
});
```

**不推荐：sessionStorage**（刷新页面会丢失）

##### 移动应用

**iOS/Android：使用系统 Keychain/Keystore**

iOS（Swift）：
```swift
import Security

func saveToken(_ token: String) {
    let data = token.data(using: .utf8)!
    let query: [String: Any] = [
        kSecClass as String: kSecClassGenericPassword,
        kSecAttrAccount as String: "jwt_token",
        kSecValueData as String: data,
        kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlock
    ]
    
    SecItemDelete(query as CFDictionary)
    SecItemAdd(query as CFDictionary, nil)
}

func getToken() -> String? {
    let query: [String: Any] = [
        kSecClass as String: kSecClassGenericPassword,
        kSecAttrAccount as String: "jwt_token",
        kSecReturnData as String: true
    ]
    
    var result: AnyObject?
    let status = SecItemCopyMatching(query as CFDictionary, &result)
    
    guard status == errSecSuccess,
          let data = result as? Data,
          let token = String(data: data, encoding: .utf8) else {
        return nil
    }
    
    return token
}
```

Android（Kotlin）：
```kotlin
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey

class TokenManager(context: Context) {
    private val masterKey = MasterKey.Builder(context)
        .setKeyScheme(MasterKey.KeyScheme.AES256_GCM)
        .build()
    
    private val sharedPreferences = EncryptedSharedPreferences.create(
        context,
        "redclaw_tokens",
        masterKey,
        EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
        EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM
    )
    
    fun saveToken(token: String) {
        sharedPreferences.edit()
            .putString("jwt_token", token)
            .apply()
    }
    
    fun getToken(): String? {
        return sharedPreferences.getString("jwt_token", null)
    }
    
    fun clearToken() {
        sharedPreferences.edit()
            .remove("jwt_token")
            .apply()
    }
}
```

#### 4.4.3 Token 刷新策略

**主动刷新（推荐）**：
```javascript
class AuthClient {
    constructor() {
        this.token = null;
        this.refreshThreshold = 5 * 60 * 1000; // 过期前 5 分钟刷新
        this.refreshTimer = null;
    }
    
    setToken(token) {
        this.token = token;
        this.scheduleRefresh();
    }
    
    scheduleRefresh() {
        if (this.refreshTimer) {
            clearTimeout(this.refreshTimer);
        }
        
        const payload = this.parseJWT(this.token);
        if (!payload || !payload.exp) return;
        
        const expiresAt = payload.exp * 1000; // 转换为毫秒
        const now = Date.now();
        const timeUntilRefresh = expiresAt - now - this.refreshThreshold;
        
        if (timeUntilRefresh > 0) {
            this.refreshTimer = setTimeout(() => {
                this.refreshToken();
            }, timeUntilRefresh);
        }
    }
    
    async refreshToken() {
        try {
            const response = await fetch('/auth/refresh', {
                method: 'POST',
                headers: {
                    'Authorization': `Bearer ${this.token}`
                }
            });
            
            if (response.ok) {
                const data = await response.json();
                this.setToken(data.token);
                console.log('Token refreshed successfully');
            } else if (response.status === 401) {
                // 会话过期，重定向到登录页
                window.location.href = '/login';
            }
        } catch (error) {
            console.error('Token refresh failed:', error);
        }
    }
    
    parseJWT(token) {
        try {
            const base64Url = token.split('.')[1];
            const base64 = base64Url.replace(/-/g, '+').replace(/_/g, '/');
            const jsonPayload = decodeURIComponent(
                atob(base64).split('').map(c => 
                    '%' + ('00' + c.charCodeAt(0).toString(16)).slice(-2)
                ).join('')
            );
            return JSON.parse(jsonPayload);
        } catch (e) {
            return null;
        }
    }
}

// 使用示例
const authClient = new AuthClient();
authClient.setToken(initialToken);
```

**被动刷新（401 响应触发）**：
```javascript
async function fetchWithAuth(url, options = {}) {
    // 第一次尝试
    let response = await fetch(url, {
        ...options,
        headers: {
            ...options.headers,
            'Authorization': `Bearer ${getToken()}`
        }
    });
    
    // 如果 401，尝试刷新 Token
    if (response.status === 401) {
        const refreshed = await refreshToken();
        if (!refreshed) {
            // 刷新失败，重定向到登录页
            window.location.href = '/login';
            return;
        }
        
        // 用新 Token 重试
        response = await fetch(url, {
            ...options,
            headers: {
                ...options.headers,
                'Authorization': `Bearer ${getToken()}`
            }
        });
    }
    
    return response;
}
```

#### 4.4.4 错误处理

**标准错误响应**：
```json
{
    "error": "unauthorized",
    "message": "token expired",
    "code": "TOKEN_EXPIRED",
    "timestamp": 1694520000
}
```

**客户端错误处理逻辑**：
```javascript
async function handleAPIResponse(response) {
    if (response.ok) {
        return await response.json();
    }
    
    const error = await response.json();
    
    switch (response.status) {
        case 401:
            // 未认证：Token 无效或过期
            if (error.code === 'TOKEN_EXPIRED') {
                // 尝试刷新
                const refreshed = await refreshToken();
                if (refreshed) {
                    // 刷新成功，重试原请求
                    return retryRequest();
                }
            }
            // 刷新失败或其他认证错误，跳转登录
            redirectToLogin();
            break;
            
        case 403:
            // 已认证但权限不足
            showError('您没有权限执行此操作');
            break;
            
        case 404:
            showError('资源不存在');
            break;
            
        case 500:
            showError('服务器错误，请稍后重试');
            break;
            
        default:
            showError('未知错误：' + error.message);
    }
    
    throw new Error(error.message);
}
```

---

## 第五部分：数据模型

### 5.1 User 模型

用户表存储租户内的用户基本信息：

```sql
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    email VARCHAR(255) NOT NULL,
    email_verified BOOLEAN DEFAULT FALSE,
    display_name VARCHAR(255),
    avatar_url TEXT,
    status VARCHAR(50) DEFAULT 'active', -- active, disabled, suspended
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    last_login_at TIMESTAMP WITH TIME ZONE,
    metadata JSONB DEFAULT '{}',
    
    CONSTRAINT users_tenant_email_unique UNIQUE (tenant_id, email)
);

CREATE INDEX idx_users_tenant_id ON users(tenant_id);
CREATE INDEX idx_users_email ON users(email);
CREATE INDEX idx_users_status ON users(status);
```

**字段说明**：
- `id`：用户唯一标识（UUID）
- `tenant_id`：所属租户 ID，外键约束确保租户存在
- `email`：用户邮箱，在租户内唯一
- `status`：用户状态，影响登录权限
- `metadata`：扩展字段，存储自定义属性（部门、职位等）

**Go 结构体**：
```go
type User struct {
    ID            string                 `json:"id"`
    TenantID      string                 `json:"tenant_id"`
    Email         string                 `json:"email"`
    EmailVerified bool                   `json:"email_verified"`
    DisplayName   string                 `json:"display_name,omitempty"`
    AvatarURL     string                 `json:"avatar_url,omitempty"`
    Status        string                 `json:"status"`
    CreatedAt     time.Time              `json:"created_at"`
    UpdatedAt     time.Time              `json:"updated_at"`
    LastLoginAt   *time.Time             `json:"last_login_at,omitempty"`
    Metadata      map[string]interface{} `json:"metadata,omitempty"`
}
```

### 5.2 Tenant 模型

租户表存储多租户隔离的根实体：

```sql
CREATE TABLE tenants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    slug VARCHAR(100) NOT NULL UNIQUE, -- URL 友好的标识符
    status VARCHAR(50) DEFAULT 'active', -- active, trial, suspended, deleted
    subscription_plan VARCHAR(100) DEFAULT 'free',
    max_users INTEGER DEFAULT 10,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    metadata JSONB DEFAULT '{}'
);

CREATE INDEX idx_tenants_slug ON tenants(slug);
CREATE INDEX idx_tenants_status ON tenants(status);
```

**Go 结构体**：
```go
type Tenant struct {
    ID               string                 `json:"id"`
    Name             string                 `json:"name"`
    Slug             string                 `json:"slug"`
    Status           string                 `json:"status"`
    SubscriptionPlan string                 `json:"subscription_plan"`
    MaxUsers         int                    `json:"max_users"`
    CreatedAt        time.Time              `json:"created_at"`
    UpdatedAt        time.Time              `json:"updated_at"`
    Metadata         map[string]interface{} `json:"metadata,omitempty"`
}
```

### 5.3 Session 模型

会话表存储持久化会话记录：

```sql
CREATE TABLE authagent.sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    subject_id UUID NOT NULL, -- 用户 ID
    token_hash BYTEA NOT NULL, -- SHA-256 哈希，不存储明文 Token
    issuer VARCHAR(100) NOT NULL, -- "redclaw.sso", "api_key", etc.
    
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    last_seen_at TIMESTAMP WITH TIME ZONE,
    
    revoked_at TIMESTAMP WITH TIME ZONE,
    revoked_by VARCHAR(255), -- "self", "admin:{user_id}"
    revoke_reason VARCHAR(255),
    
    metadata JSONB DEFAULT '{}',
    
    CONSTRAINT sessions_token_hash_unique UNIQUE (token_hash)
);

CREATE INDEX idx_sessions_tenant_subject ON authagent.sessions(tenant_id, subject_id);
CREATE INDEX idx_sessions_expires_at ON authagent.sessions(expires_at) WHERE revoked_at IS NULL;
CREATE INDEX idx_sessions_revoked_at ON authagent.sessions(revoked_at) WHERE revoked_at IS NOT NULL;
```

**字段说明**：
- `token_hash`：Token 的 SHA-256 哈希，防止数据库泄露导致凭证泄露
- `issuer`：会话发行者，用于审计追踪
- `last_seen_at`：最后访问时间，用于检测异常活动
- `revoked_at`：撤销时间，非空表示会话已失效
- `metadata`：存储 User-Agent、IP 地址等上下文信息

**Go 结构体**：
```go
type Session struct {
    ID             string                 `json:"id"`
    Token          Token                  `json:"-"` // 明文 Token，不序列化
    TokenHash      []byte                 `json:"-"` // 哈希，不序列化
    TenantID       string                 `json:"tenant_id"`
    SubjectID      string                 `json:"subject_id"`
    Issuer         string                 `json:"issuer"`
    CreatedAt      time.Time              `json:"created_at"`
    ExpiresAt      time.Time              `json:"expires_at"`
    LastSeenAt     time.Time              `json:"last_seen_at,omitempty"`
    RevokedAt      time.Time              `json:"revoked_at,omitempty"`
    RevokedBy      string                 `json:"revoked_by,omitempty"`
    RevokeReason   string                 `json:"revoke_reason,omitempty"`
    MetadataIssuer string                 `json:"metadata_issuer"`
    Metadata       map[string]interface{} `json:"metadata,omitempty"`
}

// Revoked 判断会话是否已撤销
func (s *Session) Revoked() bool {
    return !s.RevokedAt.IsZero()
}

// Expired 判断会话是否已过期
func (s *Session) Expired(now time.Time) bool {
    if now.IsZero() {
        now = time.Now().UTC()
    }
    return !s.ExpiresAt.IsZero() && !s.ExpiresAt.After(now)
}
```

### 5.4 SSO Configuration 模型

租户 SSO 配置表：

```sql
CREATE TABLE tenant_sso_configs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    provider VARCHAR(50) NOT NULL, -- "google", "okta", "azure", etc.
    
    client_id VARCHAR(255) NOT NULL,
    client_secret TEXT NOT NULL, -- 生产环境应加密存储
    
    issuer VARCHAR(500) NOT NULL,
    redirect_url VARCHAR(500) NOT NULL,
    scopes TEXT[] DEFAULT ARRAY['openid', 'profile', 'email'],
    
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    metadata JSONB DEFAULT '{}', -- 提供商特定配置
    
    CONSTRAINT tenant_sso_configs_tenant_provider_unique UNIQUE (tenant_id, provider)
);

CREATE INDEX idx_tenant_sso_configs_tenant_id ON tenant_sso_configs(tenant_id);
CREATE INDEX idx_tenant_sso_configs_is_active ON tenant_sso_configs(is_active);
```

**Go 结构体**：
```go
type SSOConfig struct {
    ID           string                 `json:"id"`
    TenantID     string                 `json:"tenant_id"`
    Provider     string                 `json:"provider"`
    ClientID     string                 `json:"client_id"`
    ClientSecret string                 `json:"-"` // 敏感信息，不序列化
    Issuer       string                 `json:"issuer"`
    RedirectURL  string                 `json:"redirect_url"`
    Scopes       []string               `json:"scopes"`
    IsActive     bool                   `json:"is_active"`
    CreatedAt    time.Time              `json:"created_at"`
    UpdatedAt    time.Time              `json:"updated_at"`
    Metadata     map[string]interface{} `json:"metadata,omitempty"`
}
```

### 5.5 Token Claims 结构

JWT Token 的 Claims 结构：

```go
type Claims struct {
    // 标准 Claims (RFC 7519)
    Issuer    string   `json:"iss"`       // Token 发行者
    Subject   string   `json:"sub"`       // 用户 ID
    Audience  []string `json:"aud"`       // Token 接收方
    ExpiresAt int64    `json:"exp"`       // 过期时间（Unix 时间戳）
    IssuedAt  int64    `json:"iat"`       // 签发时间
    NotBefore int64    `json:"nbf"`       // 生效时间
    
    // RedClaw 扩展 Claims
    Email      string   `json:"email,omitempty"`       // 用户邮箱
    TenantID   string   `json:"tenant_id"`             // 租户 ID（必需）
    SessionID  string   `json:"session_id,omitempty"`  // 会话 ID（用于撤销）
    Roles      []string `json:"roles,omitempty"`       // 角色列表
    AuthMethod string   `json:"auth_method,omitempty"` // 认证方式
}
```

**Claims 示例（JSON）**：
```json
{
  "iss": "redclaw-auth",
  "sub": "550e8400-e29b-41d4-a716-446655440000",
  "aud": ["redclaw-api"],
  "exp": 1694520900,
  "iat": 1694520000,
  "nbf": 1694519970,
  "email": "user@example.com",
  "tenant_id": "123e4567-e89b-12d3-a456-426614174000",
  "session_id": "789e0123-e45b-67c8-d901-234567890abc",
  "roles": ["user", "billing:read"],
  "auth_method": "sso"
}
```

### 5.6 数据库迁移示例

**迁移 000039：创建 sessions 表**
```sql
-- 000039_authagent_sessions.up.sql
CREATE SCHEMA IF NOT EXISTS authagent;

CREATE TABLE authagent.sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    subject_id UUID NOT NULL,
    token_hash BYTEA NOT NULL,
    issuer VARCHAR(100) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    metadata JSONB DEFAULT '{}'
);

CREATE UNIQUE INDEX sessions_token_hash_unique ON authagent.sessions(token_hash);
CREATE INDEX idx_sessions_tenant_subject ON authagent.sessions(tenant_id, subject_id);
CREATE INDEX idx_sessions_expires_at ON authagent.sessions(expires_at);
```

**迁移 000041：添加撤销和最后访问字段**
```sql
-- 000041_authagent_sessions_g32_columns.up.sql
ALTER TABLE authagent.sessions
    ADD COLUMN last_seen_at TIMESTAMP WITH TIME ZONE,
    ADD COLUMN revoked_at TIMESTAMP WITH TIME ZONE,
    ADD COLUMN revoked_by VARCHAR(255),
    ADD COLUMN revoke_reason VARCHAR(255);

CREATE INDEX idx_sessions_revoked_at ON authagent.sessions(revoked_at) WHERE revoked_at IS NOT NULL;
```

---

## 第六部分：安全最佳实践

### 6.1 密钥管理

#### 6.1.1 生产环境密钥要求

**强制要求**：
- JWT 签名密钥**至少 32 字节**（256 位）
- 使用加密安全的随机数生成器（`crypto/rand`）
- 不同服务使用不同的签名密钥
- 定期轮换密钥（建议每 90 天）

**密钥强度验证**：
```go
func ValidateJWTKey(key []byte) error {
    if len(key) < 32 {
        return errors.New("JWT key must be at least 32 bytes")
    }
    
    // 检查是否为明显的弱密钥
    weak := []string{"change-me", "password", "secret", "12345"}
    keyStr := string(key)
    for _, w := range weak {
        if strings.Contains(strings.ToLower(keyStr), w) {
            return errors.New("JWT key appears to be weak or default")
        }
    }
    
    return nil
}
```

#### 6.1.2 密钥轮换策略

**轮换流程**：
1. 生成新密钥 `KEY_NEW`
2. 配置服务同时支持 `KEY_OLD` 和 `KEY_NEW` 验证
3. 新签发的 Token 使用 `KEY_NEW`
4. 等待 `KEY_OLD` 签发的所有 Token 过期（通常 15-60 分钟）
5. 移除 `KEY_OLD`，`KEY_NEW` 成为唯一密钥

**双密钥验证实现**：
```go
type JWTConfig struct {
    SigningKey    []byte   // 当前签名密钥
    VerifyKeys    [][]byte // 历史验证密钥（轮换期间）
    Issuer        string
    Audience      string
}

func (cfg JWTConfig) VerifyToken(tokenStr string) (*Claims, error) {
    var lastErr error
    
    // 尝试用当前密钥验证
    claims, err := parseWithKey(tokenStr, cfg.SigningKey, cfg)
    if err == nil {
        return claims, nil
    }
    lastErr = err
    
    // 尝试用历史密钥验证
    for _, oldKey := range cfg.VerifyKeys {
        claims, err := parseWithKey(tokenStr, oldKey, cfg)
        if err == nil {
            return claims, nil
        }
        lastErr = err
    }
    
    return nil, lastErr
}
```

#### 6.1.3 安全存储

**环境变量（推荐用于开发和测试）**：
```bash
# .env 文件（不要提交到版本控制）
JWT_SIGNING_KEY="$(openssl rand -base64 32)"
```

**密钥管理服务（推荐用于生产）**：

使用 AWS Secrets Manager：
```go
import (
    "github.com/aws/aws-sdk-go/aws/session"
    "github.com/aws/aws-sdk-go/service/secretsmanager"
)

func GetJWTKeyFromSecretsManager(secretName string) ([]byte, error) {
    sess := session.Must(session.NewSession())
    svc := secretsmanager.New(sess)
    
    result, err := svc.GetSecretValue(&secretsmanager.GetSecretValueInput{
        SecretId: aws.String(secretName),
    })
    if err != nil {
        return nil, err
    }
    
    return []byte(*result.SecretString), nil
}
```

使用 HashiCorp Vault：
```go
import "github.com/hashicorp/vault/api"

func GetJWTKeyFromVault(path string) ([]byte, error) {
    client, err := api.NewClient(api.DefaultConfig())
    if err != nil {
        return nil, err
    }
    
    secret, err := client.Logical().Read(path)
    if err != nil {
        return nil, err
    }
    
    key, ok := secret.Data["jwt_key"].(string)
    if !ok {
        return nil, errors.New("jwt_key not found in vault")
    }
    
    return []byte(key), nil
}
```

### 6.2 Token 安全

#### 6.2.1 过期时间设置建议

| Token 类型 | 推荐过期时间 | 场景 |
|-----------|------------|------|
| 用户 Session Token | 15-30 分钟 | Web 应用、移动应用 |
| 服务间 Token | 1 小时 | 微服务内部调用 |
| API Key Token | 24 小时 | 自动化脚本、CI/CD |
| 临时操作 Token | 5 分钟 | 密码重置、邮箱验证 |

**动态过期时间**：
```go
func DetermineTokenTTL(authMethod string, userRole string) time.Duration {
    switch authMethod {
    case "sso":
        return 15 * time.Minute
    case "api_key":
        return 24 * time.Hour
    case "service_token":
        return 1 * time.Hour
    case "temp_token":
        return 5 * time.Minute
    default:
        return 15 * time.Minute
    }
}
```

#### 6.2.2 HTTPS 强制要求

**生产环境必须启用 HTTPS**：
- JWT Token 通过 Authorization 头传输，HTTP 明文传输会泄露凭证
- Cookie 设置 `Secure` 标志，仅在 HTTPS 下发送

**服务端强制 HTTPS**：
```go
func ForceHTTPS() gin.HandlerFunc {
    return func(c *gin.Context) {
        if c.Request.Header.Get("X-Forwarded-Proto") != "https" && 
           !strings.HasPrefix(c.Request.Host, "localhost") {
            c.Redirect(http.StatusMovedPermanently, "https://"+c.Request.Host+c.Request.RequestURI)
            c.Abort()
            return
        }
        c.Next()
    }
}

router.Use(ForceHTTPS())
```

#### 6.2.3 XSS/CSRF 防护

**XSS 防护**：
- 使用 HttpOnly Cookie 存储 Token，JavaScript 无法访问
- 如果使用 localStorage，必须对输出进行 HTML 转义
- 实施 Content Security Policy (CSP)

```http
Content-Security-Policy: default-src 'self'; script-src 'self' 'unsafe-inline'
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
```

**CSRF 防护**：
- Cookie 设置 `SameSite=Strict` 或 `SameSite=Lax`
- 对状态变更操作（POST/PUT/DELETE）验证 CSRF Token

```go
res.cookie('jwt_token', token, {
    httpOnly: true,
    secure: true,
    sameSite: 'strict',
    maxAge: 900000 // 15 分钟
});
```

### 6.3 SSO 安全

#### 6.3.1 PKCE 使用

**强制启用 PKCE**（适用于所有客户端类型）：
```go
// 生成 code_verifier
func generateCodeVerifier() (string, error) {
    b := make([]byte, 64)
    if _, err := rand.Read(b); err != nil {
        return "", err
    }
    return base64.RawURLEncoding.EncodeToString(b), nil
}

// 计算 code_challenge
func generateCodeChallenge(verifier string) string {
    h := sha256.Sum256([]byte(verifier))
    return base64.RawURLEncoding.EncodeToString(h[:])
}

// 授权请求
codeVerifier, _ := generateCodeVerifier()
codeChallenge := generateCodeChallenge(codeVerifier)

authorizeURL := fmt.Sprintf("%s?client_id=%s&response_type=code&redirect_uri=%s&scope=%s&state=%s&code_challenge=%s&code_challenge_method=S256",
    issuer, clientID, redirectURI, scope, state, codeChallenge)

// Token 交换
tokenRequest := url.Values{
    "grant_type":    {"authorization_code"},
    "code":          {authCode},
    "redirect_uri":  {redirectURI},
    "client_id":     {clientID},
    "code_verifier": {codeVerifier}, // 提供 verifier
}
```

#### 6.3.2 State 参数验证

**State 参数防护 CSRF 攻击**：
```go
type replayGuard struct {
    mu    sync.RWMutex
    cache map[string]stateEntry
}

type stateEntry struct {
    nonce     string
    expiresAt time.Time
}

func (g *replayGuard) Store(state, nonce string) error {
    g.mu.Lock()
    defer g.mu.Unlock()
    
    g.cache[state] = stateEntry{
        nonce:     nonce,
        expiresAt: time.Now().Add(5 * time.Minute),
    }
    return nil
}

func (g *replayGuard) Consume(state string) (string, bool) {
    g.mu.Lock()
    defer g.mu.Unlock()
    
    entry, ok := g.cache[state]
    if !ok || time.Now().After(entry.expiresAt) {
        return "", false
    }
    
    delete(g.cache, state) // 消费后立即删除，防止重放
    return entry.nonce, true
}
```

#### 6.3.3 可信提供商白名单

**租户级 SSO 绑定验证**：
```go
func (h *Handler) HandleSSOCallback(c *gin.Context) {
    // ... 解析 ID Token ...
    
    claims, err := ssoManager.ParseIDToken(idToken)
    if err != nil {
        c.JSON(400, gin.H{"error": "invalid id_token"})
        return
    }
    
    // 验证租户 SSO 绑定
    ssoConfig, err := h.dal.GetTenantSSOConfig(c.Request.Context(), claims.Tenant, provider)
    if err != nil {
        c.JSON(401, gin.H{"error": "SSO provider not authorized for this tenant"})
        return
    }
    
    if !ssoConfig.IsActive {
        c.JSON(403, gin.H{"error": "SSO provider is disabled for this tenant"})
        return
    }
    
    // ... 创建会话 ...
}
```

### 6.4 审计和监控

#### 6.4.1 认证事件日志

**关键事件记录**：
- SSO 登录成功/失败
- Token 签发
- 会话撤销
- 认证失败（401）
- 权限不足（403）

**结构化日志示例**：
```go
type AuthEvent struct {
    Timestamp  time.Time `json:"timestamp"`
    EventType  string    `json:"event_type"` // "sso_login", "token_issued", "session_revoked"
    TenantID   string    `json:"tenant_id"`
    SubjectID  string    `json:"subject_id"`
    SessionID  string    `json:"session_id,omitempty"`
    IPAddress  string    `json:"ip_address"`
    UserAgent  string    `json:"user_agent"`
    Success    bool      `json:"success"`
    ErrorCode  string    `json:"error_code,omitempty"`
    Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

func LogAuthEvent(ctx context.Context, event AuthEvent) {
    event.Timestamp = time.Now()
    logger.Info().
        Str("event_type", event.EventType).
        Str("tenant_id", event.TenantID).
        Str("subject_id", event.SubjectID).
        Bool("success", event.Success).
        Msg("Authentication event")
}
```

#### 6.4.2 异常检测

**异常登录检测规则**：
- 短时间内多次失败尝试（暴力破解）
- 来自异常地理位置的登录
- 同一用户同时从多个 IP 登录
- 会话创建速率异常

**实现示例**：
```go
func DetectAnomalousLogin(ctx context.Context, userID, ipAddress string) (bool, string) {
    // 1. 检查失败尝试次数
    failedAttempts, _ := redis.Get(ctx, fmt.Sprintf("failed_login:%s", userID))
    if failedAttempts >= 5 {
        return true, "too_many_failed_attempts"
    }
    
    // 2. 检查地理位置变化
    lastIP, _ := redis.Get(ctx, fmt.Sprintf("last_login_ip:%s", userID))
    if lastIP != "" && ipAddress != lastIP {
        lastCountry := geoIP.LookupCountry(lastIP)
        currentCountry := geoIP.LookupCountry(ipAddress)
        if lastCountry != currentCountry {
            return true, "geo_location_change"
        }
    }
    
    // 3. 检查登录速率
    loginCount, _ := redis.Incr(ctx, fmt.Sprintf("login_rate:%s", userID))
    redis.Expire(ctx, fmt.Sprintf("login_rate:%s", userID), 5*time.Minute)
    if loginCount > 10 {
        return true, "high_login_rate"
    }
    
    return false, ""
}
```

---

## 第七部分：运维指南

### 7.1 数据库迁移

#### 7.1.1 认证相关表结构

**核心表**：
- `tenants`：租户表
- `users`：用户表
- `authagent.sessions`：会话表
- `tenant_sso_configs`：SSO 配置表

**迁移工具**：使用 `golang-migrate` 或类似工具

**迁移命令**：
```bash
# 执行所有待应用的迁移
migrate -path ./migrations -database "$DATABASE_URL" up

# 回滚最后一次迁移
migrate -path ./migrations -database "$DATABASE_URL" down 1

# 查看当前迁移版本
migrate -path ./migrations -database "$DATABASE_URL" version
```

#### 7.1.2 迁移文件位置

```
services/platform-go/migrations/
├── 000039_authagent_sessions.up.sql
├── 000039_authagent_sessions.down.sql
├── 000040_authagent_sessions_token_hash.up.sql
├── 000040_authagent_sessions_token_hash.down.sql
├── 000041_authagent_sessions_g32_columns.up.sql
├── 000041_authagent_sessions_g32_columns.down.sql
├── 000043_authagent_sessions_notifications.up.sql
├── 000043_authagent_sessions_notifications.down.sql
└── 000045_authagent_sessions_token_hash_repair.up.sql
```

### 7.2 监控指标

#### 7.2.1 认证成功/失败率

**Prometheus 指标**：
```go
var (
    authAttempts = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "redclaw_auth_attempts_total",
            Help: "Total number of authentication attempts",
        },
        []string{"method", "status"}, // method: sso, api_key; status: success, failure
    )
    
    tokenIssued = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "redclaw_tokens_issued_total",
            Help: "Total number of JWT tokens issued",
        },
        []string{"issuer"},
    )
    
    sessionRevocations = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "redclaw_session_revocations_total",
            Help: "Total number of session revocations",
        },
        []string{"reason"}, // reason: logout, admin_force, expired
    )
)

func init() {
    prometheus.MustRegister(authAttempts)
    prometheus.MustRegister(tokenIssued)
    prometheus.MustRegister(sessionRevocations)
}

// 使用示例
authAttempts.WithLabelValues("sso", "success").Inc()
tokenIssued.WithLabelValues("redclaw-auth").Inc()
sessionRevocations.WithLabelValues("logout").Inc()
```

**Grafana Dashboard 查询**：
```promql
# 认证成功率
rate(redclaw_auth_attempts_total{status="success"}[5m]) 
/ 
rate(redclaw_auth_attempts_total[5m])

# 失败认证速率
rate(redclaw_auth_attempts_total{status="failure"}[5m])
```

#### 7.2.2 Token 验证延迟

```go
var tokenValidationDuration = prometheus.NewHistogramVec(
    prometheus.HistogramOpts{
        Name:    "redclaw_token_validation_duration_seconds",
        Help:    "Time spent validating JWT tokens",
        Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0},
    },
    []string{"result"}, // result: valid, invalid, expired
)

// 在中间件中记录
start := time.Now()
claims, err := verifyToken(tokenStr, cfg)
duration := time.Since(start).Seconds()

if err != nil {
    tokenValidationDuration.WithLabelValues("invalid").Observe(duration)
} else if claims.ExpiresAt < time.Now().Unix() {
    tokenValidationDuration.WithLabelValues("expired").Observe(duration)
} else {
    tokenValidationDuration.WithLabelValues("valid").Observe(duration)
}
```

#### 7.2.3 会话活跃数

```go
var activeSessions = prometheus.NewGaugeFunc(
    prometheus.GaugeOpts{
        Name: "redclaw_active_sessions",
        Help: "Number of active (non-expired, non-revoked) sessions",
    },
    func() float64 {
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        
        var count int64
        err := db.QueryRow(ctx, `
            SELECT COUNT(*) 
            FROM authagent.sessions 
            WHERE expires_at > NOW() 
              AND revoked_at IS NULL
        `).Scan(&count)
        
        if err != nil {
            return 0
        }
        return float64(count)
    },
)
```

### 7.3 故障排查

#### 7.3.1 常见错误码

| 错误码 | HTTP 状态 | 说明 | 解决方案 |
|-------|----------|------|----------|
| `missing bearer token` | 401 | 请求头缺少 Authorization | 检查客户端是否正确发送 Token |
| `invalid token` | 401 | Token 签名无效或格式错误 | 验证签名密钥配置一致 |
| `token expired` | 401 | Token 已过期 | 客户端刷新 Token |
| `token missing required claims` | 401 | Claims 缺少必需字段 | 检查 Token 签发逻辑 |
| `session revoked` | 401 | 会话已被撤销 | 用户重新登录 |
| `session not found` | 401 | 会话不存在或已过期 | 用户重新登录 |
| `insufficient role` | 403 | 用户角色权限不足 | 检查用户角色配置 |
| `SSO provider not trusted` | 403 | IdP 未绑定到租户 | 配置租户 SSO 绑定 |

#### 7.3.2 调试技巧

**启用详细日志**：
```bash
export LOG_LEVEL=debug
export AUTH_DEBUG=true
```

**JWT Token 解码（仅用于调试）**：
```bash
# 解码 Token（不验证签名）
echo "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..." | cut -d. -f2 | base64 -d | jq
```

**验证 Token 签名**：
```bash
# 使用 jwt-cli 工具
jwt decode --secret "your-secret-key" "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."
```

**检查数据库会话状态**：
```sql
-- 查看用户的所有会话
SELECT id, created_at, expires_at, revoked_at, last_seen_at
FROM authagent.sessions
WHERE tenant_id = 'xxx' AND subject_id = 'yyy'
ORDER BY created_at DESC;

-- 查看即将过期的会话（未来 5 分钟）
SELECT COUNT(*)
FROM authagent.sessions
WHERE expires_at BETWEEN NOW() AND NOW() + INTERVAL '5 minutes'
  AND revoked_at IS NULL;
```

#### 7.3.3 日志查看

**结构化日志查询（使用 jq）**：
```bash
# 过滤认证失败事件
tail -f /var/log/auth-agent.log | jq 'select(.event_type == "auth_failure")'

# 查看特定租户的认证事件
grep "tenant_id=abc123" /var/log/auth-agent.log | jq

# 统计最近 1 小时的认证成功率
tail -1000 /var/log/auth-agent.log | \
  jq -r 'select(.event_type == "sso_login") | .success' | \
  awk '{success += $1; total++} END {print "Success rate:", success/total*100"%"}'
```

**ELK Stack 查询**：
```json
{
  "query": {
    "bool": {
      "must": [
        {"match": {"event_type": "auth_failure"}},
        {"range": {"timestamp": {"gte": "now-1h"}}}
      ]
    }
  },
  "aggs": {
    "by_error_code": {
      "terms": {"field": "error_code"}
    }
  }
}
```

---

## 第八部分：参考信息

### 8.1 配置参考

#### 8.1.1 完整环境变量列表

```bash
# ============================================
# JWT 配置
# ============================================
AUTH_JWT_SIGNING_KEY="<至少 32 字节>"
AUTH_JWT_ISSUER="redclaw-auth"
AUTH_JWT_AUDIENCE="redclaw-api"

GATEWAY_JWT_SIGNING_KEY="<至少 32 字节>"
GATEWAY_JWT_ISSUER="redclaw-gateway"
GATEWAY_JWT_AUDIENCE="redclaw-api"

ORCHESTRATOR_JWT_SIGNING_KEY="<至少 32 字节>"
ORCHESTRATOR_JWT_ISSUER="redclaw-orchestrator"
ORCHESTRATOR_JWT_AUDIENCE="redclaw-api"

ADMIN_JWT_SIGNING_KEY="<至少 32 字节>"
ADMIN_JWT_ISSUER="redclaw-admin"
ADMIN_JWT_AUDIENCE="redclaw-api"

# ============================================
# SSO 配置
# ============================================
OIDC_ISSUER="https://accounts.google.com"
OIDC_CLIENT_ID="your-client-id"
OIDC_CLIENT_SECRET="your-client-secret"
OIDC_REDIRECT_URL="https://your-domain.com/sso/callback/google"
OIDC_SCOPES="openid,profile,email"

# ============================================
# 数据库配置
# ============================================
REDCLAW_DB_HOST="localhost"
REDCLAW_DB_PORT="5432"
REDCLAW_DB_USER="platform_app"
REDCLAW_DB_PASSWORD="<secure-password>"
REDCLAW_DB_NAME="redclaw_platform"
REDCLAW_DB_SSL_MODE="require"
REDCLAW_DB_MAX_CONNS="25"
REDCLAW_DB_MIN_CONNS="5"

# 或使用连接字符串
DATABASE_URL="postgres://user:pass@host:5432/dbname?sslmode=require"

# ============================================
# 会话配置
# ============================================
SESSION_TTL="15m"
SESSION_MAX_IDLE_TIME="1h"
REQUIRE_PERSISTED_SESSIONS="true"

# ============================================
# 安全配置
# ============================================
APPROVAL_HMAC_KEY="<至少 32 字节>"
DAL_INTERNAL_TOKEN="<internal-service-token>"
GATEWAY_INTERNAL_TOKEN="<gateway-service-token>"

# ============================================
# 服务配置
# ============================================
SERVICE_NAME="auth-agent"
SERVICE_VERSION="1.0.0"
HTTP_ADDR=":8092"
READ_TIMEOUT="30s"
WRITE_TIMEOUT="60s"

# ============================================
# 日志配置
# ============================================
LOG_LEVEL="info"  # debug, info, warn, error
LOG_FORMAT="json"  # json, text
AUTH_DEBUG="false"

# ============================================
# 监控配置
# ============================================
METRICS_ENABLED="true"
METRICS_ADDR=":9090"
TRACING_ENABLED="false"
TRACING_ENDPOINT="http://jaeger:14268/api/traces"
```

#### 8.1.2 配置文件示例

**config.yaml**（可选，补充环境变量）：
```yaml
service:
  name: auth-agent
  version: 1.0.0
  http_addr: :8092

jwt:
  issuer: redclaw-auth
  audience: redclaw-api
  leeway: 30s

sso:
  providers:
    - name: google
      issuer: https://accounts.google.com
      scopes:
        - openid
        - profile
        - email
    - name: okta
      issuer: https://your-domain.okta.com
      scopes:
        - openid
        - profile
        - email

session:
  ttl: 15m
  max_idle_time: 1h
  require_persisted: true

database:
  host: localhost
  port: 5432
  name: redclaw_platform
  user: platform_app
  ssl_mode: require
  max_conns: 25
  min_conns: 5

logging:
  level: info
  format: json

metrics:
  enabled: true
  addr: :9090
```

### 8.2 API 端点参考

#### 8.2.1 认证端点

**SSO 登录发起**
```http
GET /sso/login/{provider}?origin=admin&redirect_uri=https://app.example.com/callback
```
- **描述**：发起 SSO 登录流程
- **参数**：
  - `provider`：身份提供商（google, okta, azure）
  - `origin`：登录来源（admin, cli, mobile）
  - `redirect_uri`：登录成功后的重定向 URL
- **响应**：302 重定向到 IdP 授权页面

**SSO 回调**
```http
GET /sso/callback/{provider}?code=AUTH_CODE&state=STATE
```
- **描述**：IdP 授权后的回调端点
- **参数**：
  - `code`：授权码
  - `state`：CSRF 防护参数
- **响应**：
  ```json
  {
    "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
    "expires_in": 900,
    "user": {
      "id": "user-id",
      "email": "user@example.com",
      "tenant_id": "tenant-id"
    }
  }
  ```

**Token 刷新**
```http
POST /auth/refresh
Authorization: Bearer <OLD_JWT>
```
- **描述**：刷新即将过期的 Token
- **响应**：
  ```json
  {
    "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
    "expires_in": 900
  }
  ```

**登出**
```http
POST /auth/logout
Authorization: Bearer <JWT>
```
- **描述**：撤销当前会话
- **响应**：
  ```json
  {
    "message": "logged out successfully"
  }
  ```

#### 8.2.2 会话管理端点

**列出当前用户的会话**
```http
GET /api/v1/sessions
Authorization: Bearer <JWT>
```
- **响应**：
  ```json
  {
    "sessions": [
      {
        "id": "session-id",
        "created_at": 1694520000,
        "expires_at": 1694520900,
        "last_seen_at": 1694520300,
        "metadata": {
          "user_agent": "Mozilla/5.0...",
          "ip_address": "192.168.1.100"
        }
      }
    ]
  }
  ```

**撤销特定会话**
```http
DELETE /api/v1/sessions/{session_id}
Authorization: Bearer <JWT>
```
- **描述**：用户撤销自己的会话
- **响应**：
  ```json
  {
    "message": "session revoked"
  }
  ```

**管理员撤销任意会话**
```http
POST /admin/sessions/{session_id}/revoke?tenant_id=xxx
Authorization: Bearer <ADMIN_JWT>
```
- **请求体**：
  ```json
  {
    "reason": "security_incident"
  }
  ```
- **响应**：
  ```json
  {
    "message": "session revoked successfully",
    "session": {
      "id": "session-id",
      "tenant_id": "tenant-id",
      "revoked_at": 1694520500,
      "revoked_by": "admin:admin-user-id"
    }
  }
  ```

#### 8.2.3 特殊端点

**Pocket 集成（共享密钥认证）**
```http
POST /api/v1/users/verify
Authorization: Bearer <SHARED_SECRET>
Content-Type: application/json

{
  "email": "user@example.com"
}
```
- **描述**：使用共享密钥验证用户（Pocket 专用）
- **认证**：共享密钥（至少 32 字节）
- **响应**：
  ```json
  {
    "user_id": "user-id",
    "tenant_id": "tenant-id",
    "verified": true
  }
  ```

### 8.3 测试

#### 8.3.1 单元测试示例

**JWT 中间件测试**：
```go
func TestJWTMiddleware_ValidToken(t *testing.T) {
    cfg := middleware.JWTConfig{
        SigningKey: []byte("test-key-at-least-32-bytes-long!"),
        Issuer:     "test-issuer",
        Audience:   "test-audience",
    }
    
    // 生成有效 Token
    claims := middleware.Claims{
        Subject:  "user123",
        TenantID: "tenant456",
        Roles:    []string{"user"},
    }
    token, _ := middleware.IssueToken(cfg, claims, 15*time.Minute)
    
    // 创建测试请求
    w := httptest.NewRecorder()
    c, _ := gin.CreateTestContext(w)
    c.Request = httptest.NewRequest("GET", "/api/test", nil)
    c.Request.Header.Set("Authorization", "Bearer "+token)
    
    // 执行中间件
    middleware.JWTMiddleware(cfg)(c)
    
    // 验证结果
    assert.Equal(t, 200, w.Code)
    extractedClaims, ok := middleware.GetClaims(c)
    assert.True(t, ok)
    assert.Equal(t, "user123", extractedClaims.Subject)
    assert.Equal(t, "tenant456", extractedClaims.TenantID)
}
```

#### 8.3.2 集成测试 Fixtures

**SSO 测试 Fixture**：
```go
type SSOTestFixture struct {
    Tenant    *Tenant
    SSOConfig *SSOConfig
    MockIdP   *MockIdentityProvider
}

func NewSSOTestFixture(t *testing.T) *SSOTestFixture {
    tenant := &Tenant{
        ID:   "test-tenant",
        Name: "Test Tenant",
    }
    
    ssoConfig := &SSOConfig{
        TenantID:     tenant.ID,
        Provider:     "test-idp",
        ClientID:     "test-client-id",
        ClientSecret: "test-client-secret",
        Issuer:       "http://localhost:9999",
        IsActive:     true,
    }
    
    mockIdP := &MockIdentityProvider{
        Issuer:   ssoConfig.Issuer,
        ClientID: ssoConfig.ClientID,
    }
    
    return &SSOTestFixture{
        Tenant:    tenant,
        SSOConfig: ssoConfig,
        MockIdP:   mockIdP,
    }
}

func TestSSOLoginFlow(t *testing.T) {
    fixture := NewSSOTestFixture(t)
    
    // 1. 发起登录
    loginURL, state, err := ssoManager.LoginURL("admin")
    assert.NoError(t, err)
    assert.Contains(t, loginURL, fixture.MockIdP.Issuer)
    
    // 2. 模拟 IdP 授权
    authCode := fixture.MockIdP.IssueAuthCode(state)
    
    // 3. 处理回调
    jwtToken, claims, err := ssoManager.HandleCallback(context.Background(), authCode, state, "")
    assert.NoError(t, err)
    assert.NotEmpty(t, jwtToken)
    assert.Equal(t, fixture.Tenant.ID, claims.Tenant)
}
```

#### 8.3.3 性能基准

**Token 验证性能**：
```go
func BenchmarkJWTVerification(b *testing.B) {
    cfg := middleware.JWTConfig{
        SigningKey: []byte("benchmark-key-at-least-32-bytes!"),
        Issuer:     "bench-issuer",
        Audience:   "bench-audience",
    }
    
    claims := middleware.Claims{
        Subject:  "user123",
        TenantID: "tenant456",
    }
    token, _ := middleware.IssueToken(cfg, claims, 15*time.Minute)
    
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        _, _ = parseToken(token, cfg)
    }
}

// 结果示例：
// BenchmarkJWTVerification-8    100000    12345 ns/op    2048 B/op    15 allocs/op
```

### 8.4 相关文档链接

#### 8.4.1 内部文档

- **架构文档**：[ARCHITECTURE.md](/Users/xutaohuang/workspace/ai-native-tools/RedClaw/ARCHITECTURE.md)
- **本地部署指南**：[LOCAL-DOCKER-STACK-2026-08.md](/Users/xutaohuang/workspace/ai-native-tools/RedClaw/docs/LOCAL-DOCKER-STACK-2026-08.md)
- **Pocket 集成**：[pocket-integration-2026-08/03-需求矩阵与接口契约.md](/Users/xutaohuang/workspace/ai-native-tools/RedClaw/docs/pocket-integration-2026-08/03-需求矩阵与接口契约.md)
- **ADR: JWKS 迁移**：[adr/2026-08-20-jwks-migration.md](/Users/xutaohuang/workspace/ai-native-tools/RedClaw/docs/adr/2026-08-20-jwks-migration.md)

#### 8.4.2 代码位置

- **Auth Agent**：`services/platform-go/internal/authagent/`
- **JWT 中间件**：`services/platform-go/internal/platform/middleware/`
- **Session Store**：`services/platform-go/internal/orchestrator/session/`
- **数据库迁移**：`services/platform-go/migrations/`

#### 8.4.3 外部标准

- **JWT (RFC 7519)**：https://datatracker.ietf.org/doc/html/rfc7519
- **OIDC Core 1.0**：https://openid.net/specs/openid-connect-core-1_0.html
- **OAuth 2.0 (RFC 6749)**：https://datatracker.ietf.org/doc/html/rfc6749
- **PKCE (RFC 7636)**：https://datatracker.ietf.org/doc/html/rfc7636

---

## 附录

### A. 术语表

| 术语 | 全称/英文 | 说明 |
|------|----------|------|
| SSO | Single Sign-On | 单点登录，用户一次认证后可访问多个应用 |
| OIDC | OpenID Connect | 基于 OAuth 2.0 的身份认证协议 |
| JWT | JSON Web Token | 紧凑的 URL 安全令牌格式 |
| SAML | Security Assertion Markup Language | 企业级 SSO 标准协议 |
| IdP | Identity Provider | 身份提供商，如 Google、Okta |
| Claims | JWT Claims | JWT Token 中包含的声明（用户信息、权限等） |
| PKCE | Proof Key for Code Exchange | 授权码流程的安全增强 |
| RBAC | Role-Based Access Control | 基于角色的访问控制 |
| CSRF | Cross-Site Request Forgery | 跨站请求伪造攻击 |
| XSS | Cross-Site Scripting | 跨站脚本攻击 |

### B. 快速参考卡片

**生成 JWT 签名密钥**：
```bash
openssl rand -base64 32
```

**解码 JWT Token（调试用）**：
```bash
echo "<TOKEN>" | cut -d. -f2 | base64 -d | jq
```

**检查活跃会话数**：
```sql
SELECT COUNT(*) FROM authagent.sessions 
WHERE expires_at > NOW() AND revoked_at IS NULL;
```

**撤销用户所有会话**：
```sql
UPDATE authagent.sessions 
SET revoked_at = NOW(), revoked_by = 'admin', revoke_reason = 'force_logout'
WHERE tenant_id = 'xxx' AND subject_id = 'yyy' AND revoked_at IS NULL;
```

---

**文档结束**

> 如有问题或建议，请联系 RedClaw Platform Team 或提交 Issue。
> 
> **文档维护**: 请在更新认证系统后同步更新本文档。
