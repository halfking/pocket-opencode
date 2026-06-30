# MCP TLS/SNI 连接问题分析和解决方案

**问题时间**: 2026-06-29 22:23  
**问题**: Backend 无法连接到 MCP Server (https://mcp.kxpms.cn/acc/mcp)

---

## 🔴 问题现象

### 错误信息
```
failed to search sessions: session.search failed: 
failed to send HTTP request: Post "https://mcp.kxpms.cn/acc/mcp": 
remote error: tls: unrecognized name
```

### 测试结果

#### 从 184 服务器测试
```bash
curl -k -v -X POST https://mcp.kxpms.cn/acc/mcp \
  -H 'Authorization: Bearer sk-mcp-sa0cXjxzPKhU77CYNFiFDP1I7B4wMnBc' \
  -d '{"jsonrpc":"2.0","method":"session.search","params":{"query":"","limit":5},"id":1}'

# 结果：
OpenSSL/3.0.13: error:0A000458:SSL routines::tlsv1 unrecognized name
```

#### 使用 IP + Host header 测试
```bash
curl -k -X POST https://14.103.169.56/acc/mcp \
  -H 'Host: mcp.kxpms.cn' \
  -H 'Authorization: Bearer sk-mcp-sa0cXjxzPKhU77CYNFiFDP1I7B4wMnBc' \
  ...

# 结果：同样的 TLS SNI 错误
```

---

## 🔍 根本原因分析

### TLS SNI (Server Name Indication) 问题

1. **mcp.kxpms.cn 解析到**: 14.103.169.56 (56 网关服务器)
2. **56 网关的 SSL 证书**: 可能不支持 `mcp.kxpms.cn` 这个域名
3. **SNI 行为**: 
   - 客户端在 TLS 握手时发送 `ServerName: mcp.kxpms.cn`
   - 服务器收到后查找对应的证书
   - 如果证书不匹配或不支持该域名，返回 "unrecognized name" 错误

### 已尝试的解决方案

#### ✅ 方案 1: 跳过 TLS 验证
```go
TLSClientConfig: &tls.Config{
    InsecureSkipVerify: true,
}
```
**结果**: 失败，仍然报 SNI 错误

#### ✅ 方案 2: 禁用 SNI
```go
TLSClientConfig: &tls.Config{
    InsecureSkipVerify: true,
    ServerName:         "", // 空字符串仍会发送 SNI
}
```
**结果**: 失败，Go 的 TLS 库会自动从 URL 提取 ServerName

---

## 💡 可行的解决方案

### 方案 A: 修复 56 网关 Nginx 配置 (推荐)

在 56 网关服务器上检查并修复 Nginx SSL 配置：

```bash
# 1. 检查当前配置
ssh root@14.103.169.56
grep -r "server_name.*mcp" /etc/nginx/conf.d/

# 2. 确保 SSL 证书包含 mcp.kxpms.cn
# 或添加 SNI 配置

# 3. 重启 Nginx
systemctl restart nginx
```

### 方案 B: 使用内网 IP 直连 (临时方案)

如果 MCP Server 在内网可访问：

```bash
# 更新 systemd service
Environment="POCKET_MCP_URL=http://内网IP:端口/acc/mcp"

# 重启服务
systemctl restart opencode-pocket
```

### 方案 C: 添加 HTTP 转发代理

在 184 本地创建一个 HTTP 代理转发到 MCP：

```nginx
# /etc/nginx/conf.d/mcp-proxy.conf
server {
    listen 127.0.0.1:9090;
    server_name localhost;
    
    location /acc/mcp {
        proxy_pass https://mcp.kxpms.cn;
        proxy_ssl_verify off;
        proxy_ssl_server_name off;  # 关键：禁用 SNI
        proxy_set_header Host mcp.kxpms.cn;
        proxy_set_header Authorization $http_authorization;
    }
}
```

然后更新 Backend 配置：
```bash
Environment="POCKET_MCP_URL=http://127.0.0.1:9090/acc/mcp"
```

### 方案 D: 修改 Backend 使用原始 TCP 连接

创建自定义 DialContext 跳过 SNI：

```go
httpClient: &http.Client{
    Timeout: 30 * time.Second,
    Transport: &http.Transport{
        DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
            // 手动建立 TLS 连接，不发送 SNI
            conn, err := tls.Dial(network, addr, &tls.Config{
                InsecureSkipVerify: true,
                ServerName:         "", // 不会生效
            })
            return conn, err
        },
    },
}
```

---

## 📊 问题状态

| 项目 | 状态 |
|------|------|
| Backend TLS 配置 | ✅ 已禁用验证 |
| SNI 禁用尝试 | ❌ Go 强制发送 SNI |
| 56 网关 SSL 配置 | ⚠️ 待检查 |
| 临时解决方案 | ⏳ 待实施 |

---

## 🎯 推荐行动

### 立即可做
1. **检查 56 网关 Nginx 配置**
   - 登录 14.103.169.56
   - 查看 mcp.kxpms.cn 的 SSL 配置
   - 确认证书是否包含该域名

2. **实施方案 C: Nginx 本地代理** (最简单)
   - 在 184 上配置 Nginx 转发
   - 更新 Backend 环境变量
   - 无需修改代码

### 需要协调
3. **修复 56 网关 SSL 证书** (最优方案)
   - 需要访问 56 网关服务器
   - 可能需要重新签发证书

---

## 📝 技术细节

### Go TLS SNI 行为
Go 的 `crypto/tls` 包会自动从 URL 的 hostname 提取 ServerName：

```go
// 即使设置 ServerName = ""，Go 也会这样做：
if config.ServerName == "" {
    config.ServerName = hostnameFromURL(url)
}
```

### 完全禁用 SNI 的唯一方法
使用 `DialTLSContext` 手动建立连接，并在 TLS 握手前拦截。

---

## 🔗 相关资源

- **Go TLS 文档**: https://pkg.go.dev/crypto/tls
- **SNI 规范**: RFC 6066
- **OpenSSL SNI**: https://wiki.openssl.org/index.php/Server_Name_Indication

---

**报告时间**: 2026-06-29 22:25  
**优先级**: 高  
**建议方案**: 方案 C (Nginx 本地代理) - 最快实施
