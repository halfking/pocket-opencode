/**
 * ACC Approvals Store — ACC 派单待批项的客户端状态。
 *
 * 与 `accTasks`（委托/停止）配套：委托出去的任务一旦要人工批准，
 * 待批项就出现在这里，用户答完，智能体继续跑。
 *
 * 为什么是独立 store（而不是塞进 accTasks）：
 *   待批项是**另一个数据源**（agent-companion 的干预队列），
 *   生命周期也不同（它有轮询，accTasks 没有）。混在一起会让
 *   「委托一次」这个一次性动作背上轮询副作用。
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { decideAccApproval, listAccApprovals, type AccApproval } from '../api/accApprovals'

export const useAccApprovalsStore = defineStore('accApprovals', () => {
  const approvals = ref<AccApproval[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)
  /** 正在应答中的待批项 id（用于禁用按钮，避免重复应答）。 */
  const decidingId = ref<string | null>(null)

  /**
   * 拉一次待批项。
   *
   * 失败**不**清空列表：上游抖一下就把用户看到的待批项弄没了，
   * 比多显示一条过期记录更糟。
   */
  async function loadApprovals(): Promise<void> {
    loading.value = true
    try {
      approvals.value = await listAccApprovals('pending')
      error.value = null
    } catch (err: any) {
      error.value = err?.message || '读取待批项失败'
    } finally {
      loading.value = false
    }
  }

  /**
   * 应答一个待批项。成功后立刻把这一条从列表里去掉（服务端已经落定）。
   *
   * ★ 2026-10-26 修：应答失败时**不能**把条目留在列表里。
   *   daemon 侧这条已经不是 pending 时（被别人答了 = 409、或压根不存在 = 404）
   *   再点它只会再失败一次，而且**永远点不掉** ⇒ 屏幕上留下一张死卡片
   *   （实测：一条早已被拒的待批项在手机上挂了很久，用户怎么点都没用）。
   *   ⇒ 这两种错误要把条目从列表里摘掉，并说清楚发生了什么；
   *     其它错误（网络抖、服务端 5xx）才保留条目 —— 那种情况它**也许**
   *     还能答，贸然删掉等于替用户做了决定。
   */
  async function decide(id: string, optionId: string): Promise<boolean> {
    decidingId.value = id
    error.value = null
    try {
      await decideAccApproval(id, optionId)
      approvals.value = approvals.value.filter((a) => a.id !== id)
      return true
    } catch (err: any) {
      const status = err?.status
      if (status === 404 || status === 409) {
        approvals.value = approvals.value.filter((a) => a.id !== id)
        error.value =
          status === 409
            ? '该待批项已被处理或已过期，已从列表移除'
            : '该待批项已不存在，已从列表移除'
      } else {
        error.value = err?.message || '应答待批项失败'
      }
      return false
    } finally {
      decidingId.value = null
    }
  }

  return { approvals, loading, error, decidingId, loadApprovals, decide }
})
