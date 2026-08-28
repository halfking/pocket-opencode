# 交接文档：OpenCode Pocket 生物识别认证 + 跨模块集成需求

**交接时间**：2026-08-29 00:35  
**仓库**：opencode-pocket (official-deploy/services/opencode-pocket)  
**分支**：main @ `5d92589`  
**状态**：✅ 代码已提交推送，跨模块需求文档已编写完成  
**下一步**：在新会话中并行实施下游服务接口（RedClaw、ACC、Memora）

---

## 1. 本会话完成工作总览

### 1.1 OpenCode Pocket 生物识别登录（已完成）

**实施内容**（3 个 commit）：
1. **`fc002b1`** - feat(auth): 实现 WebAuthn 生物识别签名验证
   - 新增 `WebAuthnVerifier`（go-webauthn 封装）
   - 支持 BeginRegistration/FinishRegistration、BeginLogin/FinishLogin
   - COSE 公钥解析、签名验证、counter 单调性检查
   
2. **`1a82000`** - feat(biometric): 集成 RedClaw 用户验证到生物识别登录
   - `handleBiometricLoginFinish` 在签名验证后调用 `redclawBridge.VerifyUser`
   - Fail-closed：RedClaw 配置但不可用时拒绝登录（安全优先）
   - 降级：RedClaw 未配置时跳过验证（开发环境）
   
3. **`5d92589`** - feat(biometric): 启动装配 + 内存 fallback + RedClaw 集成测试
   - `main.go`：恢复 biometricStore 装配 + 注入 webAuthnVerifier
   - `config.go`：新增 `WebAuthnRP*` 三项配置（DisplayName/ID/Origin）
   - `BiometricStore`：pool == nil 时降级到内存 map（覆盖 6 个 CRUD）
   - 抽出 `WebAuthnVerifierIface` + `assertionParser` 测试 seam
   - 新增 `server_biometric_redclaw_test.go`（3 个集成测试）

**核心链路**（端到端）：
```
用户注册 → requireAuth(JWT) → WebAuthn Begin/Finish → 
   biometricStore.Register (PG/内存) → audit log → 200 OK

用户登录 → WebAuthn Begin/Finish (签名验证) → 
   biometricStore.Get → RedClaw VerifyUser (fail-closed) → 
   biometricStore.Touch (counter++) → jwtSigner.Sign → 200 + JWT
```

**测试覆盖**：
- ✅ `internal/auth`：5 个测试通过（含内存 fallback）
- ✅ `internal/server`：所有 biometric 测试通过（含 RedClaw 集成）
- ✅ `internal/redclaw`：所有测试通过

**部署配置**：
```bash
# WebAuthn 配置（三项齐全才启用签名验证）
POCKET_WEBAUTHN_RP_DISPLAY_NAME="OpenCode Pocket"
POCKET_WEBAUTHN_RP_ID="pocket.example.com"
POCKET_WEBAUTHN_RP_ORIGIN="https://pocket.example.com"

# RedClaw 连接配置
POCKET_REDCLAW_BASE_URL=http://redclaw-gateway:8092
POCKET_REDCLAW_SECRET=<shared-secret>
POCKET_REDCLAW_TENANT_ID=default
```

---

### 1.2 跨模块集成需求文档（已完成）

**文档位置**：`~/workspace/ai-native-tools/docs/来自其他模块/`

**已创建文档**（5 个，共 69KB）：

| 文档 | 大小 | 状态 | 优先级 |
|------|------|------|--------|
| [README.md](file:///Users/xutaohuang/workspace/ai-native-tools/docs/来自其他模块/README.md) | 13KB | 📋 总览 | - |
| [2026-08-29-opencode-pocket-redclaw-user-verification.md](file:///Users/xutaohuang/workspace/ai-native-tools/docs/来自其他模块/2026-08-29-opencode-pocket-redclaw-user-verification.md) | 11KB | ✅ Pocket 侧已实现 | P1 |
| [2026-08-29-opencode-pocket-acc-task-management.md](file:///Users/xutaohuang/workspace/ai-native-tools/docs/来自其他模块/2026-08-29-opencode-pocket-acc-task-management.md) | 13KB | 🟡 待实施 | P1 |
| [2026-08-29-opencode-pocket-memora-knowledge-retrieval.md](file:///Users/xutaohuang/workspace/ai-native-tools/docs/来自其他模块/2026-08-29-opencode-pocket-memora-knowledge-retrieval.md) | 16KB | 🟡 待实施 | P1 |
| [2026-08-29-opencode-pocket-agentcompanion-agent-status.md](file:///Users/xutaohuang/workspace/ai-native-tools/docs/来自其他模块/2026-08-29-opencode-pocket-agentcompanion-agent-status.md) | 16KB | 🔴 待规划 | P2 |

**需求矩阵**：

| 下游服务 | 核心接口 | 用途 | 优先级 | 实施阶段 |
|---------|---------|------|--------|---------|
| **RedClaw** | `POST /api/v1/users/verify` | 生物识别登录时验证用户有效性 | P1 | Phase 1 (2026-09) |
| **ACC** | `GET /api/v2/canonical/tasks` | 任务列表查询 | P1 | Phase 2 (2026-10) |
| **ACC** | `POST /api/v2/orchestration/approvals/{task_id}` | 移动端任务审批 | P1 | Phase 4 (2026-12) |
| **Memora** | `POST /api/v2/retrieval/search` | 知识问答（语义检索） | P1 | Phase 3 (2026-11) |
| **Memora** | `POST /api/v2/retrieval/recall` | 记忆召回（上下文增强） | P1 | Phase 3 (2026-11) |
| **AgentCompanion** | `GET /api/v2/orchestration/agents` | 代理状态查询 | P2 | Phase 5 (2027-Q1) |

**文档特点**：
- ✅ 完整的 HTTP 接口定义（请求/响应/错误码）
- ✅ 安全合规（认证、租户隔离、审计日志、敏感信息过滤）
- ✅ Pocket 侧集成设计（调用流程、UI 交互、离线降级）
- ✅ 验收标准（交付物清单、集成测试场景、端到端联调）
- ✅ 环境变量配置示例（上下游双方）

---

## 2. 当前状态快照

### 2.1 Git 状态

```
仓库：opencode-pocket (official-deploy/services/opencode-pocket)
分支：main
HEAD：5d92589 (feat(biometric): 启动装配 + 内存 fallback + RedClaw 集成测试)
远端：git@github.com:halfking/pocket-opencode.git
状态：clean（无未提交改动）
同步：与 origin/main 同步
```

**最近 3 次提交变更统计**：
```
17 files changed, 1536 insertions(+), 51 deletions(-)
- backend/internal/auth/biometric.go（内存 fallback）
- backend/internal/auth/webauthn_verifier.go（完整实现）
- backend/internal/server/server_biometric.go（RedClaw 集成）
- backend/internal/server/server_biometric_redclaw_test.go（新增）
- backend/cmd/pocketd/main.go（启动装配）
- backend/internal/config/config.go（WebAuthn RP 配置）
- docs/2026-08-28-biometric-auth-and-sqlite-fallback.md（架构文档）
```

### 2.2 代码库关键文件

**核心实现**：
- `backend/internal/auth/webauthn_verifier.go` (282 行) - WebAuthn 签名验证器
- `backend/internal/auth/biometric.go` (210 行) - 凭证存储（PG + 内存 fallback）
- `backend/internal/server/server_biometric.go` (460 行) - HTTP handlers
- `backend/internal/redclaw/client.go` (162 行) - RedClaw 客户端（含 VerifyUser）
- `backend/cmd/pocketd/main.go` (405 行) - 启动装配

**测试覆盖**：
- `backend/internal/auth/biometric_test.go` (52 行)
- `backend/internal/auth/webauthn_verifier_test.go` (227 行)
- `backend/internal/server/server_biometric_test.go` (91 行)
- `backend/internal/server/server_biometric_redclaw_test.go` (178 行，新增)
- `backend/internal/redclaw/client_test.go` (217 行)

**架构文档**：
- `docs/2026-08-28-biometric-auth-and-sqlite-fallback.md` (263 行)

### 2.3 依赖与环境

**Go 依赖**（关键新增）：
- `github.com/go-webauthn/webauthn` v0.11.2（WebAuthn 协议实现）
- 现有：`github.com/jackc/pgx/v5` (PostgreSQL)
- 现有：`github.com/golang-jwt/jwt/v5` (JWT)

**运行时依赖**：
- PostgreSQL（biometric_credentials 表，可选）
- RedClaw Gateway（用户验证，可选）
- 无 PG 时自动降级到内存存储

**环境变量**（新增）：
```bash
POCKET_WEBAUTHN_RP_DISPLAY_NAME=""  # 空=降级到 P0 stub
POCKET_WEBAUTHN_RP_ID=""
POCKET_WEBAUTHN_RP_ORIGIN=""
POCKET_REDCLAW_BASE_URL=""  # 空=跳过 RedClaw 验证
POCKET_REDCLAW_SECRET=""
```

---

## 3. 遗留问题与边界

### 3.1 已知阻塞点

**P1（生产阻塞）**：
1. ❌ **RedClaw 侧 `/api/v1/users/verify` 接口未实现**
   - Pocket 侧已实现调用逻辑（`server_biometric.go:290-318`）
   - RedClaw 需实现：租户隔离、用户状态查询、审计日志
   - 需求文档：[2026-08-29-opencode-pocket-redclaw-user-verification.md](file:///Users/xutaohuang/workspace/ai-native-tools/docs/来自其他模块/2026-08-29-opencode-pocket-redclaw-user-verification.md)

2. ❌ **ACC 任务查询接口未实现**
   - Pocket 移动端需要展示任务列表、任务详情、审批操作
   - 需求文档：[2026-08-29-opencode-pocket-acc-task-management.md](file:///Users/xutaohuang/workspace/ai-native-tools/docs/来自其他模块/2026-08-29-opencode-pocket-acc-task-management.md)

3. ❌ **Memora 知识检索接口未实现**
   - Pocket 移动端需要知识问答、记忆召回功能
   - 需求文档：[2026-08-29-opencode-pocket-memora-knowledge-retrieval.md](file:///Users/xutaohuang/workspace/ai-native-tools/docs/来自其他模块/2026-08-29-opencode-pocket-memora-knowledge-retrieval.md)

**P2（增强功能）**：
4. 🔴 **AgentCompanion 组件未实施**
   - 是拟建组件，当前无法查询代理状态
   - 需求文档：[2026-08-29-opencode-pocket-agentcompanion-agent-status.md](file:///Users/xutaohuang/workspace/ai-native-tools/docs/来自其他模块/2026-08-29-opencode-pocket-agentcompanion-agent-status.md)
   - 可延迟到 Phase 5（2027-Q1）

### 3.2 技术债务

1. **前端 WebAuthn 客户端缺失**
   - 当前仅实现了后端签名验证
   - 需要浏览器调用 `navigator.credentials.create/get`
   - 不在本仓库范围（前端独立项目）

2. **Redis challenge store 缺失**
   - 当前 challenge 存储在内存（单实例）
   - 多实例部署需要 Redis 共享 challenge
   - 可在 Phase 2 实施

3. **WebAuthn Transports 字段未完善**
   - 当前 `Transports` 字段为空字符串
   - 需要从 `ccr.Response.Transports` 提取
   - 不影响核心功能，可后续优化

### 3.3 设计决策记录

**为什么 BiometricStore 有内存 fallback？**
- 原因：无 PG 的单机部署（如本地开发）也能用生物识别
- 一致性：与 chatagent SQLite fallback 对齐
- 测试友好：单元测试无需 PG 依赖

**为什么 RedClaw 验证是 fail-closed？**
- 安全优先：RedClaw 不可用时宁可拒绝所有登录，也不绕过验证
- 降级路径：RedClaw 未配置（`redclawBridge == nil`）时跳过验证
- 审计完整：每次验证失败都记录审计日志

**为什么需要 assertionParser 测试 seam？**
- 工程实用：避免在测试中构造合法的 CBOR-encoded authenticatorData
- 接口清晰：生产用 `defaultAssertionParser`，测试可替换
- 不影响生产：`parseAssertionFn` 字段仅在测试时注入

---

## 4. 下一步行动计划

### 4.1 并行任务分解

**建议在新会话中同时启动 3 个并行任务**：

#### Task 1: RedClaw VerifyUser 接口实施（P1，阻塞生产）
- **仓库**：`~/workspace/ai-native-tools/RedClaw`
- **需求文档**：[2026-08-29-opencode-pocket-redclaw-user-verification.md](file:///Users/xutaohuang/workspace/ai-native-tools/docs/来自其他模块/2026-08-29-opencode-pocket-redclaw-user-verification.md)
- **交付物**：
  - [ ] 实现 `POST /api/v1/users/verify` 端点
  - [ ] 租户隔离校验（Bearer token + `X-Tenant-ID`）
  - [ ] 返回 `valid` + `user_info`（含 `roles`）
  - [ ] 审计日志记录
  - [ ] 单元测试（有效用户、无效用户、租户不匹配）
  - [ ] API 文档更新（Swagger）
- **联调验收**：与 Pocket 侧生物识别登录联调（用户禁用后拒绝登录）

#### Task 2: ACC 任务查询接口实施（P1，核心功能）
- **仓库**：`~/workspace/ai-native-tools/agent-control-center`
- **需求文档**：[2026-08-29-opencode-pocket-acc-task-management.md](file:///Users/xutaohuang/workspace/ai-native-tools/docs/来自其他模块/2026-08-29-opencode-pocket-acc-task-management.md)
- **交付物**：
  - [ ] 实现 `GET /api/v2/canonical/tasks` 端点（任务列表查询）
  - [ ] 实现 `GET /api/v2/canonical/tasks/{task_id}` 端点（任务详情）
  - [ ] 实现 `POST /api/v2/orchestration/approvals/{task_id}` 端点（审批）
  - [ ] 租户隔离 + 审批权限校验
  - [ ] 审计日志记录
  - [ ] 单元测试（权限、租户隔离、冲突检测）
  - [ ] API 文档更新
- **联调验收**：Pocket 移动端展示任务列表 + 审批操作

#### Task 3: Memora 知识检索接口实施（P1，核心功能）
- **仓库**：`~/workspace/ai-native-tools/memora`
- **需求文档**：[2026-08-29-opencode-pocket-memora-knowledge-retrieval.md](file:///Users/xutaohuang/workspace/ai-native-tools/docs/来自其他模块/2026-08-29-opencode-pocket-memora-knowledge-retrieval.md)
- **交付物**：
  - [ ] 实现 `POST /api/v2/retrieval/search` 端点（知识检索）
  - [ ] 实现 `POST /api/v2/retrieval/recall` 端点（记忆召回）
  - [ ] 实现 `POST /api/v2/retrieval/recommend` 端点（知识推荐）
  - [ ] 租户隔离 + 用户隔离（记忆不跨用户）
  - [ ] 敏感信息过滤（密码、API Key 脱敏）
  - [ ] 审计日志记录
  - [ ] 单元测试（租户隔离、用户隔离、过滤逻辑）
  - [ ] API 文档更新
- **联调验收**：Pocket 移动端知识问答 + 上下文召回

### 4.2 实施顺序建议

**Phase 1（2026-09）**：
1. RedClaw VerifyUser 实施（1 周）
2. Pocket + RedClaw 联调验收（2 天）
3. 生产部署验证（1 天）

**Phase 2（2026-10）**：
1. ACC 任务查询接口实施（2 周）
2. Pocket 移动端任务列表页开发（1 周）
3. 联调验收（3 天）

**Phase 3（2026-11）**：
1. Memora 知识检索接口实施（2 周）
2. Pocket 移动端知识问答功能开发（1 周）
3. 联调验收（3 天）

**Phase 4（2026-12）**：
1. ACC 审批接口实施（1 周）
2. Pocket 移动端审批功能开发（3 天）
3. 联调验收（2 天）

**Phase 5（2027-Q1）**：
1. AgentCompanion PoC（3 周）
2. ACC Runtime Control API 增强（1 周）
3. Pocket 移动端代理状态页开发（1 周）
4. 联调验收（3 天）

### 4.3 联调环境准备

**Docker 网络**：
- ✅ `shared-infra` 网络已就绪（参考 `docs/docker-network-centralization-2026-08-27.md`）
- ✅ 中心 PostgreSQL：`llm-gateway-pg` (172.18.0.2)
- ✅ 中心 Redis：`nbjl-redis` (172.18.0.3)

**服务启动顺序**：
1. 中心依赖（PG、Redis、MySQL）
2. RedClaw Gateway
3. ACC
4. Memora
5. OpenCode Pocket

**环境变量配置**：
```bash
# Pocket 侧
POCKET_REDCLAW_BASE_URL=http://redclaw-gateway:8092
POCKET_REDCLAW_SECRET=<共享密钥>
POCKET_WEBAUTHN_RP_DISPLAY_NAME="OpenCode Pocket"
POCKET_WEBAUTHN_RP_ID="pocket.example.com"
POCKET_WEBAUTHN_RP_ORIGIN="https://pocket.example.com"

# RedClaw 侧
REDCLAW_SHARED_SECRET=<共享密钥>

# ACC 侧
ACC_REDIS_URL=redis://nbjl-redis:6379

# Memora 侧
MEMORA_QDRANT_URL=http://qdrant:6333
MEMORA_REDIS_URL=redis://nbjl-redis:6379
```

---

## 5. 关键决策与风险

### 5.1 架构决策

**✅ 通过 RedClaw façade 统一路由**
- Pocket 不直接调用 ACC/Memora，通过 RedClaw Gateway 代理
- 优点：统一认证、租户隔离、流量控制
- 风险：RedClaw 成为单点故障（需高可用部署）

**✅ Fail-closed 安全策略**
- RedClaw 不可用时拒绝生物识别登录（不降级）
- 优点：安全优先，防止绕过用户验证
- 风险：RedClaw 故障会影响所有生物识别用户（需监控告警）

**✅ 离线投影而非复制状态**
- Pocket 可缓存任务列表、知识片段（只读），但写操作必须在线
- 优点：避免状态不一致、冲突处理复杂度
- 限制：离线时无法审批、无法提问（可接受）

### 5.2 风险与缓解

**风险 1：RedClaw VerifyUser 接口延迟**
- 影响：Pocket 生物识别登录无法验证用户有效性
- 缓解：Pocket 侧已实现降级逻辑（RedClaw 未配置时跳过验证）
- 时间窗口：开发环境可先用降级模式，生产部署前必须实施

**风险 2：下游服务接口设计冲突**
- 影响：需求文档中的接口路径/字段与现有 API 冲突
- 缓解：技术评审会提前确认（建议 2026-09-05 前完成）
- 回退方案：Pocket 侧适配下游实际接口（优先级低于下游修改）

**风险 3：并行开发的集成测试复杂度**
- 影响：3 个下游服务同时开发，联调窗口紧张
- 缓解：按 Phase 1→2→3 顺序联调，不要求同时完成
- 时间预留：每个 Phase 预留 2-3 天联调时间

---

## 6. 参考资料与链接

### 6.1 本仓库文档
- [docs/2026-08-28-biometric-auth-and-sqlite-fallback.md](file:///Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/docs/2026-08-28-biometric-auth-and-sqlite-fallback.md) - 生物识别认证架构设计
- [backend/internal/auth/webauthn_verifier.go:1-282](file:///Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend/internal/auth/webauthn_verifier.go) - WebAuthn 验证器实现
- [backend/internal/server/server_biometric.go:253-318](file:///Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend/internal/server/server_biometric.go) - 生物识别登录 handler（含 RedClaw 集成）

### 6.2 跨项目需求文档
- [ai-native-tools/docs/来自其他模块/README.md](file:///Users/xutaohuang/workspace/ai-native-tools/docs/来自其他模块/README.md) - 需求总览
- [2026-08-29-opencode-pocket-redclaw-user-verification.md](file:///Users/xutaohuang/workspace/ai-native-tools/docs/来自其他模块/2026-08-29-opencode-pocket-redclaw-user-verification.md) - RedClaw 用户验证接口需求
- [2026-08-29-opencode-pocket-acc-task-management.md](file:///Users/xutaohuang/workspace/ai-native-tools/docs/来自其他模块/2026-08-29-opencode-pocket-acc-task-management.md) - ACC 任务管理接口需求
- [2026-08-29-opencode-pocket-memora-knowledge-retrieval.md](file:///Users/xutaohuang/workspace/ai-native-tools/docs/来自其他模块/2026-08-29-opencode-pocket-memora-knowledge-retrieval.md) - Memora 知识检索接口需求

### 6.3 架构文档
- [ai-native-tools/docs/全面优化v3/02-目标架构与模块边界.md](file:///Users/xutaohuang/workspace/ai-native-tools/docs/全面优化v3/02-目标架构与模块边界.md) - v3 架构定义
- [ai-native-tools/docs/全面优化v3/03-协议与可靠性契约.md](file:///Users/xutaohuang/workspace/ai-native-tools/docs/全面优化v3/03-协议与可靠性契约.md) - 跨服务协议规范
- [ai-native-tools/docs/docker-network-centralization-2026-08-27.md](file:///Users/xutaohuang/workspace/ai-native-tools/docs/docker-network-centralization-2026-08-27.md) - Docker 网络集中化状态

### 6.4 Git 提交记录
- `fc002b1` - feat(auth): 实现 WebAuthn 生物识别签名验证
- `1a82000` - feat(biometric): 集成 RedClaw 用户验证到生物识别登录
- `5d92589` - feat(biometric): 启动装配 + 内存 fallback + RedClaw 集成测试

---

## 7. 元信息

**交接文档版本**：1.0  
**生成时间**：2026-08-29 00:35  
**会话 ID**：sess_* (当前会话)  
**负责人**：OpenCode Pocket 团队  
**下游协调**：RedClaw、ACC、Memora 团队  

**文档维护**：
- 下游服务实施进展 → 更新"当前状态"章节
- 联调验收通过 → 更新"遗留问题"章节
- Phase 实施完成 → 归档到项目 CHANGELOG

---

**EOF**
