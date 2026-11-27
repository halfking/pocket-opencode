# 邮箱验证码密码管理功能

## 功能概述

本次更新新增了完整的邮箱验证码驱动的密码管理功能，包括：

1. **已登录用户修改密码**（通过邮箱验证码，无需旧密码）
2. **登录后邮箱检查和设置流程**
3. **更新用户邮箱功能**

## 新增后端端点

### 1. POST /api/auth/change-password-with-code

已登录用户通过邮箱验证码修改密码（无需旧密码）。

**请求头：**
```
Authorization: Bearer <token>
```

**请求体：**
```json
{
  "email": "user@example.com",
  "code": "123456",
  "new_password": "newpassword123"
}
```

**响应：**
```json
{
  "ok": true
}
```

**错误响应：**
- 400: 验证码错误或已过期
- 403: 邮箱与当前用户不匹配
- 401: 未登录

### 2. POST /api/auth/update-email

已登录用户更新邮箱（需要验证码验证新邮箱所有权）。

**请求头：**
```
Authorization: Bearer <token>
```

**请求体：**
```json
{
  "email": "newemail@example.com",
  "code": "123456"
}
```

**响应：**
```json
{
  "ok": true
}
```

**错误响应：**
- 400: 验证码错误或已过期
- 409: 该邮箱已被其他用户使用
- 401: 未登录

## 前端新增功能

### 1. 修改密码页面 (ChangePasswordView.vue)

路由：`/change-password`

**功能：**
- 已登录用户可以通过邮箱验证码修改密码
- 无需输入旧密码
- 支持验证码发送和重发（60秒冷却）
- 修改成功后自动退出登录，引导用户使用新密码重新登录

**使用流程：**
1. 用户访问 `/change-password`
2. 输入邮箱地址，点击"发送验证码"
3. 输入收到的验证码和新密码
4. 确认新密码
5. 提交修改
6. 修改成功后自动退出，使用新密码重新登录

### 2. 邮箱验证对话框 (EmailVerificationDialog.vue)

**功能：**
- 可复用的邮箱验证对话框组件
- 支持必填/可跳过两种模式
- 两步流程：发送验证码 → 验证邮箱
- 支持验证码重发

**使用示例：**
```vue
<EmailVerificationDialog
  :open="showEmailDialog"
  :required="false"
  :initial-email="userEmail"
  @close="onClose"
  @success="onEmailVerified"
  @skip="onSkip"
/>
```

### 3. 登录后邮箱检查

**功能：**
- 用户登录成功后自动检查是否已设置邮箱
- 如果没有设置邮箱，显示邮箱验证对话框
- 用户可以选择立即设置或跳过

**流程顺序：**
1. 用户登录成功
2. 检查主密码 → 如果没有，显示主密码创建对话框
3. 主密码创建完成后 → 检查邮箱
4. 如果没有邮箱 → 显示邮箱验证对话框
5. 邮箱设置完成或跳过 → 进入应用主界面

## API 函数

新增的 API 函数位于 `frontend/src/api/auth.ts`：

```typescript
// 已登录用户通过邮箱验证码修改密码
export async function changePasswordWithCode(
  email: string, 
  code: string, 
  newPassword: string
): Promise<OkResponse>

// 已登录用户更新邮箱
export async function updateEmail(
  email: string, 
  code: string
): Promise<OkResponse>
```

## 使用场景

### 场景 1：用户忘记密码（未登录）

使用现有的 `/forgot-password` 页面，流程不变。

### 场景 2：用户想修改密码（已登录，但记不住旧密码）

1. 进入设置页面
2. 点击"修改密码"
3. 跳转到 `/change-password`
4. 通过邮箱验证码修改密码
5. 使用新密码重新登录

### 场景 3：新用户首次登录

1. 用户登录成功
2. 系统检测到没有邮箱
3. 自动弹出邮箱设置对话框
4. 用户填写邮箱并验证
5. 进入应用

### 场景 4：用户想更换邮箱

1. 使用 `EmailVerificationDialog` 组件
2. 输入新邮箱
3. 验证新邮箱所有权
4. 更新成功

## 安全考虑

1. **验证码验证**：所有验证码操作都需要验证所有权
2. **邮箱匹配检查**：修改密码时会验证邮箱是否属于当前登录用户
3. **防止邮箱冲突**：更新邮箱时检查是否已被其他用户使用
4. **自动登出**：密码修改成功后强制重新登录
5. **频率限制**：验证码发送有频率限制（后端已实现）

## 测试建议

### 后端测试

```bash
# 1. 测试已登录改密码（邮箱验证码）
curl -X POST http://localhost:8080/api/auth/send-code \
  -H "Content-Type: application/json" \
  -d '{"email":"test@example.com","purpose":"reset"}'

curl -X POST http://localhost:8080/api/auth/change-password-with-code \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{"email":"test@example.com","code":"123456","new_password":"newpass123"}'

# 2. 测试更新邮箱
curl -X POST http://localhost:8080/api/auth/send-code \
  -H "Content-Type: application/json" \
  -d '{"email":"newemail@example.com","purpose":"register"}'

curl -X POST http://localhost:8080/api/auth/update-email \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{"email":"newemail@example.com","code":"123456"}'
```

### 前端测试

1. **测试修改密码流程**
   - 登录系统
   - 访问 `/change-password`
   - 完成密码修改流程
   - 验证是否自动退出
   - 使用新密码重新登录

2. **测试登录后邮箱检查**
   - 创建一个没有邮箱的新用户
   - 登录该用户
   - 验证是否弹出邮箱设置对话框
   - 测试"跳过"和"设置邮箱"两条路径

3. **测试邮箱更新**
   - 使用 EmailVerificationDialog 组件
   - 测试邮箱冲突检测
   - 验证邮箱更新成功

## 配置要求

### 后端环境变量

确保配置了 SMTP 服务器用于发送验证码：

```bash
POCKET_SMTP_HOST=smtp.example.com
POCKET_SMTP_PORT=587
POCKET_SMTP_USER=noreply@example.com
POCKET_SMTP_PASSWORD=your_password
POCKET_SMTP_FROM=noreply@example.com
```

如果没有配置 SMTP，验证码只会记录在日志中，需要设置：

```bash
POCKET_SMTP_DEBUG_ECHO=true  # 开发环境回显验证码
```

## 数据库变更

**无需额外的数据库迁移**，使用现有的 `users` 表：
- `email` 字段：存储用户邮箱
- `email_verified` 字段：标记邮箱是否已验证

## 部署注意事项

1. **向后兼容**：新功能不影响现有的登录和密码重置流程
2. **可选功能**：邮箱检查可跳过，不会阻塞用户使用
3. **渐进增强**：老用户首次登录时会被提示设置邮箱
4. **SMTP 配置**：生产环境需要正确配置 SMTP 才能发送验证码

## 文件清单

### 后端文件
- `backend/internal/server/server_auth_extended.go` - 新增两个端点
- `backend/internal/auth/users.go` - 新增 `UpdateUserEmail` 方法
- `backend/internal/server/server.go` - 路由注册

### 前端文件
- `frontend/src/api/auth.ts` - 新增 API 函数
- `frontend/src/features/auth/ChangePasswordView.vue` - 修改密码页面
- `frontend/src/features/auth/EmailVerificationDialog.vue` - 邮箱验证对话框
- `frontend/src/features/auth/LoginView.vue` - 登录后邮箱检查逻辑
- `frontend/src/composables/useEmailCheck.ts` - 邮箱检查组合函数
- `frontend/src/app/router-mobile.ts` - 路由配置

## 未来改进

1. 支持邮箱变更通知（向旧邮箱发送变更通知）
2. 支持多因素认证（MFA）
3. 邮箱验证历史记录
4. 密码强度检测增强
5. 支持手机号验证作为邮箱的替代方案
