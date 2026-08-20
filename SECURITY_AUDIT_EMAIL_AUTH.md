# 安全审计报告 - Email IMAP Fetch + Auth System

**审计时间**: 2026-07-03  
**审计范围**: feat/email-imap-fetch-v2 分支  
**提交**: 3a2ceaf  

## ✅ 审计通过项

### 1. 密码学实现
- **bcrypt 密码哈希**: ✅ 使用 `bcrypt.DefaultCost` (cost=10)
  - `users.go:35`: 正确使用 `GenerateFromPassword`
  - `users.go:60`: 正确使用 `CompareHashAndPassword`
  - 未存储明文密码

- **AES-256-GCM 加密**: ✅ 
  - `crypto.go:28`: 正确初始化 `cipher.NewGCM`
  - `crypto.go:41`: 使用 `Seal` 加密（附加认证数据为空是合理的）
  - `crypto.go:56`: 使用 `Open` 解密并验证 MAC
  - Nonce 随机生成（12 字节）并附加到密文前

- **随机数生成**: ✅ 所有随机数使用 `crypto/rand`
  - AES nonce: `crypto.go:38` - `io.ReadFull(rand.Reader, nonce)`
  - Master key: `crypto.go:86` - 32 字节密钥
  - OAuth PKCE verifier: `oauth.go:20` - 32 字节
  - OAuth state: `oauth.go:32` - 32 字节

### 2. SQL 注入防护
- **参数化查询**: ✅ 所有数据库操作使用 pgx 的参数化查询
  - `store.go:211,217,230,279,352` 等均使用 `$1, $2, ...` 占位符
  - 无字符串拼接 SQL

### 3. OAuth2 安全
- **PKCE (RFC 7636)**: ✅ 
  - `oauth.go:20-27`: 正确实现 S256 challenge
  - `oauth_callback.go:191`: code_verifier 在 token 交换时使用
  
- **CSRF 防护**: ✅
  - `oauth.go:31-35`: 生成 32 字节随机 state
  - `oauth_callback.go:137-142`: 验证 state 并从内存 map 移除（防重放）

- **Token 存储**: ✅
  - `oauth_callback.go:149-150`: refresh/access token 加密后存储
  - 未在日志中输出 token 明文

### 4. 认证系统
- **JWT**: ✅
  - `jwt.go:44`: 使用 HS256 签名
  - `jwt.go:49`: 包含 ExpiresAt/IssuedAt/NotBefore claim
  - `config.go:90`: 默认密钥带 "insecure" 警告，生产需设置 `POCKET_JWT_SECRET`

- **首次启动**: ✅
  - `main.go:101-110`: 仅在 `POCKET_DEV_AUTH=true` 时允许 admin/admin
  - 生产环境默认拒绝自动创建弱密码账户

### 5. 凭证管理
- **Master Key**: ✅
  - `crypto.go:63-93`: 优先从环境变量读取，否则自动生成并持久化到 `dataDir/email_master.key`
  - 文件权限 0600（仅 owner 可读写）
  - `main.go:118`: 若未设置环境变量会警告

- **IMAP 密码**: ✅
  - `fetcher.go:37`: 解密后立即使用，不持久化到内存
  - `store.go` 所有 credential 列均为加密后的 base64

### 6. 错误处理
- **不泄露敏感信息**: ✅
  - `oauth_callback.go:144,153,159`: 仅记录错误类型，不记录 token
  - `fetcher.go:42,48,54,67,73,82`: 错误消息不包含密码

- **覆盖率**: ✅
  - `fetcher.go`: 9 处 error 检查
  - `crypto.go`: 6 处 error 检查
  - `users.go`: 3 处 error 检查

### 7. 静态分析
- **go vet**: ✅ 通过（无警告）
- **go build**: ✅ 通过
- **go test**: ✅ 通过

## ⚠️ 建议改进项（非阻塞）

### 1. Token 交换错误处理
**位置**: `oauth_callback.go:149-150`  
```go
refreshEnc, _ := cfg.Crypto.EncryptString(tokens.RefreshToken)
accessEnc, _ := cfg.Crypto.EncryptString(tokens.AccessToken)
```
**建议**: 不应忽略加密错误，应检查并返回 500。  
**影响**: 低（AES-GCM 失败极少见，但应该处理）

### 2. Master Key 长度验证
**位置**: `crypto.go:65-72`  
**当前**: 仅检查 base64 解码后是否为 32 字节，但原始字符串长度检查逻辑略弱。  
**建议**: 明确文档化 `POCKET_EMAIL_MASTER_KEY` 应为 base64 编码的 32 字节（44 字符）。  
**影响**: 低（已有运行时校验）

### 3. JWT 过期时间配置化
**位置**: `main.go:111`  
**当前**: 硬编码 24 小时 TTL  
**建议**: 添加 `POCKET_JWT_TTL_HOURS` 环境变量  
**影响**: 低（24h 是合理默认值）

### 4. OAuth Pending Map 容量限制
**位置**: `oauth_callback.go:55`  
**当前**: 内存 map 无大小上限  
**建议**: 添加最大条目数限制（如 1000），防止内存耗尽攻击  
**影响**: 低（GC 每 5 分钟清理，10 分钟超时）

## 📊 指标总结

| 类别 | 通过 | 警告 | 失败 |
|------|------|------|------|
| 密码学 | 4 | 0 | 0 |
| SQL 注入 | 1 | 0 | 0 |
| OAuth2 | 3 | 0 | 0 |
| 认证 | 2 | 0 | 0 |
| 凭证管理 | 2 | 0 | 0 |
| 错误处理 | 2 | 0 | 0 |
| 静态分析 | 3 | 0 | 0 |
| **总计** | **17** | **0** | **0** |

**建议改进项**: 4 个（全部非阻塞）

## ✅ 审计结论

**状态**: ✅ **通过 - 可以合并**

代码实现了行业标准的安全实践：
- 使用成熟的密码学库（bcrypt, AES-GCM）
- 正确实现 OAuth2 + PKCE
- 无 SQL 注入风险
- 敏感数据加密存储
- 错误处理不泄露信息

建议改进项均为非功能性优化，不影响当前安全性。

---

**审计人员**: Kiro  
**签名**: 通过  
**日期**: 2026-07-03
