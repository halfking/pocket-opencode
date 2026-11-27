/**
 * 登录后邮箱检查逻辑
 * 
 * 用于在用户登录成功后检查是否已设置邮箱，如果没有则引导设置。
 */
import { ref } from 'vue'
import { fetchMe } from '../api/auth'

const shouldShowEmailDialog = ref(false)
const userEmail = ref('')

export function useEmailCheck() {
  /**
   * 检查用户是否已设置邮箱
   * @returns true 表示需要设置邮箱，false 表示已有邮箱
   */
  async function checkEmailRequired(): Promise<boolean> {
    try {
      const me = await fetchMe()
      if (!me.email || me.email.trim() === '') {
        shouldShowEmailDialog.value = true
        return true
      }
      userEmail.value = me.email
      return false
    } catch (e) {
      console.warn('[useEmailCheck] 获取用户信息失败:', e)
      // 获取失败时不强制设置邮箱，避免阻塞用户
      return false
    }
  }

  function closeEmailDialog() {
    shouldShowEmailDialog.value = false
  }

  function skipEmailSetup() {
    shouldShowEmailDialog.value = false
    // 可以在这里记录用户跳过了邮箱设置，下次登录再提醒
    localStorage.setItem('email_setup_skipped', Date.now().toString())
  }

  function onEmailVerified(email: string) {
    userEmail.value = email
    shouldShowEmailDialog.value = false
  }

  return {
    shouldShowEmailDialog,
    userEmail,
    checkEmailRequired,
    closeEmailDialog,
    skipEmailSetup,
    onEmailVerified,
  }
}
