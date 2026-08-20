# RECLAW-CHANGE-REQUEST-001：OpenCode Pocket 移动端平台集成

> **状态**：变更需求（非已实现功能）  
> **提出方**：ReClaw Platform / RedClaw Shell  
> **日期**：2026-08-14  
> **关联仓库**：`RedClaw`、`openpocket`  
> **优先级**：P1 移动端契约；P0 认证与租户隔离不得绕过

## 1. 产品边界

OpenCode Pocket 是 ReClaw Platform 的移动客户端，不是新的工作流编排或 memory SSOT。

- **RedClaw Shell**：统一 Web/产品入口与 API façade。
- **ACC**：任务、审批、状态机和协作控制面。
- **Memora**：记忆/知识平面。
- **LLM Gateway**：模型路由与会话推理平面。
- **OpenCode Pocket**：Android/iOS 移动体验、离线展示和移动能力适配。

移动端不得复制 ACC 编排状态机、Memora 隔离规则或服务端权限判定。

## 2. 认证和请求上下文

所有移动 API 请求必须使用服务端验证的认证机制，并携带/派生以下上下文：

```json
{
  "tenant_id": "tenant-123",
  "user_id": "user-456",
  "project_id": "project-789",
  "session_id": "uuid",
  "request_id": "uuid",
  "client": {"platform": "ios|android", "version": "..."}
}
```

要求：

1. tenant/user/project 由可信 token 或网关声明推导，客户端 header 不能单独决定授权范围。
2. token 过期、错误 audience、设备解绑、跨 tenant/project 和重放请求必须被拒绝。
3. 移动日志、崩溃报告和离线缓存不得记录 access token、完整 memory 正文或敏感任务附件。
4. 离线数据采用租户/用户绑定缓存命名，并在登出、切租户、权限撤销时安全清理。

## 3. 任务与会话同步契约

OpenCode Pocket 需要通过版本化共享 API 消费以下资源：

| 场景 | API/事件需求 | 关键验收 |
|---|---|---|
| 任务看板 | 查询任务、状态增量、审批动作 | 状态版本单调；重复/乱序事件无副作用 |
| 工作流详情 | workflow run/node 只读视图与授权操作 | 不直接绕过 ACC transition guard |
| 会话同步 | session 消息、流式状态、断线续连 token | 恢复后不重复展示/发送消息 |
| 通知 | 任务待审批、失败、完成、配额与安全通知 | 通知只含最小必要元数据，点击后重新鉴权 |
| 记忆检索 | 经 RedClaw/gateway 的受控查询 | 不直接使用可伪造 `X-User-Id` 调 Memora |

每个同步事件建议包括：`schema_version`、`event_id`、`occurred_at`、`tenant_id`、`project_id`、`resource_id`、`state_version`、`correlation_id`。

## 4. Mock-first 联调

在真实 ACC、Memora 和 gateway 契约冻结前，OpenCode Pocket 应支持可注入 mock provider：

1. mock 登录/刷新/撤销 token；
2. mock 任务列表、幂等审批、状态推送和断线恢复；
3. mock workflow 状态与失败/DLQ 可见性；
4. mock memory 查询的空结果、权限拒绝、限流和超时；
5. mock push 通知去重与 deep link 授权检查。

mock 数据必须包含两个 tenant、两个 user、两个 project，以验证 UI 与离线缓存不泄漏跨域数据。

## 5. 验收条件

- [ ] 发布版本化 API/事件 schema 与能力发现响应。
- [ ] Android/iOS 均通过跨 tenant、跨 user、跨 project 的正反向隔离测试。
- [ ] 网络中断、重连、重复推送、乱序状态和 token 刷新均有可重复测试。
- [ ] 移动端不直接调用 Memora 内部接口、不维护独立的工作流状态机。
- [ ] 真实环境替换前，mock 合同测试通过并将证据链接回本需求。

## 6. RedClaw mock 适配状态

RedClaw 当前未将 OpenCode Pocket 接入真实本地 provider stack。本需求用于冻结移动接入边界；真实替换以共享 API、认证方案和 staging 合同测试完成为前置条件。
