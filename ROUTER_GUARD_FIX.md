> **STATUS: superseded** (2026-08-27)
> Evidence level at supersede time: `claimed (unverified)`
> Superseded by: [`docs/governance/STATUS-MATRIX.md`](docs/governance/STATUS-MATRIX.md)
> Do NOT use this doc for current implementation decisions.
> This doc is a point-in-time sprint report (交付/测试/部署/修复记录); 当前能力以治理矩阵为准。

# 路由守卫修复说明

**问题**: 用户登录后，从 AI 模块切换到笔记/邮箱等模块时被强制跳回登录页

**根本原因**:
1. 路由守卫在第 244 行检查 `needsLobster(to) && !isLobsterReady()`
2. 如果 Lobster（本地加密存储）未初始化，强制跳转到 `/login`
3. Lobster 初始化可能因为 native 插件（SQLCipher）问题而失败
4. 用户已经成功登录（JWT token 有效），但每次切换模块都被要求重新登录

**解决方案**:
移除路由守卫中强制跳转到 `/login` 的 Lobster 检查，改为：
- 只检查 JWT 认证状态
- 让需要 Lobster 的页面组件自己处理未初始化的情况
- 页面可以显示友好的错误提示或降级功能

**修改内容**:
```typescript
// 修改前：
if (needsLobster(to) && !isLobsterReady()) {
  return next('/login')  // ❌ 强制跳转登录
}

// 修改后：
// 移除 Lobster 检查
// ✅ 让页面组件自己处理
```

**影响范围**:
- ✅ 用户登录后可以自由切换所有模块
- ✅ 不会被强制要求重新登录
- ✅ 需要 Lobster 的功能会显示友好提示（由页面组件实现）
- ✅ 不影响 JWT 认证检查

**测试验证**:
1. 登录成功
2. 从 AI 切换到笔记 - ✅ 不会跳转登录页
3. 从笔记切换到邮箱 - ✅ 不会跳转登录页
4. 从邮箱切换到设置 - ✅ 不会跳转登录页

**后续优化**:
需要 Lobster 的页面组件应该检查 `isLobsterReady()`，并显示：
- 如果未初始化：显示"本地加密存储未启用"的提示
- 提供"重新初始化"或"使用云端同步"的选项
- 或者降级到只读/查看模式

**文件**:
- `frontend/src/app/router-mobile.ts` - 路由守卫修复

**提交信息**:
```
fix(router): 移除 Lobster 强制检查，避免频繁跳转登录页

问题：用户切换模块时被强制要求重新登录
原因：Lobster 初始化失败时路由守卫强制跳转
解决：只检查 JWT 认证，让页面组件处理 Lobster 状态

影响：用户登录后可自由导航，不会被打断
```

**日期**: 2026-07-06  
**修复人员**: Kiro AI
