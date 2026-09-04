<!--
  SsoCallbackView — RedClaw OIDC SSO 回调处理页。

  浏览器流程：
    1. 用户在 LoginView 点 SSO → 前端拿 /api/auth/sso/login 给的 URL
       (redirect_url=本组件路径) → window.location 跳到 RedClaw Auth Agent
    2. RedClaw 把用户带到 IdP；IdP 认证后回调 RedClaw /sso/callback
    3. RedClaw 颁发平台 JWT 后，浏览器被 302 到本组件 path（含 #/auth/sso/callback）
    4. 本组件在 mount 时：
         - 校验 sessionStorage 里留下的 state
         - 调 /api/auth/me 拿 employee 画像
         - token 是 RedClaw 颁发并已存到 localStorage（由后端 handleAuthSsoCallback 落）
         - 把 user/workspace_id 写进 store，跳到 /ai

  注意：RedClaw 把 token 放在哪（body / cookie / URL fragment）由
  RedClaw handleAuthSsoCallback 决定；本组件只负责"展示中转页"与
  校验 state、落 store、跳转。
-->
<template>
  <div class="sso-callback">
    <div class="spinner" />
    <p class="hint">{{ status }}</p>
    <p v-if="error" class="error">{{ error }}</p>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '../../stores/auth'
import { fetchMe } from '../../api/auth'

const router = useRouter()
const auth = useAuthStore()
const status = ref('正在完成企业账号登录…')
const error = ref('')

onMounted(async () => {
  // 1. 校验 state（防 CSRF）：RedClaw 会原样回传
  const expected = sessionStorage.getItem('pocket_sso_state')
  const params = new URLSearchParams(window.location.search)
  const got = params.get('state')
  if (expected && got && expected !== got) {
    error.value = 'state 校验失败，可能为 CSRF 攻击或 session 已过期'
    status.value = '登录失败'
    return
  }
  sessionStorage.removeItem('pocket_sso_state')

  // 2. 从 query 拿 token（后端 handleAuthSsoCallback 302 注入）
  const token = params.get('token')
  const user = params.get('user') || ''
  const userId = params.get('user_id') || user
  const workspaceId = params.get('workspace_id') || 'default'
  if (!token) {
    error.value = '未拿到 token（RedClaw 回调失败？）'
    status.value = '登录失败'
    return
  }
  auth.setAuthWithWorkspace(token, user, userId, workspaceId, 'redclaw-sso')

  // 3. 拉取 employee 画像更新 UI（失败不阻塞：登入已成功）
  try {
    const me = await fetchMe()
    if (me.name || me.email) {
      auth.setAuthWithWorkspace(token, me.name || user, me.id || userId, workspaceId, 'redclaw-sso')
    }
  } catch (e) {
    console.debug('fetchMe after SSO failed (non-fatal):', e)
  }

  status.value = '登录成功，正在进入…'
  // 清掉 URL 上的 token（避免刷新页面把 token 重新触发 fetchMe 之类）
  router.replace('/ai')
})
</script>

<style scoped>
.sso-callback {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  min-height: 60vh;
  padding: 24px;
  gap: 16px;
}
.spinner {
  width: 36px;
  height: 36px;
  border: 3px solid #ddd;
  border-top-color: #4f46e5;
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}
@keyframes spin {
  to { transform: rotate(360deg); }
}
.hint { color: var(--text-secondary, #666); font-size: 14px; }
.error { color: #d33; font-size: 14px; text-align: center; }
</style>
