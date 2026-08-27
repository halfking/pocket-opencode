# 远程主机与 IDE 工作流

> 状态：`TARGET`；AgentCompanion、IDE Bridge 和媒体服务当前尚未形成独立仓库，均标为 `PLANNED`。  
> 目标：让 IT 人员在移动端查看和控制远程开发工作，同时保留 navop 的完整桌面运维能力。

## 1. 当前事实

| 能力 | 当前状态 | 证据 |
|---|---|---|
| OpenPocket ACP | `CURRENT/PARTIAL`：有 stdio、HTTP draft、WS transport；stdio 真实接入，HTTP 明确 future-proof | `openpocket/backend/internal/agent/{transport.go,transport_ws.go,transport_http.go,adapter_acp_stdio.go}` |
| navop 外部 Agent | `CURRENT`：navop 作为 ACP client 接入外部 coding agent | `navop/crates/ai_chat_view/src/acp/`、`navop/main/src/ai_chat_acp.rs` |
| navop 工具 | `CURRENT`：Public MCP 是 loopback 随机 TCP 端口 + 首行 token + rmcp；不是公网 HTTP API | `navop/crates/public_mcp/src/server.rs:12-25,97-114` |
| navop headless/ACP server | `PLANNED`：当前未发现实现 | navop workspace 无对应 crate/入口 |
| AgentCompanion | `PLANNED`：当前不存在独立项目 | workspace 搜索无 `agentcompanion` 目录 |
| IDE Bridge | `PLANNED`：当前不存在 | workspace 搜索无对应扩展/服务 |
| billd-desk | `PARTIAL`：已有浏览器/Electron WebRTC peer、Socket.IO 外部信令和 SRS 调用；不是现成通用 SFU | `billd-desk/src/utils/network/webRTC.ts`、`webSocket.ts`、`hooks/webrtc/remoteDesk.ts` |

## 2. AgentCompanion 最小职责

AgentCompanion 是每台智能体主机上的无界面 daemon，不负责跨主机调度。其职责：

1. 发现经本机 allowlist 允许的 coding agent 和 IDE bridge；
2. 向 ACC 进行注册、续租、注销和能力快照上报；
3. 与 ACC 建立出站 TLS/WS Runtime Control 连接，接收 dispatch；
4. 对 coding agent 使用标准 ACP（initialize/session/prompt/cancel/update/permission）；
5. 对 navop Public MCP、IDE Bridge 和其他本机工具使用 MCP 或 typed local tool bridge；
6. 将 progress、artifact、终态和错误回传 ACC；
7. 在断线或重启后用 lease/resume 语义恢复，不把“自动重连”误报为事件回放。

### 不负责

- 不选择其他主机；
- 不审批高危操作；
- 不直接修改 ACC 任务状态数据库；
- 不将主机凭据写入移动端；
- 不自动扫描并执行所有本机进程；发现结果必须受 allowlist、签名和配置约束。

## 3. 主机注册与租约

### 注册输入

```json
{
  "host_id": "host-dev-01",
  "agentcompanion_id": "ac-host-dev-01",
  "platform": "darwin-arm64",
  "runtime_version": "0.1.0",
  "capabilities": [
    {"id": "coding-agent.opencode", "version": "...", "scopes": ["prompt", "read"]},
    {"id": "navop.public-mcp", "version": "...", "scopes": ["terminal.read", "db.query"]}
  ],
  "labels": ["dev", "owner:alice"]
}
```

注册请求的 tenant、actor 和身份来源由已验证 token 派生，不接受 body 中的 tenant 覆盖。ACC 返回 `lease_id`、过期时间、连接版本和被允许的能力集合。

### 续租和失效

- 心跳间隔由 ACC 返回，默认不写死；
- lease 过期后，ACC 将主机标记 offline，未确认的 dispatch 进入可恢复/unknown 分支；
- AgentCompanion 重连必须携带 `resume_token` 和最近确认的事件序号；
- 同一 host 的新 lease 使用 fencing token，使旧连接无法继续执行命令。

## 4. IDE Bridge

### 第一阶段只读能力

| 能力 | 数据 | 默认 scope | 备注 |
|---|---|---|---|
| `ide.workspace.snapshot` | workspace、open editors、active editor | `ide.read` | 不上传完整源码 |
| `ide.diagnostics.list` | error/warning、文件和范围 | `ide.read` | 可按项目/文件过滤 |
| `ide.tasks.list` | task、最近状态、退出码 | `ide.read` | 输出截断并给 artifact 引用 |
| `ide.debug.snapshot` | session、thread、stack、breakpoint 摘要 | `ide.debug.read` | 变量值默认脱敏 |
| `ide.git.status` | branch、dirty、ahead/behind、摘要 diff | `repo.read` | 完整 diff 需单独 grant |
| `ide.terminal.summary` | 最近输出摘要、退出码 | `terminal.read` | 不默认上传完整 scrollback |

### 写操作

`ide.workspace.edit`、`ide.task.run`、`ide.debug.attach`、`terminal.exec` 均创建 ACC operation，并检查：

- 目标 workspace/project；
- capability version；
- required scopes；
- side-effect class；
- approval policy；
- `Idempotency-Key`；
- lease/fencing token；
- 超时、取消和未知结果规则。

### VSCode 与 Cursor

Cursor 基于 VS Code 生态，但不能直接假定所有 API 行为一致。第一阶段使用同一个 TypeScript 扩展，并建立独立兼容性矩阵：版本、桌面/远程扩展 host、diagnostics、tasks、debug、terminal、权限提示和性能。只有测试结果进入 `CURRENT`，否则保持 `PARTIAL`。

LSP 用于语言服务语义，DAP 用于调试会话，VS Code Extension API 用于工作区事件和 UI 适配；三者都不承担平台授权、任务持久化或审计。

## 5. navop 接入路径

### 当前可执行路径

```text
AgentCompanion（主机）
  -> 读取 navop discovery 文件
  -> 校验 loopback、PID、Token 和 mode
  -> 作为 navop Public MCP client 调用 tools/list/tools/call
  -> 将结果映射成 ACC operation artifact
```

Navop Public MCP 当前监听 IPv4 loopback 的随机 TCP 端口，discovery 文件包含 pid/host/port/token/mode；不能将该端口或 token 转发公网。若 Navop 未运行或 tool exposure 未开放，AgentCompanion 应返回 capability_unavailable，而不是猜测命令。

### 后续可选路径

若确实需要无 GUI 主机，另立 navop upstream change proposal，评估独立 `navop-agent-host` 或官方支持的 headless runtime。它必须与桌面 GUI、持久化状态、许可证和升级路径分别评审，不能在本方案中假设已经存在。

## 6. WebRTC 可视通道

### 推荐候选：LiveKit

LiveKit 只作为候选媒体层，负责 room、participant token、SFU、TURN、媒体/data track、断线和选择性订阅。它不负责屏幕捕获、键鼠注入、命令审批或审计。

```text
OpenPocket viewer -> LiveKit room/token -> Host capture adapter
                                     -> screen/audio/data track
                                     -> host input adapter（受 grant 控制）
```

### billd-desk 的使用边界

当前 billd-desk 可以作为 WebRTC peer/client 和外部 Socket.IO/SRS 信令的参考或兼容客户端，但当前仓库没有证据证明其提供可直接部署的通用 SFU。是否复用必须以独立 PoC 验证：信令协议、TURN、Android 兼容、屏幕捕获、输入控制、租户隔离、录制和许可证。

### 画面与结构化状态双轨

- 结构化 IDE 状态是移动端默认入口；
- WebRTC 画面用于人工接管、图形工具和适配失败回退；
- OCR 只作为局部、按需、明确授权的辅助，不作为代码事实源；
- 视频、截图、剪贴板、源码和终端输出均按敏感级别、保留期和脱敏策略处理。

## 7. 故障与恢复验收

必须覆盖：

1. AgentCompanion 进程崩溃后重新注册；
2. ACC 发送 dispatch 后主机断网；
3. 工具副作用完成但 ACK 丢失；
4. duplicate dispatch 和过期 fencing token；
5. IDE 扩展卸载/重启；
6. WebRTC 直连失败并切 TURN；
7. SSE cursor 过期后 snapshot fallback；
8. 移动端离线查看最近一次可信 snapshot；
9. 跨项目读取和敏感文件外发被拒绝；
10. 任何未确认的副作用显示 `unknown`，不得自动重复执行。
