# ZAG 安全测试矩阵 v1

> 与 `threat-model.md` 一一对应。每条测试给出前置条件、操作、断言、关联威胁 ID
> 与可执行的单元测试路径。**所有"自动化"列必须指向可 `go test` 跑的代码，
> 不允许纯文字描述。**

---

## 1. 跨租户访问（horizontal / vertical）

### TM-01 horizontal — 同角色跨 tenant 拒绝
- **前置条件**：tenant A 的 JWT，URI 指向 tenant B 的 task/pod/session/IDE。
- **操作**：`GET /api/v1/tasks/<tenant-B-task-id>`，Authorization = tenant A 的 JWT。
- **断言**：
  - HTTP `403`。
  - 错误码 `cross_tenant_denied`。
  - audit outbox 落 deny 记录（actor=tenant A，resource=tenant B 的 id，decision=deny）。
- **关联单测**：`backend/internal/security/tenant_isolation_test.go::TestCrossTenantAccessForbidden`
- **威胁 ID**：`T-S-01`、`T-E-01`、`T-I-02`。

### TM-02 vertical — 同 tenant 但 role 不足
- **前置条件**：viewer 角色，URI 指向 `POST /api/v1/permissions/:id/reply`。
- **操作**：用 viewer token 调 approve。
- **断言**：`403 role_forbidden`，无 side effect；audit deny 落盘。
- **关联单测**：`backend/internal/security/tenant_isolation_test.go::TestVerticalRBACBlocksApprove`
- **威胁 ID**：`T-E-01`、`T-E-02`。

---

## 2. 重放攻击（nonce / Idempotency-Key / event cursor）

### TM-03 nonce 重放（delegated token `jti`）
- **前置条件**：ZAG 接受过一次合法 token（jti=X，exp=T+15min）。
- **操作**：原封不动再次发送同一 token。
- **断言**：第二次返回 `401 ZAG_AUTH_TOKEN_REPLAY`；第一次响应保持原状。
- **关联单测**：`backend/internal/security/replay_test.go::TestJTIRepRejected`
- **威胁 ID**：`T-S-01`、`T-S-02`。

### TM-04 Idempotency-Key body collision
- **前置条件**：同一 `(tenant, principal, method, path, key)`，第一次 body=`<A>` 返回 200。
- **操作**：第二次用相同 key 发送 body=`<B>`。
- **断言**：`409 idempotency_key_collision`；第一次响应未被覆盖。
- **关联单测**：`backend/internal/security/replay_test.go::TestIdempotencyKeyBodyCollision`
- **威胁 ID**：`T-T-01`（隐含：审计绑定到原始请求）。

### TM-05 Event cursor 回放（last_event_id 已消费）
- **前置条件**：客户端已收到 event_id=E、sequence=N。
- **操作**：用 `last_event_id=E` 重连 SSE。
- **断言**：客户端收到 sequence > N 的事件；E 不会再次投递。
- **关联单测**：`backend/internal/security/replay_test.go::TestEventCursorReplayNoRedelivery`
- **威胁 ID**：`T-T-02`。

---

## 3. 越权（RBAC bypass / ABAC bypass / scope escalation）

### TM-06 RBAC bypass via missing policy check
- **前置条件**：handler 走旁路直接调底层 store（绕过 policy engine）。
- **操作**：审计代码路径，确认每个写路径前都有 `policy.Evaluate(...)`。
- **断言**：静态扫描（`go test -run TestRBACPathCoverage`）覆盖 100% 写路径。
- **关联单测**：`backend/internal/security/rbac_path_test.go::TestEveryWritePathRunsPolicyEngine`（参考 `audit_writer.go` 的 `Write*` 收敛模式）。
- **威胁 ID**：`T-E-01`。

### TM-07 ABAC bypass — workspace 越界
- **前置条件**：operator 的 `workspace_ids` claim 仅包含 ws_A。
- **操作**：调用 `GET /api/v1/workspaces/ws_B/agents`。
- **断言**：`403 workspace_forbidden`。
- **关联单测**：`backend/internal/security/tenant_isolation_test.go::TestWorkspaceScopeBlocksForeignWS`
- **威胁 ID**：`T-E-01`、`T-I-02`。

### TM-08 Scope escalation via string contains
- **前置条件**：token scope = `"viewer"`；handler 错误地使用 `strings.Contains(scope, "write")`。
- **操作**：scope = `"viewer_write"`（含子串但非真集合）。
- **断言**：`403 role_forbidden`；不允许仅靠字符串子串提升权限。
- **关联单测**：`backend/internal/security/replay_test.go::TestScopeSetSemanticsNotContains`
- **威胁 ID**：`T-E-02`。

---

## 4. 路径逃逸（file / URL / JSON pointer）

### TM-09 File path traversal
- **前置条件**：请求体含 `path = "../../etc/passwd"` 或 Windows `..\\..\\..\\`。
- **操作**：`POST /api/v1/ide/zcode/command {"command":"open_file","args":{"path":"../../etc/passwd"}}`。
- **断言**：`400 invalid_path`；不允许到达下游 connector。
- **关联单测**：`backend/internal/security/path_traversal_test.go::TestFilePathTraversalRejected`
- **威胁 ID**：`T-I-01`、`T-T-04`。

### TM-10 URL path traversal（reverse proxy）
- **前置条件**：请求 `/api/v1/../internal/admin` 或 URL-encoded `%2e%2e`。
- **操作**：标准 URL normalization 后转发。
- **断言**：404 或 400；不得落到 `/internal/*` 路由。
- **关联单测**：`backend/internal/security/path_traversal_test.go::TestURLPathTraversalBlocked`
- **威胁 ID**：`T-I-01`。

### TM-11 JSON pointer escape
- **前置条件**：patch 操作使用 RFC 6901 JSON Pointer，例如 `/secrets/0`。
- **操作**：`{"op":"replace","path":"/../secrets/0","value":"x"}`。
- **断言**：拒绝并返回 `400 invalid_pointer`。
- **关联单测**：`backend/internal/security/path_traversal_test.go::TestJSONPointerEscapeRejected`
- **威胁 ID**：`T-T-03`。

---

## 5. SSRF（agent 拉取 URL / 白名单）

### TM-12 Outbound URL rejects private/loopback/metadata
- **前置条件**：ZAG 需要出站拉取 IDE connector 给的 URL。
- **操作**：调用 `validateOutboundURL`（或等价校验）对：
  - `http://127.0.0.1:8080`
  - `http://10.0.0.5`
  - `http://169.254.169.254/latest/meta-data`
  - `http://[::1]`
- **断言**：全部被拒；返回 `400 forbidden_endpoint`。
- **关联单测**：`backend/internal/security/ssrf_test.go::TestSSRFBlockMetadataAndPrivate`
- **威胁 ID**：`T-I-01`。

### TM-13 Connector endpoint allowlist
- **前置条件**：connector 已注册 endpoint = `https://connector.allowed.local`。
- **操作**：尝试让 ZAG 出站到 `https://attacker.example/`。
- **断言**：禁止；ZAG 只走 allowlist。
- **关联单测**：`backend/internal/security/ssrf_test.go::TestConnectorAllowlistEnforced`
- **威胁 ID**：`T-I-01`。

### TM-14 DNS rebinding
- **前置条件**：hostname 解析为 `1.2.3.4`，但第二次解析切到 `169.254.169.254`。
- **操作**：dial-time 再校验解析到的每一个 IP。
- **断言**：第二次拨号直接失败；不允许 race 窗口。
- **关联单测**：`backend/internal/security/ssrf_test.go::TestDialTimeRebindingDefense`
- **威胁 ID**：`T-I-01`。

---

## 6. 环境变量泄漏（log scrub / error response scrub）

### TM-15 Log scrub
- **前置条件**：handler 写日志，包含 `Authorization: Bearer eyJhbGc...`。
- **操作**：写一行带 token 的 info 日志。
- **断言**：落盘日志不含 token 原文；redactDetail 命中 `authorization`。
- **关联单测**：`backend/internal/security/env_leak_test.go::TestLogScrubsBearerToken`
- **威胁 ID**：`T-I-03`、`T-T-04`。

### TM-16 Error response scrub
- **前置条件**：handler panic，错误信息中包含请求 body（含 API key）。
- **操作**：触发 panic；检查 5xx body。
- **断言**：5xx body 不包含原始 secret；envelope 内 secret 被替换为 `[REDACTED]`。
- **关联单测**：`backend/internal/security/env_leak_test.go::TestErrorEnvelopeScrubsSecret`
- **威胁 ID**：`T-I-03`。

### TM-17 Subprocess env allowlist
- **前置条件**：OpenClaw 启动子进程，环境包含 `AWS_ACCESS_KEY_ID`。
- **操作**：subprocess 启动后读取 `os.Environ()`。
- **断言**：`AWS_ACCESS_KEY_ID` 不在子进程 env 内；仅 allowlist 字段可见。
- **关联单测**：`backend/internal/security/env_leak_test.go::TestSubprocessEnvAllowlist`
- **威胁 ID**：`T-I-04`。

---

## 7. 审批参数篡改（permission / question 字段）

### TM-18 Approval field tampering
- **前置条件**：approver 收到 `permission_request {tool:"git.push", args:{branch:"main", diff:"..."}}`。
- **操作**：approver 在 UI 把 `branch` 改为 `master`，签第二个签名后提交。
- **断言**：ZAG / RedClaw 用 canonical JSON 重算 hash，发现字段不一致 → 拒绝签名。
- **关联单测**：`backend/internal/security/approval_tamper_test.go::TestApprovalCanonicalMismatchRejected`
- **威胁 ID**：`T-T-03`。

### TM-19 Approval reply decision confusion
- **前置条件**：approver 想 deny；客户端篡改为 `allow_always`。
- **操作**：发送 `"decision":"allow_always"`。
- **断言**：服务端验证 token scope 不含 `permission.approve.always`；返回 `403`。
- **关联单测**：`backend/internal/security/approval_tamper_test.go::TestDecisionScopeEscalationBlocked`
- **威胁 ID**：`T-E-02`。

---

## 8. 双签 signer independence

### TM-20 Two keys not same controller
- **前置条件**：ZAG 持有 signer1；admin signer2 由独立审批服务持有。
- **操作**：构造 payload；用 signer1 + signer2 签名（两者归同一用户/同一设备）。
- **断言**：签名验证通过；但 signer_fingerprint 与 initiator_fingerprint 一致 → 拒绝执行。
- **关联单测**：`backend/internal/security/signer_independence_test.go::TestSignerIndependenceRejectsSameController`
- **威胁 ID**：`T-S-01`（关联）、`T-T-03`。

### TM-21 Replay of one signature
- **前置条件**：双签 command_id=C，nonce=N1 已执行。
- **操作**：用相同 payload + 相同 nonce N1 + 相同两个签名再次提交。
- **断言**：`409 control_already_consumed`；一次性消费表已落 record。
- **关联单测**：`backend/internal/security/signer_independence_test.go::TestDualSignatureReplayRejected`
- **威胁 ID**：`T-S-03`、`T-T-03`。

---

## 9. SSE / WS 订阅 ACL（订阅应与初始 auth 一致）

### TM-22 SSE subscribe tenant isolation
- **前置条件**：tenant A 的订阅连接；中途 path 中出现 `task_id` 属于 tenant B。
- **操作**：客户端伪造 path 或注入 subscribe message。
- **断言**：服务端逐对象授权；发现 resource.tenant != principal.tenant → 关闭连接 + 落 deny。
- **关联单测**：`backend/internal/security/sse_acl_test.go::TestSSESubscribeTenantIsolation`
- **威胁 ID**：`T-I-02`。

### TM-23 WS ticket reuse after token rotation
- **前置条件**：client 拿到短期 ws_ticket（TTL=60s）。
- **操作**：原 token 已 revoke；继续用 ws_ticket 连。
- **断言**：连接被拒（ws_ticket 引用已撤销的 token）。
- **关联单测**：`backend/internal/security/sse_acl_test.go::TestWSTicketBoundToToken`
- **威胁 ID**：`T-S-01`。

### TM-24 Slow consumer eviction
- **前置条件**：订阅方 buffer=128，30 秒不读。
- **操作**：服务端持续推送事件，客户端不读。
- **断言**：服务端检测到慢消费者 → 关闭连接 + emit `subscriber_dropped` audit。
- **关联单测**：`backend/internal/security/sse_acl_test.go::TestHubDropsSlowConsumer`
- **威胁 ID**：`T-D-02`。

---

## 10. 审计 outbox 恢复（kill -9 后能否恢复未消费事件）

### TM-25 Outbox survives crash mid-flush
- **前置条件**：写入 5 条 audit 事件到 outbox，正在 flush 第 3 条时 `kill -9` 进程。
- **操作**：进程重启；扫描 outbox。
- **断言**：第 1~2 条已落 WORM，第 3~5 条重发；WORM 内 `event_id` 不重复。
- **关联单测**：`backend/internal/security/audit_recovery_test.go::TestAuditOutboxRecoveryAfterKill`
- **威胁 ID**：`T-T-01`、`T-T-02`。

### TM-26 Audit sink unavailable fail-closed
- **前置条件**：审计 sink 模拟不可写。
- **操作**：发起高危写请求（如 `task.cancel`）。
- **断言**：handler 返回 `503 audit_unavailable`；业务状态未变更。
- **关联单测**：`backend/internal/security/audit_recovery_test.go::TestAuditFailClosedOnSinkError`
- **威胁 ID**：`T-T-01`、`T-D-01`。

### TM-27 Outbox sequence monotonicity
- **前置条件**：同一 tenant 持续 100 次 audit 写入。
- **操作**：读取后落 WORM，按 tenant 排序 sequence。
- **断言**：sequence 严格递增；`prev_audit_hash` 与上一条 canonical 一致。
- **关联单测**：`backend/internal/security/audit_recovery_test.go::TestAuditSequenceMonotonic`
- **威胁 ID**：`T-T-02`、`T-R-01`。

---

## 11. 覆盖率统计

| 类别 | 测试条数 |
|---|---:|
| 跨租户访问 | 2 |
| 重放攻击 | 3 |
| 越权（RBAC/ABAC/scope） | 3 |
| 路径逃逸 | 3 |
| SSRF | 3 |
| 环境变量泄漏 | 3 |
| 审批参数篡改 | 2 |
| 双签 signer independence | 2 |
| SSE/WS 订阅 ACL | 3 |
| 审计 outbox 恢复 | 3 |
| **合计** | **27** |

每条测试都引用了对应的 STRIDE 威胁 ID（见 `threat-model.md`），且 ADR 关联在每条用例描述中给出。
