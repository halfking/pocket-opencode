<template>
  <div class="forgot-password-view">
    <h1>忘记密码</h1>
    <p class="hint">通过邮箱验证码重置密码</p>
    <!--
      完整实现待下一会话补全：
        1. 步骤 1：输入邮箱 → sendCode(email, 'reset')
        2. 步骤 2：输入验证码 + 新密码 → forgotPassword(email, code, newPassword)
        3. 成功后跳转 /login 让用户重新登录
      API 已就绪：frontend/src/api/auth.ts 提供 sendCode / forgotPassword
    -->
    <p class="todo">TODO: 完整表单实现</p>
  </div>
</template>

<script setup lang="ts">
// 占位实现 — 下一会话补全
</script>

<style scoped>
.forgot-password-view {
  padding: 20px;
}
.hint {
  color: var(--text-secondary, #666);
  margin-bottom: 16px;
}
.todo {
  color: var(--warning, #f59e0b);
}
</style>
