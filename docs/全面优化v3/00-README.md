# IT 人员 AI 工作平台 · 全面优化 v3

> 版本：v3.0（审计收敛版）  
> 编制日期：2026-08-28  
> 状态：**PLANNED（规划基线，非交付声明）**  
> 适用范围：openpocket、RedClaw、ACC、memora、llm-gateway-go、myvpn、navop、billd-desk 及拟建 AgentCompanion/IDE Bridge

## 0. 结论

本方案的目标是构建一套面向 IT 人员的个人与团队工作平台：

- **OpenPocket**：移动端用户入口，提供任务、审批、告警、知识问答、远程 IDE 状态和按需远程查看。
- **RedClaw**：统一 API façade 与 Web/桌面入口，负责身份、路由、兼容和聚合，不复制任务事实。
- **ACC**：任务、派发、审批、租约和审计的权威系统。
- **AgentCompanion**：拟建的每主机 daemon，负责本机智能体发现、心跳、会话代理和工具 allowlist；当前仓库中不存在该项目。
- **navop**：独立的桌面运维工作台，保留 Royal TSX 替代能力；通过其现有 Public MCP/CLI 作为受控工具源，不假设 headless 或源码嵌入。
- **memora**：记忆与知识权威，记录来源、版本、租户、项目和删除语义；当前 v2 ingest 仍有 501 缺口。
- **llm-gateway-go**：统一推理出口，当前调用以 HTTP 契约为事实基线。
- **myvpn/NetBird/Headscale/FRP**：网络可达性和隧道，不承担业务授权。
- **LiveKit（候选）**：后续 WebRTC 媒体平面；billd-desk 当前证据主要是客户端/外部信令，不能当作现成 SFU。

本版本优先复用当前真实能力，不再一次性引入虚构的 `navop-core-headless`、`navop-acp-server`、`Remote Gateway`、`Skill Gateway` 等未落地组件。

## 1. 状态标签

| 标签 | 含义 |
|---|---|
| `CURRENT` | 源码、启动入口或测试可直接证明已存在 |
| `PARTIAL` | 有实现但能力不完整、依赖未接线或仅部分路径可用 |
| `STUB` | 代码存在但返回占位、501 或没有真实后端 |
| `PLANNED` | 文档或计划提出，当前没有可用实现 |
| `TARGET` | 目标架构/验收结果，不代表已完成 |
| `RETIRED` | 已明确退役，不能作为新调用入口 |

任何路线图、架构图和总结必须使用这些标签，不用 `✅` 表示未来成果。

## 2. 文档索引

| 文档 | 用途 |
|---|---|
| [01-事实基线与审计报告](01-事实基线与审计报告.md) | 仓库事实、证据和 P0/P1/P2 问题 |
| [02-目标架构与模块边界](02-目标架构与模块边界.md) | 两端产品、组件 ownership 和依赖图 |
| [03-协议与可靠性契约](03-协议与可靠性契约.md) | Runtime Control、ACP、A2A、MCP、事件和恢复 |
| [04-远程主机与 IDE 工作流](04-远程主机与IDE工作流.md) | AgentCompanion、VSCode/Cursor、WebRTC 分阶段设计 |
| [05-记忆与知识治理](05-记忆与知识治理.md) | 文档、提示词、运行记录和 Memora 数据治理 |
| [06-开源项目选型与许可证矩阵](06-开源项目选型与许可证矩阵.md) | 官方资料、借鉴边界和许可证 |
| [07-分阶段实施路线图与验收矩阵](07-分阶段实施路线图与验收矩阵.md) | 可执行阶段、owner、验收证据 |

## 3. 历史方案

- `docs/全面优化v1/`：早期五平面和跨系统契约，保留作为历史审计记录。
- `docs/全面优化v2/`：曾提出六平面和产品化方案，但包含事实误述、协议冲突和未实现组件；**由本 v3 supersede，不作为实施基线**。

## 4. 总体架构

```text
OpenPocket / Web / CLI / 独立 Navop Desktop
             |
             | RedClaw façade: REST/OpenAPI commands + SSE durable events
             v
ACC canonical task / orchestration / approval / audit
             |
             | Runtime Control API（第一版；非自定义 ACP）
             v
AgentCompanion daemon（每台智能体主机）
       |                         |
       | 标准 ACP                 | MCP / typed tool bridge
       v                         v
Codex / Claude / OpenCode       navop Public MCP / IDE Bridge / OS tools

旁路：ACC -> Memora（HTTP v2 + service JWT）
      服务 -> llm-gateway-go（现有 HTTP 契约）
      OpenPocket/Web -> LiveKit（后续 WebRTC PoC）
      myvpn/NetBird/Headscale/FRP -> 网络可达性
```

## 5. 非目标

1. 本版本不把 navop 源码、GPUI、动态库或 WASM 嵌入 RedClaw/openpocket 的发行包。
2. 本版本不创建第二套“ACP”并用该名字表示工具 RPC。
3. 本版本不把 A2A 当作内部队列、工具调用或授权系统。
4. 本版本不在没有基准证据时承诺 `<2s`、`<500ms`、100 并发、99.9% 可用性。
5. 本版本不把 Memora 向量索引当作任务或审计事实源。
6. 本版本不把 billd-desk 当前客户端/外部信令代码描述成已部署的通用 SFU/TURN 服务。

## 6. 评审门禁

在任何代码实现或部署前，必须完成：

- ACC canonical task/approval API 的合同测试；
- AgentCompanion 仓库、owner、部署方式和密钥策略登记；
- ACP/A2A/MCP/Runtime Control 的术语和版本冻结；
- 命令面与 durable event 面的 schema、幂等、租约、取消和 replay 测试；
- navop Public MCP 的 loopback 访问模型和许可证边界确认；
- WebRTC 主机捕获、信令、媒体服务器和输入授权分层的 PoC；
- Memora ingest 不再 501，或明确将其作为当前限制。
