# 前端 API 连接问题排查和解决

**时间**: 2026-06-30 01:26  
**问题**: 前端显示 "Failed to fetch"

---

## 🔍 问题分析

### 发现的硬编码问题

1. **client.ts** - ✅ 已修复
   ```typescript
   const API_BASE = import.meta.env.VITE_API_BASE || ""
   ```

2. **websocket.ts** - ✅ 已修复
   ```typescript
   const API_BASE = import.meta.env.VITE_API_BASE || ''
   ```

3. **version.ts** - ✅ 已修复
   ```typescript
   const API_BASE = import.meta.env.VITE_API_BASE || ''
   ```

4. **SettingsView.vue** - ✅ 已修复
   ```vue
   <div class="setting-value small">{{ window.location.origin }}</div>
   ```

5. **ServerSelectView.vue** - ℹ️ 保持不变
   - 这是服务器选择页面，显示的是外部服务器列表
   - 不影响 API 调用

---

## ✅ 修复内容

### 所有 API_BASE 使用相对路径

**原理**:
```
前端代码: fetch(`${API_BASE}/api/instances`)
API_BASE = "" (空字符串)
实际请求: fetch("/api/instances")
浏览器解析: http://14.103.112.184:9011/api/instances
Nginx 代理: → http://127.0.0.1:9010/api/instances (Backend)
```

---

## 🧪 验证步骤

### 1. 检查打包产物
```bash
cd frontend
cat dist/assets/index-*.js | grep -o 'localhost:8088' | wc -l
# 结果: 3 (只在服务器选择页面，不影响实际 API 调用)
```

### 2. 测试 API 端点
```bash
# Backend 健康检查
curl http://14.103.112.184:9011/healthz
# 预期: ok

# 实例列表
curl http://14.103.112.184:9011/api/instances
# 预期: JSON 数据

# 任务列表
curl http://14.103.112.184:9011/api/tasks
# 预期: JSON 数据
```

### 3. 浏览器测试

访问 http://14.103.112.184:9011/#/instances

**检查步骤**:
1. 打开浏览器开发者工具 (F12)
2. 切换到 Network 标签
3. 刷新页面
4. 查看 API 请求:
   - `/api/instances` - 应该是 200 OK
   - 请求 URL 应该是相对路径
   - 不应该有 CORS 错误

---

## 🌐 预期的网络请求

### 正确的请求流程

```
Browser Request:
GET http://14.103.112.184:9011/api/instances

Nginx (9011) 处理:
- 匹配 location /api/
- 代理到 http://127.0.0.1:9010

Backend (9010) 响应:
- 返回 JSON 数据

Browser 接收:
- Status: 200 OK
- Content-Type: application/json
```

---

## 🔴 如果仍然失败

### 可能的原因

1. **浏览器缓存**
   - 清除浏览器缓存
   - 硬刷新: Ctrl+Shift+R (Windows) 或 Cmd+Shift+R (Mac)

2. **CORS 问题**
   - 检查 Backend 是否设置了 CORS headers
   - 检查 Nginx 配置

3. **Nginx 代理问题**
   - 检查 `/api/` 路径配置
   - 查看 Nginx 错误日志

---

## 📝 调试命令

### Backend 日志
```bash
journalctl -u opencode-pocket -f
```

### Nginx 访问日志
```bash
tail -f /var/log/nginx/access.log | grep 9011
```

### Nginx 错误日志
```bash
tail -f /var/log/nginx/error.log
```

### 测试 API 直接访问
```bash
# 从服务器内部测试
curl http://localhost:9010/api/instances

# 从外部测试通过 Nginx
curl http://14.103.112.184:9011/api/instances
```

---

## ✅ 部署状态

- ✅ 前端代码已修复所有硬编码地址
- ✅ 重新构建前端 (index-Bb0d025Z.js)
- ✅ 上传到服务器
- ✅ 部署到 /data/www/pocket.kxpms.cn
- ✅ Backend 运行在 9010
- ✅ Frontend (Nginx) 运行在 9011
- ✅ API 代理配置正确

---

## 🎯 下一步

1. **清除浏览器缓存**并访问: http://14.103.112.184:9011
2. 打开开发者工具查看 Network 标签
3. 如果仍有问题，提供:
   - Console 中的错误信息
   - Network 标签中失败的请求详情
   - 请求的完整 URL

---

**更新时间**: 2026-06-30 01:26  
**前端版本**: index-Bb0d025Z.js  
**状态**: 已部署，等待浏览器测试
