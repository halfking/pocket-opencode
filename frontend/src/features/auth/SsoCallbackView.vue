<!--
  SsoCallbackView — RedClaw OIDC SSO 回调处理页。

  浏览器流程：
    1. 用户在 LoginView 点 SSO → 前端拿 /api/auth/sso/login 给的 URL
       (redirect_url=本组件上游的 /api/auth/sso/callback 路径) →
       window.location 跳到 RedClaw Auth Agent，同时后端已落下
       HttpOnly 绑定 cookie（pocket_sso_txn）
    2. RedClaw 把用户带到 IdP；IdP 认证后回调 /api/auth/sso/callback
    3. 后端消费绑定 cookie、透传 code+state 给 auth-agent 换平台 JWT，
       签发一次性 sso_code 后 302 到本组件 path
       （/auth/sso/callback?sso_code=...；失败时 ?error=...）
    4. 本组件在 mount 时：
         - error → 展示稳定错误码对应文案
         - sso_code → POST /api/auth/sso/exchange 换 token（token 不走
           URL，防浏览器历史 / 访问日志泄露）
         - 落 store，跳到 /ai

  state 合约（2026-09-05 修复）：RedClaw auth-agent 自行生成 state 并由 IdP
  原样带回，前端 sessionStorage 严格比对永远不成立（旧实现死路），CSRF
  绑定已改由后端绑定 cookie 承担。见
  docs/handoff/2026-09-05-sso-state-contract-mismatch.md。
-->
<template>
  <div class="sso-callback">
    <div v-if="!error" class="spinner" />
    <p class="hint">{{ status }}</p>
    <p v-if="error" class="error">{{ error }}</p>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '../../stores/auth'
import { fetchMe, exchangeSsoCode } from '../../api/auth'

const router = useRouter()
const auth = useAuthStore()
const status = ref('正在完成企业账号登录…')
const error = ref('')

/** 后端 ssoRedirectError 的稳定错误码 → 用户可读文案。 */
const ERROR_MESSAGES: Record<string, string> = {
  sso_session: '登录会话校验失败（绑定已失效或回调被重放），请回到登录页重新发起',
  sso_idp: '身份提供商认证失败或已取消，请重试',
  sso_invalid: '回调参数不完整，请重新发起登录',
  sso_upstream: 'RedClaw 平台换取凭据失败，请稍后重试',
  sso_no_user: 'RedClaw 未返回有效用户身份，请联系管理员',
}

function fail(msg: string) {
  error.value = msg
  status.value = '登录失败'
}

onMounted(async () => {
  const params = new URLSearchParams(window.location.search)
  const errCode = params.get('error')
  const ssoCode = params.get('sso_code')
  // 无论成败先把一次性 code 从地址栏清掉（code 已消费或即将消费，
  // 留在历史/地址栏只会诱导刷新重放）。
  window.history.replaceState({}, '', window.location.pathname)

  if (errCode) {
    fail(ERROR_MESSAGES[errCode] || `SSO 登录失败（${errCode}）`)
    return
  }
  if (!ssoCode) {
    fail('未拿到登录凭据（回调链路中断？），请重新发起登录')
    return
  }

  // 1. 一次性 code 换登录结果（90s TTL、单次有效）
  let handoff
  try {
    handoff = await exchangeSsoCode(ssoCode)
  } catch (e: any) {
    console.debug('sso exchange failed:', e)
    fail('登录凭据已过期或无效，请重新发起登录')
    return
  }
  auth.setAuthWithWorkspace(handoff.token, handoff.user, handoff.user_id, handoff.workspace_id || 'default', 'redclaw-sso')

  // 2. 拉取 employee 画像更新 UI（失败不阻塞：登入已成功）
  try {
    const me = await fetchMe()
    if (me.name || me.email) {
      auth.setAuthWithWorkspace(handoff.token, me.name || handoff.user, me.id || handoff.user_id, handoff.workspace_id || 'default', 'redclaw-sso')
    }
  } catch (e) {
    console.debug('fetchMe after SSO failed (non-fatal):', e)
  }

  status.value = '登录成功，正在进入…'
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
