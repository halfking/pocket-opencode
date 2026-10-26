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
   */
  async function decide(id: string, optionId: string): Promise<boolean> {
    decidingId.value = id
    error.value = null
    try {
      await decideAccApproval(id, optionId)
      approvals.value = approvals.value.filter((a) => a.id !== id)
      return true
    } catch (err: any) {
      error.value = err?.message || '应答待批项失败'
      return false
    } finally {
      decidingId.value = null
    }
  }

  return { approvals, loading, error, decidingId, loadApprovals, decide }
})
