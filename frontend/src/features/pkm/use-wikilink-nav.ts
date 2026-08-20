/**
 * use-wikilink-nav.ts — wikilink 点击导航（点击即创建）。
 *
 * 点击 [[目标]]：
 *   - 目标笔记存在 → 跳转 /pkm/n/{id}
 *   - 不存在 → 新建空笔记（title=目标）再跳转
 *
 * 被 PkmNoteView / PkmTodayView 复用。
 */
import { useRouter } from 'vue-router'
import { findByTitle, saveNote } from './pkm-store'
import { useAuthStore } from '../../stores/auth'

export function useWikilinkNav() {
  const router = useRouter()
  const auth = useAuthStore()
  const workspaceId = auth.workspaceId || 'default'

  async function navigate(target: string) {
    if (!target) return
    const existing = await findByTitle(target, workspaceId)
    if (existing) {
      router.push(`/pkm/n/${existing.id}`)
      return
    }
    // 不存在 → 新建空笔记后跳转（Roam 式点击即创建）
    const created = await saveNote({
      title: target,
      html: '',
      workspaceId,
    })
    router.push(`/pkm/n/${created.id}`)
  }

  return { navigate }
}
