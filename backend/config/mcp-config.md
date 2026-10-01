# MCP Configuration for OpenCode Pocket

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
**Description**: OpenCode Pocket MCP client access

## Environment Variables

### Production (184 Server)
```bash
export POCKET_MCP_ENABLED=true
export POCKET_MCP_URL=https://mcp.kxpms.cn/acc/mcp
export POCKET_MCP_API_KEY=sk-mcp-<REDACTED-ROTATE-ME>
```

### Local Development
```bash
export POCKET_MCP_ENABLED=false  # Use HTTP adapter for local testing
```

## Usage

Backend will automatically use MCP adapter when `POCKET_MCP_ENABLED=true`.

The MCP adapter connects to ACC server and provides:
- session.search - Search sessions
- session.create - Create new session
- session.append - Add messages to session
- session.get - Get session details
