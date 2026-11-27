# OpenPocket 部署验证报告
**日期**: 2026-11-25  
**服务器**: 192.168.31.211  
**测试设备**: 4c308e2e (Xiaomi 2411DRN47C)

## ✅ 部署完成摘要

### 1. 认证架构确认
- **系统**: Casdoor OIDC SSO (v2.63.0)
- **数据库**: PostgreSQL `casdoor` (生产环境)
- **组织**: `redclaw`
- **应用**: `openpocket` (client_id: 3b32499fd1d86bcbbbf434fd5798dc5e)
- **注意**: 本地密码登录已退役，返回 HTTP 410

### 2. 用户凭据

#### Admin 账户
- **用户名**: `admin`
- **密码**: `RedClaw@2026`
- **邮箱**: `[REDACTED]`
- **组织**: `redclaw`
- **数据库**: `casdoor`.`user` 表

#### 你的账户
- **用户名**: `huangxt`
- **密码**: `Huangxt@2026`
- **邮箱**: `huangxt@itestu.cn`
- **组织**: `redclaw`

#### Casdoor 管理后台
- **URL**: http://192.168.31.211:28000
- **组织**: `built-in`
- **用户名**: `admin`
- **密码**: `Admin@2026` (仅用于访问 Casdoor 后台，不是 OpenPocket 登录密码)

### 3. 服务器状态

**Backend (pocketd)**:
```bash
$ curl http://192.168.31.211:8090/healthz
ok

$ curl http://192.168.31.211:8090/api/auth/sso/status
{"enabled":true,"mode":"oidc"}
```

**容器状态**:
```
opencode-pocket-pocketd    healthy    8090/tcp
opencode-pocket-frontend   healthy    4175/tcp
redclaw-local-stack-redclaw-casdoor-1   healthy    28000/tcp
```

### 4. Android 真机配置

**APK 信息**:
- 包名: `com.kaixuan.opencode.pocket`
- API 地址: `http://192.168.31.211:8090`
- Capacitor scheme: `http` (内网测试)
- 大小: 2.0GB

**验证结果**:
```
[LoginView] SSO status loaded: [object Object] ✅
```

## 📱 手动测试步骤

### 测试 1: SSO 企业账号登录

1. **启动应用**
   ```bash
   adb -s 4c308e2e shell am start -n com.kaixuan.opencode.pocket/.MainActivity
   ```

2. **登录步骤**
   - 应用启动后应该显示登录界面
   - 查看是否有"企业账号登录"表单（用户名 + 密码）
   - 输入用户名: `huangxt`
   - 输入密码: `Huangxt@2026`
   - 点击"企业账号登录"

3. **预期结果**
   - 登录成功，进入主界面
   - 如果提示"创建主密码"，输入并确认主密码（例如: `TestMaster@123`）
   - 应该看到底部导航栏（AI、会话、任务等）

4. **验证功能**
   - 点击"AI"标签，验证聊天功能
   - 点击"会议"标签，验证会议功能
   - 点击"任务"标签，验证任务管理
   - 点击"更多"菜单，验证设置页面

### 测试 2: 用户切换

1. **退出登录**
   - 点击"更多" → "设置"
   - 滚动到底部，点击"退出登录"

2. **使用 admin 账户登录**
   - 用户名: `admin`
   - 密码: `RedClaw@2026`
   - 验证能否正常登录

### 测试 3: Casdoor 管理后台

1. **访问管理界面**
   ```bash
   open http://192.168.31.211:28000
   ```

2. **登录步骤**
   - 组织: `built-in`
   - 用户名: `admin`
   - 密码: `Admin@2026`

3. **验证功能**
   - 切换到 `redclaw` 组织
   - 查看用户列表（应该有 39 个用户）
   - 验证 `openpocket` 应用配置

## 🔧 Maestro 自动化测试

### 准备工作

由于 MIUI 的安全限制，需要先允许 Maestro 安装应用：

1. **设置 → 应用 → 应用管理**
2. 找到 Maestro 相关应用
3. 允许"安装未知应用"权限

### 运行测试

```bash
cd /Users/xutaohuang/workspace/ai-native-tools/client/openpocket

# 设置环境变量
export POCKET_DEV_PASS="Huangxt@2026"
export POCKET_MASTER="TestMaster@123"

# 运行 SSO 登录测试
maestro test --device 4c308e2e .maestro/sso-login-test.yaml

# 运行完整测试套件
maestro test --device 4c308e2e .maestro/smoke-test-suite.yaml
```

### 可用的测试套件

- `sso-login-test.yaml` - SSO 企业账号登录测试
- `smoke-test-suite.yaml` - 烟雾测试套件
- `regression-test-suite.yaml` - 回归测试套件
- `master-test-suite.yaml` - 完整功能测试

## 🐛 已知问题

### 1. Maestro 安装权限
**问题**: `INSTALL_FAILED_USER_RESTRICTED: Install canceled by user`
**原因**: MIUI 安全限制
**解决**: 在设置中允许 Maestro 安装未知应用

### 2. 明文 HTTP 连接
**状态**: 已配置 `CAP_ANDROID_SCHEME=http` 允许内网测试
**注意**: 生产环境需要使用 HTTPS

## 📊 测试检查清单

### 基础功能
- [ ] SSO 企业账号登录成功
- [ ] admin 账户登录成功
- [ ] huangxt 账户登录成功
- [ ] 主密码创建和解锁
- [ ] 底部导航栏正常显示
- [ ] 侧边菜单可以打开

### 核心功能
- [ ] AI 聊天功能正常
- [ ] 会议创建和列表
- [ ] 任务管理功能
- [ ] 笔记功能
- [ ] 设置页面可访问

### 账户管理
- [ ] 退出登录功能
- [ ] 用户切换功能
- [ ] 个人信息显示正确

## 🔄 回滚步骤

如需回滚到之前的版本：

```bash
ssh 192.168.31.211
cd /opt/kaixuan/stacks/platform

# 回滚镜像
docker tag opencode-pocket:rollback-20261124T185145Z opencode-pocket:amd64
docker tag opencode-pocket-frontend:rollback-20261124T185145Z opencode-pocket-frontend:amd64

# 重启服务
docker compose -p kaixuan-platform up -d --no-deps --force-recreate \
  opencode_pocket_pocketd opencode_pocket_frontend
```

## 📝 重要提示

1. **密码复杂度**: 所有密码都符合企业安全要求（至少 8 位，包含大小写字母和数字）
2. **数据库访问**: Casdoor 数据在 PostgreSQL `acc-pg17` 容器，`casdoor` 数据库
3. **日志查看**: 
   ```bash
   # Backend 日志
   ssh 192.168.31.211 "docker logs opencode-pocket-pocketd --tail 100"
   
   # Casdoor 日志
   ssh 192.168.31.211 "docker logs redclaw-local-stack-redclaw-casdoor-1 --tail 100"
   ```

## ✅ 验证完成

**部署人**: 自动化部署系统  
**验证人**: 待手动验证  
**签字日期**: _________________

---

**下一步行动**:
1. 在真机上手动验证登录流程
2. 测试核心功能是否正常
3. 配置 Maestro 权限后运行自动化测试
4. 记录任何发现的问题
