# 端口调整方案

**时间**: 2026-06-30 00:22  
**目标**: 调整 OpenCode Pocket 端口到 9010+ 段，避免与现有服务冲突

---

## 📊 当前端口占用情况

### 8000-9099 段占用端口
```
8080 - python (某服务)
8081 - python3 (本地)
8082 - python (某服务)
8088 - pocketd (OpenCode Pocket Backend) ← 需要调整
8089 - nginx (OpenCode Pocket Frontend) ← 需要调整
8090 - python3 (某服务)
9001 - uvicorn (某服务)
9058 - nginx (某服务)
9090 - nginx (MCP 代理，本地) ← 需要调整
9091 - csagent-worker (某服务)
```

### 9010-9019 段
✅ **完全空闲，可用**

---

## 🎯 新端口分配方案

| 服务 | 当前端口 | 新端口 | 说明 |
|------|----------|--------|------|
| **Backend** | 8088 | **9010** | pocketd 主服务 |
| **Frontend (Nginx)** | 8089 | **9011** | Web 界面访问 |
| **MCP 代理 (Nginx)** | 9090 | **9012** | 内网 MCP 代理 |

---

## 📝 需要修改的配置文件

### 1. Backend Systemd Service
**文件**: `/etc/systemd/system/opencode-pocket.service`
```diff
- Environment="POCKET_HTTP_PORT=8088"
+ Environment="POCKET_HTTP_PORT=9010"
```

### 2. Frontend Nginx 配置
**文件**: `/etc/nginx/conf.d/pocket.kxpms.cn.conf`
```diff
- listen 8089;
+ listen 9011;

  location /api/ {
-     proxy_pass http://127.0.0.1:8088;
+     proxy_pass http://127.0.0.1:9010;
  }
  
  location /healthz {
-     proxy_pass http://127.0.0.1:8088;
+     proxy_pass http://127.0.0.1:9010;
  }
```

### 3. MCP 代理 Nginx 配置
**文件**: `/etc/nginx/conf.d/mcp-local-proxy.conf`
```diff
- listen 127.0.0.1:9090;
+ listen 127.0.0.1:9012;
```

### 4. Backend MCP URL (如果使用本地代理)
**文件**: `/etc/systemd/system/opencode-pocket.service`
```diff
- Environment="POCKET_MCP_URL=http://127.0.0.1:9090/acc/mcp"
+ Environment="POCKET_MCP_URL=http://127.0.0.1:9012/acc/mcp"
```

---

## 🔄 迁移步骤

### 步骤 1: 更新 Backend 配置
```bash
# 1. 修改 systemd service
vim /etc/systemd/system/opencode-pocket.service
# 修改 POCKET_HTTP_PORT=9010

# 2. 重载并重启
systemctl daemon-reload
systemctl restart opencode-pocket

# 3. 验证
systemctl status opencode-pocket
curl http://localhost:9010/healthz
```

### 步骤 2: 更新 Nginx 配置
```bash
# 1. 修改 Frontend 配置
vim /etc/nginx/conf.d/pocket.kxpms.cn.conf
# 修改 listen 9011
# 修改 proxy_pass http://127.0.0.1:9010

# 2. 修改 MCP 代理配置
vim /etc/nginx/conf.d/mcp-local-proxy.conf
# 修改 listen 127.0.0.1:9012

# 3. 测试并重载
nginx -t
systemctl reload nginx

# 4. 验证
curl http://localhost:9011/
curl http://localhost:9011/api/instances
```

### 步骤 3: 验证所有功能
```bash
# 健康检查
curl http://localhost:9011/healthz

# API 测试
curl http://localhost:9011/api/instances
curl http://localhost:9011/api/tasks

# 外部访问
curl http://14.103.112.184:9011/
```

---

## 🌐 新访问地址

### 内部访问
- Backend: http://localhost:9010
- Frontend: http://localhost:9011
- MCP 代理: http://localhost:9012/acc/mcp

### 外部访问
- **Web 应用**: http://14.103.112.184:9011

---

## ⚠️ 注意事项

1. **无需停机**: 可以在新端口启动后再关闭旧端口
2. **防火墙**: 确保 9011 端口对外开放（如有防火墙）
3. **浏览器缓存**: 用户需要使用新地址访问
4. **文档更新**: 所有文档中的端口需要更新

---

## 📊 迁移后端口使用

```
9010 - pocketd (OpenCode Pocket Backend)
9011 - nginx (OpenCode Pocket Frontend)
9012 - nginx (MCP Local Proxy)
9013-9019 - 预留给 OpenCode Pocket 扩展
```

---

## ✅ 验证清单

- [ ] Backend 在 9010 端口启动
- [ ] Backend 健康检查通过
- [ ] Frontend 在 9011 端口可访问
- [ ] API 代理正常工作
- [ ] 实例列表正常
- [ ] 任务列表正常
- [ ] 外部可访问 9011
- [ ] 停止旧服务
- [ ] 更新文档

---

**准备时间**: 2026-06-30 00:22  
**预计执行时间**: 5 分钟  
**停机时间**: 无（无缝切换）
