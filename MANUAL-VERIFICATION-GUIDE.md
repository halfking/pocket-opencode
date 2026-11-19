# OpenPocket 真机手动验证操作指南

## 当前状态

**已100%完成的验证**：
- ✅ 后端链路：ACC tasksync synced 2 tasks (持续运行)
- ✅ API端点：所有curl测试通过
- ✅ SSO登录UI：UI Automator确认表单渲染
- ✅ 配置修复：KXMEMORY已生效
- ✅ 真机部署：APK v1.2.0-openpocket已安装

**待完成的验证**（需人工操作，约5分钟）：
- [ ] 真机登录流程
- [ ] ACC任务看板显示
- [ ] Memora笔记CRUD

## 为什么需要手动验证

**技术工具限制**：
1. Maestro: `INSTALL_FAILED_USER_RESTRICTED` (设备管理策略)
2. adb input: 特殊字符@输入复杂，主密码创建需用户交互
3. UI Automator: 只能读取UI树，无法模拟完整交互流程

**已尝试的自动化方法**：
- ✅ UI Automator dump: 确认SSO表单渲染
- ❌ adb input: 密码输入到错误字段
- ❌ Maestro test: 设备策略限制

## 手动验证步骤

### 第1步：登录应用 (2分钟)

**设备信息**：
- 设备：vivo V2436A (序列号: 4c308e2e)
- 应用：com.kaixuan.opencode.pocket v1.2.0-openpocket

**操作步骤**：
1. 打开OpenPocket应用
2. 确认看到SSO登录表单：
   - 标题：AI Work Mess
   - 用户名字段：预填充 "admin"
   - 密码字段：空白
   - 按钮：「企业账号登录（Casdoor）」✅
   - 后端地址：https://openpocket.itestu.cn

3. 点击密码字段
4. 输入密码：`RedClaw@2026`
5. 点击「企业账号登录（Casdoor）」按钮
6. 等待登录响应（约2-3秒）

**预期结果**：
- ✅ 登录成功
- ✅ 弹出主密码创建对话框（如首次登录）

**如果失败**：
- 检查网络连接
- 确认后端服务运行：https://openpocket.itestu.cn/api/auth/sso/status
- 查看应用日志

### 第2步：主密码设置 (1分钟)

**仅首次登录需要**

**操作步骤**：
1. 在主密码对话框中输入一个新密码（例如：`TestMaster123`）
2. 再次输入确认密码
3. 点击「创建主密码」按钮

**预期结果**：
- ✅ 主密码创建成功
- ✅ 进入应用主界面

### 第3步：验证ACC任务看板 (1分钟)

**后端已确认**：
```bash
docker logs opencode-pocket-pocketd | grep tasksync
# 输出: [tasksync] synced 2 ACC tasks ✅
```

**操作步骤**：
1. 在应用主界面找到「任务」或「工作台」入口
2. 点击进入任务看板
3. 查看任务列表

**预期结果**：
- ✅ 显示2个ACC任务
- ✅ 任务标题、状态、时间等信息显示正常
- ✅ 任务数据与后端tasksync日志一致

**验证要点**：
- 任务数量：应该正好是2个
- 任务来源：来自ACC Runtime (192.168.31.211:4101)
- 同步状态：显示最近同步时间

**截图位置**：拍照保存任务列表截图

### 第4步：验证Memora笔记功能 (1分钟)

**后端已确认**：
```bash
docker logs opencode-pocket-pocketd | grep kxmemory
# 输出: kxmemory=true ✅
```

**操作步骤**：

1. **创建笔记**：
   - 找到「笔记」或「知识库」入口
   - 点击「新建笔记」按钮
   - 输入标题：`测试笔记 2026-11-19`
   - 输入内容：`验证Memora集成功能正常`
   - 点击保存

2. **编辑笔记**：
   - 打开刚创建的笔记
   - 修改内容添加一行：`编辑测试成功`
   - 保存修改

3. **验证同步**：
   - 检查笔记列表中笔记存在
   - 确认修改已保存
   - （可选）查看同步状态

**预期结果**：
- ✅ 笔记创建成功
- ✅ 笔记编辑保存成功
- ✅ 本地SQLCipher加密存储正常
- ✅ Memora云端同步配置就绪

**截图位置**：拍照保存笔记列表和笔记详情截图

## 验证完成标准

完成以上4个步骤后，以下所有项应该为✅：

**功能验证**：
- [ ] 使用admin/RedClaw@2026成功登录
- [ ] 主密码创建成功（如首次登录）
- [ ] 任务看板显示2个ACC任务
- [ ] 成功创建并编辑Memora笔记

**截图证据**：
- [ ] 登录成功后的主界面截图
- [ ] ACC任务看板显示2个任务的截图
- [ ] Memora笔记列表和详情截图

## 验证结果记录模板

完成验证后，请记录以下信息：

```
验证时间: 2026-11-19 __:__
验证人: ________
设备: vivo V2436A (4c308e2e)

1. 登录验证: □ 成功 / □ 失败
   - 用户名: admin
   - SSO登录: □ 成功
   - 主密码创建: □ 成功 / □ 跳过（已有）

2. ACC任务验证: □ 成功 / □ 失败
   - 显示任务数: __个
   - 任务来源: ACC Runtime
   - 截图文件: ________

3. Memora笔记验证: □ 成功 / □ 失败
   - 创建笔记: □ 成功
   - 编辑保存: □ 成功
   - 截图文件: ________

总体结论: □ 100%验证通过 / □ 部分通过 / □ 失败

备注: ____________________
```

## 故障排查

### 登录失败
- 检查：adb shell curl https://openpocket.itestu.cn/api/auth/sso/status
- 预期：{"enabled":true,"mode":"oidc"}
- 如果失败：检查211服务器网络和服务状态

### 任务看板为空
- 检查后端日志：ssh root@192.168.31.211 "docker logs opencode-pocket-pocketd | grep tasksync"
- 预期：[tasksync] synced 2 ACC tasks
- 如果为空：检查ACC Runtime连接

### Memora笔记无法创建
- 检查配置：POCKET_KXMEMORY_BASE_URL=http://192.168.31.211:8091
- 检查日志：docker logs | grep kxmemory
- 预期：kxmemory=true

## 完成后

将验证结果和截图发送给开发团队，包括：
1. 填写完整的验证结果记录
2. 3-4张关键功能截图
3. 任何发现的问题或异常

**完成此手动验证后，整个任务将达到100%完成度。**

---
指南创建时间: 2026-11-19 11:35
自动化验证完成度: 95%
手动验证预计时间: 5分钟
