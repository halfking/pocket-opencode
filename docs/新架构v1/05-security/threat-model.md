# ZAG 威胁模型（STRIDE，审计修正版）

> **状态**：与 `docs/新架构v1/01-architecture/安全模型.md` 配套的实施前威胁建档。
> 引用每条威胁映射到的 ADR 或合约文档。所有威胁 ID 形如 `T-<维度>-<序号>`，
> 在 `test-matrix.md` 与 `release-gate.md` 中通过同一 ID 引用。

---

## 0. 范围与信任域

| 域 | 角色 | 关键资产 |
|---|---|---|
| 不可信客户端域 | OpenPocket Mobile、IDE、MCP 客户端 | 用户凭证、intent、参数 |
| Pocket 控制域 | pocketd | JWT 签发、对象授权、WS/SSE 出口、通知 |
| ZAgentGateway 控制适配域 | ZAG | delegated token、operation mapping、策略投影、审计 outbox |
| RedClaw 控制域 | platform-go | tenant/policy/approval/audit |
| acc-go 编排域 | orchestrator | taskdecompose/MCP/mission |
| 用户 PC 执行域 | OpenCode / OpenClaw / IDE / shell | workspace、源码、密钥、git |

威胁模型关注跨域攻击面与单点失败（POF），不重复业务设计文档。

---

## 1. STRIDE 威胁清单（21 条）

### S — Spoofing（伪装，6 条）

#### T-S-01：用户级 token 越域重放
- **资产**：ZAG delegated token（ADR-0001）、pocketd 签发的 user token。
- **攻击路径**：攻击者在 `pocket` 域抓取到普通 Web JWT，删除或修改 `aud`，重放到 ZAG `/api/v1/*`，试图让 ZAG 把它当 `zagent-gateway` audience 接受。
- **前置条件**：中间人能看到流量或能接触到泄露的 JWT。
- **影响**：越权访问任意用户任务、Pod、IDE。
- **缓解措施**：
  - ZAG 仅接受 ADR-0001 §4 规定的 audience；其余 audience 直接 `ZAG_AUTH_BAD_AUDIENCE`。
  - ZAG 不复用 Web JWT（独立 delegated token，ADR-0001 §10）。
  - pocketd 拒绝把内部 user JWT 透传给 ZAG（`fleetbridge/zag_client.go` 二次签发）。
- **剩余风险**：若 pocketd 与 ZAG 共享 KMS 密钥，且 KMS 失陷，两个 audience 等价。
- **关联 ADR/合约**：`docs/security/zag-adr-0001-token-format.md`、`docs/新架构v1/01-architecture/安全模型.md` §3.2。

#### T-S-02：算法替换（`alg=none` / `alg=HS256`）
- **资产**：ZAG 验证 token 的密钥。
- **攻击路径**：构造 `alg=none` 的 token 或把 EdDSA 公钥作为 HS256 共享密钥使用，绕过签名验证。
- **前置条件**：ZAG JWT 库未硬编码允许算法集合。
- **影响**：完整身份冒用，可发起任意受控操作。
- **缓解措施**：
  - ADR-0001 §3 强制 `alg ∈ {EdDSA, RS256}`；解析前拒绝其他。
  - 测试 `TestAuthTokenRejectsAlgSubstitution` 必须通过。
- **剩余风险**：依赖第三方 JWT 库未来不会引入新默认算法；CI 必须锁版本。
- **关联 ADR/合约**：`docs/security/zag-adr-0001-token-format.md` §3、`docs/adr/2026-08-20-jwks-migration.md`。

#### T-S-03：mTLS 缺失降级（fallback to HMAC）
- **资产**：服务间 mTLS 通道。
- **攻击路径**：运维在 TLS 握手失败时自动把 `TLSConfig` 切回 `nil`，使 ZAG↔RedClaw 走明文 + 共享 HMAC。
- **前置条件**：存在 fallback 路径或运维误关。
- **影响**：token、payload、签名都被窃听，间接导致越权。
- **缓解措施**：
  - ADR-0002 强制 fail-closed；JWKS/CA 不可达必须返回 `ZAG_AUTH_ISSUER_UNREACHABLE`。
  - ZAG 启动时若加载不到 SVID 直接 panic，不允许"best-effort"。
  - `TestMTLSEnforcedFailClosed` 验证关闭 CA 后请求被拒。
- **剩余风险**：SPIRE 自身被攻陷（PKI 级 POF）。
- **关联 ADR/合约**：`docs/security/zag-adr-0002-mtls.md` §1/§8。

#### T-S-04：JWKS cache 中毒
- **资产**：ZAG 缓存的 issuer JWKS。
- **攻击路径**：污染 issuer 的 JWKS 端点（CDN/DNS/中间件）使 ZAG 把攻击者公钥当主键。
- **前置条件**：ZAG JWKS 拉取只走 HTTP，且无交叉验证。
- **影响**：攻击者签发的任何 token 都通过验证。
- **缓解措施**：
  - JWKS 拉取走 mTLS（ADR-0002）并校验 issuer 证书固定主体。
  - `Cache-Control: max-age` 严格遵守；超过 `max-age` 立即重拉。
  - 测试：mock issuer 替换 JWKS，ZAG 必须 `ZAG_AUTH_UNKNOWN_KID`。
- **剩余风险**：若 max-age 设大（如 24h），中毒窗口可达上限。
- **关联 ADR/合约**：`docs/security/zag-adr-0001-token-format.md` §8。

#### T-S-05：SVID 短密钥被静态签发冒充
- **资产**：SPIRE 颁发的 X.509 SVID。
- **攻击路径**：伪造一个 leaf cert，使用相同 trust domain 的 SAN 字段，让未授权 Pod 假冒 `pocketd`/`redclaw` 接入 ZAG。
- **前置条件**：SPIRE 中间 CA 失陷或攻击者能拿到合法 signing key。
- **影响**：伪装 peer 服务，绕开所有应用层身份。
- **缓解措施**：
  - SPIRE 中间 CA 离线保存；private key 写入 HSM/KMS（ADR-0002 §1）。
  - ZAG 强制 `client_cert_fp` 与 token claim 比对（ADR-0003 §4.6）。
  - 周期性 revoke list 拉取，最坏 5 分钟失效。
- **剩余风险**：SPIRE server 入侵的恢复时间 < 5 分钟（bundle 周期）。
- **关联 ADR/合约**：`docs/security/zag-adr-0002-mtls.md` §6。

#### T-S-06：第三方 MCP client 跨租户身份复用
- **资产**：MCP client 注册身份（Cursor、Claude Code）。
- **攻击路径**：MCP session 长连接不重置 principal，把 tenant A 的 client id 缓存带到 tenant B 调用。
- **前置条件**：ZAG MCP 入口在 session 切换时未重新解析 identity。
- **影响**：用 tenant A 的 identity 调 tenant B 的 `zag_*` 工具。
- **缓解措施**：
  - 安全模型 §3.4 要求每次 `tools/call` 重新做对象授权；不允许 session 级缓存。
  - 测试：MCP session A 认证后串到 B 必须 `cross_tenant_denied`。
- **剩余风险**：取决于 MCP 客户端实现是否会复用 token；ZAG 不能控制。
- **关联 ADR/合约**：`docs/新架构v1/01-architecture/安全模型.md` §3.4。

---

### T — Tampering（篡改，4 条）

#### T-T-01：审计 outbox 注入 / 漏写
- **资产**：durable append-only 审计日志。
- **攻击路径**：在 ZAG handler 错误路径上漏写 audit，或在 outbox flush 之前返回 success。
- **前置条件**：审计与业务提交存在 race；或审计 sink 被降级为异步。
- **影响**：高危操作无法溯源；监管/取证缺失。
- **缓解措施**：
  - ADR-0007 §4：handler 必须在 audit WORM 写成功之后才返回 success。
  - ZAG audit 实现使用 `audit.Recorder` 同步包装器，失败返回 `ErrSinkUnavailable`。
  - 测试：`TestAuditFailClosedOnSinkError`、`TestAuditRecoveryOutbox`。
- **剩余风险**：跨实例时钟偏移导致 ordering 不确定。
- **关联 ADR/合约**：`docs/security/zag-adr-0007-audit.md`、`docs/新架构v1/01-architecture/安全模型.md` §7.4。

#### T-T-02：事件 prev_event_hash 链断裂 / 篡改
- **资产**：event outbox 哈希链。
- **攻击路径**：直接修改 outbox 记录或重写 `prev_event_hash`，让消费者误以为链连续。
- **前置条件**：outbox 写入后未做 detached signature 校验。
- **影响**：隐藏某次高危操作，或伪造"未发生"。
- **缓解措施**：
  - ADR-0006 §3：每个 event 都用 cluster signing key 做 Ed25519 detached signature。
  - WORM 桶拒绝覆盖（ADR-0007 §2）。
- **剩余风险**：cluster key 失陷 = 整链可伪造。
- **关联 ADR/合约**：`docs/security/zag-adr-0006-event-safety.md`、`docs/security/zag-adr-0007-audit.md`。

#### T-T-03：审批 canonical payload 篡改
- **资产**：approval request 的 canonical JSON + 第二个签名。
- **影响**：把"git push"审批参数里的 `branch=main` 改成 `branch=master` 或把 diff 内容替换为恶意版本，但双签仍通过。
- **前置条件**：canonical JSON 字段排序/序列化方案不固定；不同语言实现产生不同字节序列。
- **缓解措施**：
  - 使用 RFC 8785 JCS（JSON Canonicalization Scheme）或 Go `encoding/json/v2` 排序键。
  - 在签名时除 args/digest 之外，必须包含 `aggregate_version`、`policy_version`、`nonce`、`expires_at`。
  - 测试：篡改任一字段必须让第二个签名验证失败。
- **剩余风险**：取决于序列化库的实现稳定性。
- **关联 ADR/合约**：`docs/新架构v1/01-architecture/安全模型.md` §5.3。

#### T-T-04：日志脱敏绕过
- **资产**：日志 / 审计 detail 中的 API key、JWT、SSH key。
- **攻击路径**：构造未在 allowlist 中的 key 名（如 `pwd`、`access_token_alt`）绕过 redactDetail。
- **前置条件**：redactDetail 只覆盖已知敏感 key 名集合。
- **影响**：凭据随日志进入 ELK、备份、合规导出。
- **缓解措施**：
  - 现有 `audit_writer.go` 已用 allowlist + 长度截断（测试 `TestRedactDetail_StripsSensitiveValues`）。
  - 未知字段默认丢弃或哈希化（安全模型 §8.4）。
  - 测试：`TestLogRedactionHardensUnknownFields`。
- **剩余风险**：未覆盖的新格式（如 protobuf 编码的 token）。
- **关联 ADR/合约**：`docs/新架构v1/01-architecture/安全模型.md` §8.4、`backend/internal/server/audit_writer.go`。

---

### R — Repudiation（否认，3 条）

#### T-R-01：审批人否认行为
- **资产**：approver 的不可篡改审批记录。
- **攻击路径**：approver 在事件发生后删除/篡改自己签字的 approval 记录。
- **前置条件**：审批表未做 WORM 存储，或允许 `UPDATE`。
- **影响**：合规审计失败；事后追责空缺。
- **缓解措施**：
  - 审批表使用 WORM；append-only。
  - 审批记录同步落 `audit` 并包含 `idempotency_key`、`prev_audit_hash`、`signature`（ADR-0007 §1）。
  - 测试：尝试 `UPDATE approval WHERE id=?` 必须失败。
- **剩余风险**：DB 超级用户可绕过；需要 KMS-level 防御。
- **关联 ADR/合约**：`docs/security/zag-adr-0007-audit.md` §2。

#### T-R-02：客户端否认 intent
- **资产**：用户发起的 intent 历史。
- **攻击路径**：用户发起"删除某个 session"，完成后声称未操作。
- **前置条件**：intent 请求未绑定设备指纹或 chain-of-custody。
- **影响**：计费/合规争议无据可依。
- **缓解措施**：
  - 每个 intent 必须带 `X-Request-Id`、`device_id`、`actor_id`，落 audit（ADR-0001 §1、`04-contracts/pocket-zag-incremental.md` §3）。
  - 测试：`TestIntentImmutability`。
- **剩余风险**：设备指纹可被伪造；需依赖 mTLS `device_id` 绑定。
- **关联 ADR/合约**：`docs/security/zag-adr-0001-token-format.md`、`docs/新架构v1/04-contracts/pocket-zag-incremental.md`。

#### T-R-03：审计时间漂移否认窗口
- **资产**：审计时间戳。
- **攻击路径**：在不同实例上发起"先行 deny 试探再正式执行"的请求，否认 deny 试探。
- **前置条件**：deny 决策未落 audit。
- **影响**：对手可探测 RBAC 而不留痕。
- **缓解措施**：
  - ADR-0003 §7：每次决策都落 audit，包括 deny。
  - 测试：模拟探测请求后 `audit_query` 必须返回 deny 记录。
- **剩余风险**：audit sink 故障期间无记录（fail-closed 但要可观测）。
- **关联 ADR/合约**：`docs/security/zag-adr-0003-authz-model.md` §7。

---

### I — Information Disclosure（信息泄漏，4 条）

#### T-I-01：SSRF 探测用户内网
- **资产**：用户内网元数据服务（169.254.169.254）、Redis、Postgres。
- **攻击路径**：通过 IDE connector execute 让 ZAG 发起任意 HTTP 出站，命中内网地址。
- **前置条件**：connector endpoint 未做 allowlist / SSRF 校验。
- **影响**：凭据泄漏（云 metadata）、内网横向。
- **缓解措施**：
  - ZAG 只允许调用 connector 注册表中已登记的 endpoint（安全模型 §4.2）。
  - 现有 `backend/internal/server/ssrf.go` 的 `validateOutboundURL` 已经拦截 loopback、私网、metadata IP。
  - 测试：`TestSSRFBlockMetadataAndPrivate`。
- **剩余风险**：DNS rebinding（解析时合法、拨号时切换 IP）；需要 dial-time 校验 IP。
- **关联 ADR/合约**：`docs/新架构v1/01-architecture/安全模型.md` §4.2、`backend/internal/server/ssrf.go`。

#### T-I-02：跨租户读取 SSE 流
- **资产**：OpenPocket WS Hub、ZAG SSE `/api/v1/tasks/:id/events`。
- **攻击路径**：订阅其他 tenant 的 task event（通过修改 path 中的 task_id，或在 subscribe 时改 client scope）。
- **前置条件**：subscribe handler 不重做对象授权。
- **影响**：看到其他用户的 prompt、diff、审批结论。
- **缓解措施**：
  - 安全模型 §6：`subscribe` 时逐对象授权；不因已认证就允许任意 task/pod。
  - 测试：`TestSSESubscribeTenantIsolation`。
- **剩余风险**：WS ticket 被截获后到过期前可用；需 ticket 短 TTL + 强制 mTLS 绑定。
- **关联 ADR/合约**：`docs/新架构v1/01-architecture/安全模型.md` §6。

#### T-I-03：日志 / 错误响应里回显 token
- **资产**：JWT、delegated token、API key。
- **攻击路径**：未捕获 panic / 错误响应里把原始 token 写在 5xx body 或 panic stack 中。
- **前置条件**：handler 在错误处理路径上 `%v` 整个 request。
- **影响**：凭据落到 ELK、客户端 telemetry、Sentry。
- **缓解措施**：
  - error envelope 严格 allowlist；`redactDetail` 在写入前替换。
  - middleware 把 `Authorization` 头从 panic dump 中移除。
  - 测试：`TestErrorEnvelopeScrubsToken`、`TestPanicScrubsAuthHeader`。
- **剩余风险**：第三方库 `debug.Stack()` 中可能残留。
- **关联 ADR/合约**：`docs/新架构v1/01-architecture/安全模型.md` §8.4。

#### T-I-04：环境变量泄漏到 IDE plugin
- **资产**：用户 PC 上的 SSH key、provider key、OS token。
- **攻击路径**：OpenClaw 启动子进程时未使用 env allowlist，把 `~/.aws/credentials`、`GITHUB_TOKEN` 一起继承。
- **前置条件**：subprocess 调用未做 env 过滤。
- **影响**：敏感 env 被 IDE 插件/脚本读取并外发。
- **缓解措施**：
  - 安全模型 §4.2：env allowlist；只传 PATH、LANG 等白名单变量。
  - 测试：起进程后 `os.Environ()` 不应包含敏感 key。
- **剩余风险**：依赖 RedClaw agentcontainer 是否实现 allowlist（合规边界）。
- **关联 ADR/合约**：`docs/新架构v1/01-architecture/安全模型.md` §4.2。

---

### D — Denial of Service（拒绝服务，2 条）

#### T-D-01：审计 sink 故障引发全局拒绝
- **资产**：WORM bucket / Postgres。
- **攻击路径**：审计 sink 抖动 → ZAG 拒绝所有高危操作（fail-closed），造成大范围功能不可用。
- **前置条件**：sink SLA 与 ZAG SLA 不匹配。
- **影响**：租户功能全面受限（控制面）。
- **缓解措施**：
  - breaker + cooldown（`audit.Recorder` 已有）。
  - ADR-0007 §4：仅高危操作 fail-closed；只读不受影响。
  - 测试：`TestAuditBreakerDoesNotBlockReads`。
- **剩余风险**：sink 长期不可用 → 业务停滞；需要明确切换到降级模式的人工 SOP。
- **关联 ADR/合约**：`docs/security/zag-adr-0007-audit.md` §4、`services/zagent-gateway/internal/audit/audit.go`。

#### T-D-02：恶意 SSE 客户端资源耗尽
- **资产**：ZAG Hub buffer、backpressure。
- **攻击路径**：订阅大量 task/pod/event，且慢消费导致 hub buffer 耗尽。
- **前置条件**：无连接级 max-buffer、无消息大小限制。
- **影响**：其他租户的 SSE 也被拖慢 → 拒绝服务。
- **缓解措施**：
  - 安全模型 §6：单连接消息数 / 尺寸 / 订阅数 / buffer 限额；慢消费者强制断连 + 补偿。
  - 测试：`TestHubDropsSlowConsumer`。
- **剩余风险**：合理的 burst 流量被错杀；需要合理 backlog。
- **关联 ADR/合约**：`docs/新架构v1/01-architecture/安全模型.md` §6。

---

### E — Elevation of Privilege（权限提升，2 条）

#### T-E-01：RBAC 越权（role 混淆）
- **资产**：角色→动作矩阵（ADR-0003 §3）。
- **攻击路径**：持有 `viewer` 角色但利用 endpoint 缺陷执行 `approve` 或 `rotate_key`。
- **前置条件**：handler 未走 policy engine 直接调用底层 store。
- **影响**：权限提升。
- **缓解措施**：
  - 所有写操作必须经过 ADR-0003 §5 decision pipeline；缺失任一阶段直接 503。
  - 测试：每个 role × verb 组合有 table-driven 测试覆盖 deny/allow。
- **剩余风险**：新加 verb 时若漏测，会绕过；需要静态扫描 + 测试覆盖率卡点。
- **关联 ADR/合约**：`docs/security/zag-adr-0003-authz-model.md`、`docs/新架构v1/01-architecture/安全模型.md` §3.3。

#### T-E-02：scope escalation via token claim injection
- **资产**：delegated token 的 `scope` claim。
- **攻击路径**：把 `scope: "viewer"` 改写为 `scope: "agents.read agents.invoke sessions.write tasks.write permissions.reply"`；若 ZAG 未对 scope 集做严格闭合校验，则接受超集 scope。
- **前置条件**：ZAG 在解析时把 scope 字段当 string 做 `strings.Contains`，而非 set 比较。
- **影响**：权限提升。
- **缓解措施**：
  - scope 解析必须 split → set；权限检查用 `HasScope` / `HasAnyScope`（`identity.Principal`）。
  - 测试：篡改 scope 必须被拒；多余 scope 不影响 deny。
- **剩余风险**：对未来新 scope（如 `ide.write`）未在 matrix 中即默认拒绝；需持续维护。
- **关联 ADR/合约**：`docs/security/zag-adr-0003-authz-model.md` §1、`docs/新架构v1/01-architecture/安全模型.md` §3.2。

---

## 2. 维度统计

| 维度 | 威胁数 |
|---|---:|
| S — Spoofing | 6 |
| T — Tampering | 4 |
| R — Repudiation | 3 |
| I — Information Disclosure | 4 |
| D — Denial of Service | 2 |
| E — Elevation of Privilege | 2 |
| **总计** | **21** |

---

## 3. 引用文档（ADR / 合约）

- `docs/security/zag-adr-0001-token-format.md`
- `docs/security/zag-adr-0002-mtls.md`
- `docs/security/zag-adr-0003-authz-model.md`
- `docs/security/zag-adr-0004-request-safety.md`
- `docs/security/zag-adr-0005-state-replication.md`
- `docs/security/zag-adr-0006-event-safety.md`
- `docs/security/zag-adr-0007-audit.md`
- `docs/新架构v1/01-architecture/安全模型.md`
- `docs/新架构v1/02-modules/zagent-gateway.md`
- `docs/新架构v1/04-contracts/pocket-zag-incremental.md`
- `docs/新架构v1/04-contracts/pocket-adapter-matrix.md`
- `services/zagent-gateway/internal/audit/audit.go`
- `services/zagent-gateway/internal/identity/principal.go`
- `backend/internal/server/ssrf.go`
- `backend/internal/server/audit_writer.go`
