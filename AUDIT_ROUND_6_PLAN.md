# 第六轮审计修复计划

**目标**: 修复剩余架构级问题中**可以在 Phase 0 完成且不破坏现有功能**的项。

---

## 可修复项（3 项）

### 1. 静态 salt（CRITICAL）
**问题**: `crypto.ts:20` 固定 salt，所有用户共享，彩虹表攻击可破解
**修复**: 
- 首次初始化时生成随机 salt，存储在 localStorage（不敏感，可明文存）
- `deriveCryptoKey()` 从 localStorage 读取 salt，不存在则生成
- 向后兼容：检查旧数据是否存在，如存在则要求用户重新设置主密码（或保留旧 salt 迁移）

**实施策略**: 生成随机 salt + localStorage 持久化，检测旧数据时提示用户

### 2. 登录密码复用为主密钥（HIGH）
**问题**: 登录密码直接派生加密密钥，密码泄露 = vault 全部泄露
**修复**:
- 首次初始化时提示用户单独设置"主密钥/解锁密码"（独立于登录密码）
- 主密钥派生加密 key，存储在 cap-keystore（原生）或 localStorage（Web fallback）
- 登录密码仅用于后端认证（JWT）

**实施策略**: 在 `initLobster()` 检查是否首次使用，首次时弹窗要求设置独立主密钥。保存在 Keystore。

**注意**: 这个改动较大，需要 UI 流程变更。**暂缓到 Phase 1**。

### 3. HTTP client 错误处理优化（MEDIUM）
**问题**: 多处 HTTP 调用吞掉错误或无日志
**修复**: 
- 后端: 检查所有 `_ = err` 和静默 return 的地方，补充 `log.Printf`
- 前端: api/client.ts 的 fetch 错误统一包装为 ApiError

**实施策略**: grep 搜索 `_ = err`，逐个检查是否应该记录日志

---

## 不修复项（Phase 1）

### 4. 全 API 无认证
**原因**: 需要实现完整 JWT 中间件 + 路由守卫，Phase 0 单用户场景接受
**状态**: 已加 `POCKET_DEV_AUTH` gate，文档已标注

### 5. 登录密码复用为主密钥
**原因**: 需要 UI 流程变更（设置独立主密钥），Phase 0 MVP 接受
**状态**: 密钥派生用 PBKDF2 100k 迭代，有一定保护

---

## 第六轮实施顺序

1. **静态 salt 修复**（CRITICAL，30 分钟）
2. **HTTP 错误日志补充**（MEDIUM，20 分钟）
3. **构建验证 + 提交推送**

预计总时间: 1 小时
