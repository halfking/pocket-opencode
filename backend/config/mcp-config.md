# MCP Configuration for AI Work Mess

## ACC MCP Server
- **Public URL**: https://mcp.kxpms.cn/acc/mcp
- **Internal URL**: http://agent-control-center:4100/mcp

## API Key
**Key ID**: 17
**Key Name**: opencode-pocket-mcp
**API Key**: sk-mcp-<REDACTED-ROTATE-ME>
**Expires**: 2027-06-29

> ⚠️ **这把 key 已于 2026-10-02 从本文件移除，且必须视为已泄漏。**
>
> 它自 2026-06-29 起就以明文提交在本仓库（含 git 历史与 origin 远端），
> 2026-10-02 由新增的密钥卡口 `backend/internal/repohygiene/secrets_test.go` 扫出。
> 删掉字面量**不等于**作废——`git log -S` 仍可取回。唯一有效的处置是**轮换/吊销**。
>
> 换新 key 后请通过环境变量注入，**不要**再写回任何受跟踪文件：
> `POCKET_MCP_API_KEY=<新 key>`（生产 184 的注入位置见部署脚本）。
**Description**: AI Work Mess MCP client access

## Environment Variables

### Production (184 Server)
```bash
export POCKET_MCP_BASE_URL=https://mcp.kxpms.cn/acc/mcp
export POCKET_MCP_API_KEY=sk-mcp-<REDACTED-ROTATE-ME>
export POCKET_MCP_TENANT_ID=<tenant_id>
```

> ⚠️ 开关是 `POCKET_MCP_BASE_URL`（**非空即启用**），不是 `POCKET_MCP_ENABLED`
> / `POCKET_MCP_URL` —— 这两个名字**全仓没有任何代码读**，照着写等于没配。
> 三项缺一不可（`backend/cmd/pocketd/main.go:599-607`）：
> · `POCKET_MCP_BASE_URL` 空 ⇒ 不启用 ACC MCP client；
> · `POCKET_MCP_TENANT_ID` 空 ⇒ 生产环境**启动即报错**
>   （`backend/internal/config/config.go:498`）；
> · `POCKET_MCP_API_KEY` 空 ⇒ 客户端不启用并打 WARN（不报错）。

### Local Development
```bash
# 不设 POCKET_MCP_BASE_URL 即不启用 ACC MCP client（本地开发默认走 HTTP 适配）
```

## Usage

Backend will automatically use MCP adapter when `POCKET_MCP_BASE_URL` is set
（并配齐 `POCKET_MCP_TENANT_ID` 与 `POCKET_MCP_API_KEY`）。

The MCP adapter connects to ACC server and provides:
- acc_get_tasks - List tasks
- acc_list_sessions - List sessions
- acc_create_task - Create a task
- acc_task_claim - Claim a task
- acc_task_complete - Complete a task
- acc_report_session - Report a session

> ⚠️ 上面这份 tool 名单取自 `backend/internal/mcp/client.go:45-50` 的常量
> （`Capabilities()` 同源）。**不是** `session.search` / `session.create` /
> `session.append` / `session.get` —— 那四个名字 ACC 侧没有注册，
> 照着调只会拿到「unknown tool」。
