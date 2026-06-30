# 🔍 OpenCode Pocket 实例列表问题排查指南

**问题:** 手机 App 上看不到 OpenCode 实例列表  
**状态:** 正在排查中

---

## ✅ 已确认正常的部分

### 1. Backend API 正常工作
```bash
curl http://14.103.169.56:8088/api/instances
# ✅ 返回 4 个实例
```

### 2. 实例配置正确
```json
{
  "instances": [
    {
      "id": "opencode-local-test",
      "displayName": "OpenCode 本地测试 (XUTAOdeMacBook-Pro)",
      "environment": "development"
    },
    {
      "id": "opencode-kaixuan1",
      "displayName": "OpenCode @ kaixuan-1",
      "environment": "production"
    },
    {
      "id": "opencode-kaixuan2",
      "displayName": "OpenCode @ kaixuan-2",
      "environment": "production"
    },
    {
      "id": "opencode-kaixuan3",
      "displayName": "OpenCode @ kaixuan-3",
      "environment": "production"
    }
  ]
}
```

### 3. 前端 API 地址配置
```
VITE_API_BASE=http://14.103.169.56:8088
```

---

## 🔍 可能的问题

### 问题 1: 网络连接
**症状:** 手机无法访问 14.103.169.56:8088

**检查方法:**
```
手机和电脑是否在同一网络？
手机是否可以访问其他内网服务？
```

**解决方案:**
- 确保手机和服务器在同一网络
- 或者配置端口转发
- 或者使用外网地址

### 问题 2: CORS 跨域
**症状:** API 请求被浏览器阻止

**检查方法:**
```bash
# 检查 Backend CORS 配置
curl -H "Origin: capacitor://localhost" \
     -H "Access-Control-Request-Method: GET" \
     -X OPTIONS \
     http://14.103.169.56:8088/api/instances
```

**解决方案:**
- Backend 需要允许 Capacitor 的 Origin

### 问题 3: HTTP vs HTTPS
**症状:** 混合内容被阻止

**当前配置:**
```
App: 本地文件 (file:// 或 capacitor://)
API: http://14.103.169.56:8088 (HTTP)
```

**检查:**
- Android 配置已设置 `allowMixedContent: true`
- Network Security Config 已允许 cleartext

### 问题 4: API 响应格式
**症状:** 前端无法解析响应

**检查:**
```javascript
// 前端期望的格式
{ instances: [...] }

// Backend 返回的格式
{ instances: [...] } ✅ 匹配
```

---

## 📱 在手机上的诊断步骤

### Step 1: 检查登录页
```
打开 App
→ 是否显示渐变紫色登录页？
→ 是否有用户名/密码输入框？
```

### Step 2: 检查服务器选择
```
登录 (admin/admin)
→ 是否跳转到服务器选择页？
→ 是否看到 2 个服务器卡片？
```

### Step 3: 检查实例列表状态
```
点击 "NPS 56 服务器"
→ 实例列表页显示什么？

可能的状态：
A) 显示加载中（转圈动画）
B) 显示空状态（"暂无可用的 OpenCode 实例"）
C) 显示错误信息（"加载失败: xxx"）
D) 显示 4 个实例卡片 ✅
E) 页面卡住/白屏
```

---

## 🔧 快速修复方案

### 方案 A: 使用外网地址（如果手机不在内网）

**修改 .env:**
```bash
# 如果手机在外网，使用外网地址
VITE_API_BASE=https://pocket.kxpms.cn
```

**重新构建:**
```bash
npm run build
npx cap sync android
cd android && ./gradlew assembleDebug
```

### 方案 B: 使用 adb 端口转发（临时方案）

```bash
# 将手机的 8088 端口转发到电脑的服务器
adb reverse tcp:8088 tcp:8088

# 然后修改 API 地址为 localhost
VITE_API_BASE=http://localhost:8088
```

### 方案 C: 添加详细的网络日志

**在 api/client.ts 中添加:**
```typescript
async getInstances(): Promise<Instance[]> {
  console.log('🌐 API_BASE:', API_BASE)
  console.log('🌐 请求 URL:', `${API_BASE}/api/instances`)
  
  try {
    const response = await fetch(`${API_BASE}/api/instances`)
    console.log('📊 响应状态:', response.status)
    console.log('📊 响应头:', response.headers)
    
    const data = await response.json()
    console.log('📊 响应数据:', data)
    
    return data.instances
  } catch (error) {
    console.error('❌ 网络错误:', error)
    throw error
  }
}
```

---

## 📋 信息收集清单

请提供以下信息：

### 手机状态
- [ ] 手机型号: vivo X Fold5
- [ ] 系统版本: Android 16
- [ ] 网络连接: WiFi / 4G / 5G
- [ ] 与电脑是否同一网络: 是 / 否

### App 状态
- [ ] 登录页是否正常: 是 / 否
- [ ] 服务器选择页是否正常: 是 / 否
- [ ] 实例列表页显示: A / B / C / D / E (见上面的状态)
- [ ] 是否有错误提示: 是 / 否，内容：______

### 测试结果
- [ ] 从手机浏览器访问 http://14.103.169.56:8088: 成功 / 失败
- [ ] 从手机浏览器访问 http://14.103.169.56:8088/api/instances: 成功 / 失败

---

## 🔬 高级调试

### 使用 Chrome 远程调试

**步骤:**
```
1. 电脑打开 Chrome
2. 访问 chrome://inspect
3. 手机通过 USB 连接
4. 在 Chrome 中找到 OpenCode Pocket
5. 点击 "inspect"
6. 查看 Console 和 Network 标签
```

### 查看网络请求
```
在 Chrome DevTools 中:
1. 切换到 Network 标签
2. 在手机上刷新实例列表
3. 查看是否有请求到 14.103.169.56:8088
4. 查看请求状态和响应
```

---

## ✅ 解决后的验证

当问题解决后，你应该看到：

```
实例列表页
  ↓
4 个实例卡片:
  📱 OpenCode 本地测试 (XUTAOdeMacBook-Pro)
  💻 OpenCode @ kaixuan-1
  💻 OpenCode @ kaixuan-2
  💻 OpenCode @ kaixuan-3
```

---

**请告诉我手机上看到的具体状态，我会根据情况提供针对性的解决方案！** 🔍
