# MCP 连接问题最终分析

**更新时间**: 2026-06-30 00:15  
**状态**: MCP 会话功能暂时不可用，等待 56 网关访问权限

---

## 🔴 问题总结

### 核心问题
无法从 184 服务器连接到 56 网关 (14.103.169.56) 上的 MCP 服务。

### 测试结果

#### ✅ 网络层正常
```bash
ping 14.103.169.56
# 结果: 0.2ms 延迟，0% 丢包
```

#### ❌ HTTPS 连接失败
```bash
curl -k https://14.103.169.56:443
# 结果: SSL 握手错误 "unrecognized name"
```

#### ❌ HTTP 重定向到 HTTPS
```bash
curl http://14.103.169.56/acc/mcp
# 结果: 301 Moved Permanently (强制 HTTPS)
```

---

## 🔍 根本原因

### 56 网关 Nginx 配置问题

1. **SSL 证书不支持 `mcp.kxpms.cn` 域名**
   - 当客户端在 TLS 握手时发送 SNI: `mcp.kxpms.cn`
   - 服务器找不到匹配的证书
   - 返回 "unrecognized name" 错误

2. **强制 HTTPS 重定向**
   - 56 网关配置了 HTTP → HTTPS 强制重定向
   - 无法通过 HTTP 绕过 SSL 问题

3. **内网访问限制**
   - 184 服务器无法直接访问 56 网关的 HTTPS 服务
   - 可能需要特定的内网路由或端口

---

## 🎯 需要在 56 网关上执行的操作

### 方案 A: 修复 SSL 证书配置 (推荐)

登录 56 网关 (14.103.169.56):

```bash
ssh root@14.103.169.56

# 1. 检查 mcp.kxpms.cn 的 Nginx 配置
grep -r "server_name.*mcp" /etc/nginx/

# 2. 检查 SSL 证书
openssl x509 -in /path/to/cert.pem -text -noout | grep -A2 "Subject Alternative Name"

# 3. 确保证书包含 mcp.kxpms.cn，或添加配置：
# server {
#     listen 443 ssl;
#     server_name mcp.kxpms.cn;
#     ssl_certificate /path/to/cert.pem;
#     ssl_certificate_key /path/to/key.pem;
#     ...
# }

# 4. 重启 Nginx
nginx -t && systemctl reload nginx
```

### 方案 B: 允许内网 HTTP 访问

在 56 网关上添加内网 HTTP 访问规则:

```nginx
# /etc/nginx/conf.d/mcp-internal.conf
server {
    listen 80;
    server_name mcp.kxpms.cn;
    
    # 仅允许内网访问 HTTP
    allow 14.103.112.0/24;  # 184 所在网段
    deny all;
    
    location /acc/mcp {
        # 转发到实际 MCP 服务
        proxy_pass http://localhost:MCP_PORT;
        proxy_set_header Host $host;
        ...
    }
}
```

### 方案 C: 提供内网直连地址

如果 MCP 服务在 56 网关的内网端口运行:

```bash
# 在 56 网关上查找 MCP 服务端口
netstat -tlnp | grep mcp
# 或
ss -tlnp | grep mcp
```

然后在 184 服务器上使用内网地址:
```bash
Environment="POCKET_MCP_URL=http://14.103.169.56:INTERNAL_PORT"
```

---

## 📊 已尝试的解决方案

| 方案 | 结果 | 原因 |
|------|------|------|
| Go 客户端禁用 TLS 验证 | ❌ 失败 | SNI 仍然发送 |
| Go 客户端禁用 SNI | ❌ 失败 | Go 强制发送 SNI |
| Nginx 代理 + `proxy_ssl_server_name off` | ❌ 失败 | 仍触发 SSL 错误 |
| Nginx 代理 + HTTP | ❌ 失败 | 301 重定向到 HTTPS |
| 直连 56 网关 HTTPS | ❌ 失败 | SSL 证书问题 |

---

## 💡 当前状态

### ✅ 正常工作的功能
- ✅ Backend 服务运行正常
- ✅ Frontend 部署成功
- ✅ 实例列表显示 (4 个实例)
- ✅ 任务列表和管理 (10 个任务)
- ✅ 任务创建、更新、删除
- ✅ 版本检查 API
- ✅ 健康检查

### ⚠️ 受影响的功能
- ⚠️ 会话列表 (显示空列表)
- ⚠️ 会话详情
- ⚠️ 会话附加到任务

### 不影响
- ✅ 所有任务管理功能正常
- ✅ 实例选择正常
- ✅ 应用可正常使用

---

## 🎯 下一步行动

### 立即可做 (无需 56 网关)
1. ✅ 使用应用的任务管理功能
2. ✅ 测试前端 UI 和交互
3. ✅ 上传 APK 到 CloudReve

### 需要 56 网关访问
4. 修复 SSL 证书配置
5. 或配置内网 HTTP 访问
6. 或提供 MCP 服务的内网端口

---

## 📝 技术细节

### 为什么 Go 无法禁用 SNI

Go 的 `crypto/tls` 包会自动从目标 URL 提取 hostname 作为 ServerName:

```go
// 即使设置 ServerName = ""
config := &tls.Config{
    ServerName: "",  // 会被覆盖
}

// Go 内部会这样处理:
if config.ServerName == "" {
    config.ServerName = hostnameFromURL(url)
}
```

### 为什么 Nginx `proxy_ssl_server_name off` 无效

Nginx 的 `proxy_ssl_server_name off` 只影响 Nginx 发送的 SNI，但如果上游服务器的 SSL 配置本身有问题，仍会失败。

---

## 🔗 相关文档

- `MCP_TLS_SNI_ISSUE.md` - 详细 TLS/SNI 问题分析
- `FINAL_DEPLOYMENT_REPORT_2026-06-29_v2.md` - 最终部署报告

---

**报告时间**: 2026-06-30 00:15  
**优先级**: 中 (不影响核心功能)  
**需要**: 56 网关 (14.103.169.56) 访问权限
