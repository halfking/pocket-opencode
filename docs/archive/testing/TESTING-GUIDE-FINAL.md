# OpenPocket 最终测试指南

## 🎯 当前状态

**日期**: 2026-11-25  
**服务器**: 192.168.31.211 (healthy ✓)  
**设备**: 4c308e2e (Xiaomi)  
**应用状态**: 已安装，在登录页面

---

## 📱 立即开始测试

### 步骤 1: 准备登录

应用当前在登录页面，显示"企业账号登录（Casdoor）"表单。

### 步骤 2: 清除错误信息

如果看到红色错误提示，请：
1. 清空用户名输入框
2. 清空密码输入框

### 步骤 3: 输入凭据

**推荐使用您的账户**:
```
用户名: huangxt
密码: <DEV-PASS>…<masked>
```

**或使用 admin 账户**:
```
用户名: admin
密码: <DEV-ADMIN-PASS>…<masked>
```

### 步骤 4: 登录

1. 点击**"企业账号登录（Casdoor）"**按钮
2. 等待 3-5 秒

### 步骤 5: 创建主密码（如果提示）

如果弹出"创建主密码"对话框：
```
主密码: [REDACTED:secret]
确认主密码: [REDACTED:secret]
```
然后点击"创建"按钮

### 步骤 6: 验证登录成功

登录成功后应该看到：
- ✓ 底部导航栏（AI、会话、任务、笔记、更多）
- ✓ 左上角的菜单按钮
- ✓ 主界面内容

---

## 🧪 功能验证清单

登录成功后，请依次测试以下功能：

### 基础导航
- [ ] 点击"AI"标签 → 进入AI聊天界面
- [ ] 点击"会话"标签 → 查看会话列表
- [ ] 点击"任务"标签 → 查看任务管理
- [ ] 点击"笔记"标签 → 查看笔记列表
- [ ] 点击"更多"标签 → 进入更多菜单

### AI 聊天测试
1. 点击"AI"标签
2. 在输入框输入: "你好，测试一下"
3. 点击发送
4. 验证是否收到回复

### 会议功能测试
1. 点击"会话"标签
2. 检查是否能看到会议列表
3. 尝试创建新会议（可选）

### 设置页面测试
1. 点击"更多"
2. 点击"设置"
3. 检查各项设置是否可访问

---

## 🐛 故障排查

### 如果登录失败

**错误: "用户名或密码错误"**

解决方案1 - 使用测试密码:
```bash
# 在Mac上运行以下命令重置为简单密码
ssh 192.168.31.211 "docker exec acc-pg17 psql -U postgres -d casdoor" << 'EOSQL'
UPDATE "user" 
SET password = '\$2a\$10\$ssleUwjRl.3VJlEdd0IlEOPqCHyC1Wror6yrCL/Y5Q.o04/mgvvTK',
    password_type = 'bcrypt'
WHERE owner='redclaw' AND name='huangxt';
EOSQL

# 然后在真机上使用:
# 用户名: huangxt
# 密码: Test@123456
```

**错误: "连接失败" 或 超时**

检查网络:
```bash
# 在Mac上运行
curl http://192.168.31.211:8090/healthz
```

**应用崩溃或卡住**

重启应用:
```bash
adb -s 4c308e2e shell am force-stop com.kaixuan.opencode.pocket
adb -s 4c308e2e shell am start -n com.kaixuan.opencode.pocket/.MainActivity
```

### 查看日志

**应用日志**:
```bash
adb -s 4c308e2e logcat -s chromium:I | grep -E "LoginView|error|Error"
```

**服务器日志**:
```bash
ssh 192.168.31.211 "docker logs opencode-pocket-pocketd --tail 50"
```

---

## 📊 测试报告模板

测试完成后，请记录以下信息：

```
[ ] 登录成功 (用户: _______)
[ ] 底部导航正常显示
[ ] AI 聊天功能正常
[ ] 会话功能可访问
[ ] 任务管理可访问
[ ] 笔记功能可访问
[ ] 设置页面可访问

遇到的问题:
- 

备注:
-
```

---

## ✅ 预期结果

**成功标准**:
1. 能够使用提供的凭据成功登录
2. 看到完整的应用主界面
3. 底部5个导航标签都可以点击
4. AI聊天可以发送消息
5. 各个功能模块可以正常访问

**时间预估**: 5-10 分钟

---

## 📞 支持信息

如遇到无法解决的问题，请提供：
1. 错误截图
2. 应用日志（上面的命令）
3. 操作步骤描述

**部署文档**: `DEPLOYMENT-VERIFICATION-2026-11-25.md`  
**测试脚本**: `.maestro/sso-login-test.yaml`

---

**祝测试顺利！** 🎉
