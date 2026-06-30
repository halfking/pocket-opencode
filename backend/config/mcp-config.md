# MCP Configuration for OpenCode Pocket

## ACC MCP Server
- **Public URL**: https://mcp.kxpms.cn/acc/mcp
- **Internal URL**: http://agent-control-center:4100/mcp

## API Key
**Key ID**: 17
**Key Name**: opencode-pocket-mcp
**API Key**: sk-mcp-sa0cXjxzPKhU77CYNFiFDP1I7B4wMnBc
**Expires**: 2027-06-29
**Description**: OpenCode Pocket MCP client access

## Environment Variables

### Production (184 Server)
```bash
export POCKET_MCP_ENABLED=true
export POCKET_MCP_URL=https://mcp.kxpms.cn/acc/mcp
export POCKET_MCP_API_KEY=sk-mcp-sa0cXjxzPKhU77CYNFiFDP1I7B4wMnBc
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
