import { computed, ref } from 'vue'
import { emailApi, type EmailClassifyReport } from '../../api/email'
import i18n from '../../i18n'
import { classifyStopHint, runClassifyLoop } from './email-classify-loop'
import { normalizeEmailCategory } from './email-categories'
import { applyClassifyResult, classifyProgressLabel, isUncategorized } from './email-classify-run'
import { sanitizeFetchHint } from './email-fetch-plan'
import { hasInboxSearch, matchInboxSearch, type InboxSearch } from './email-inbox-search'
import { selectedIdList, toggleSelect } from './email-inbox-select'
import { purgeEmailsLocal } from './email-soft-delete'
import * as emailsStore from './emails-store'
import type { LocalEmail } from './emails-store'

export function useEmailInbox() {
  const selectMode = ref(false)
  const selected = ref(new Set<string>())
  const searchOpen = ref(false)
  const search = ref<InboxSearch>({})
  const moreOpen = ref(false)
  const classifying = ref(false)
  const classifyHint = ref('')
  const classifyCancel = ref(false)
  /** 在途归类请求的中止器；点「取消」时 abort，让 HTTP 层真正断掉。 */
  const classifyAbort = ref<AbortController | null>(null)
  const purgeBusy = ref(false)

  const selectedCount = computed(() => selected.value.size)

  function visibleEmails(list: LocalEmail[]): LocalEmail[] {
    if (!hasInboxSearch(search.value)) return list
    return list.filter((m) => matchInboxSearch(m, search.value))
  }

  function enterSelect() {
    selectMode.value = true
    selected.value = new Set()
    moreOpen.value = false
  }

  function exitSelect() {
    selectMode.value = false
    selected.value = new Set()
  }

  function toggle(id: string) {
    selected.value = toggleSelect(selected.value, id)
  }

  function toggleSearch() {
    searchOpen.value = !searchOpen.value
    moreOpen.value = false
  }

  function confirmSearch() {
    searchOpen.value = false
  }

  function clearSearch() {
    search.value = {}
    searchOpen.value = false
  }

  async function confirmPurge(): Promise<string[]> {
    const ids = selectedIdList(selected.value)
    if (!ids.length) return []
    purgeBusy.value = true
    try {
      await purgeEmailsLocal(ids)
      try { await emailApi.purgeEmails(ids) } catch { /* 本地已删，服务端稍后重试 */ }
      exitSelect()
      return ids
    } finally {
      purgeBusy.value = false
    }
  }

  /**
   * 强行终止归类：既要停批间循环，也要真的 abort 在途 HTTP 请求。
   * 原实现只置 classifyCancel 标记，用户点「取消」后当前这批仍会跑满
   * （逐封调 LLM，单批可达分钟级），表现为「点了没反应」。
   */
  function cancelClassify() {
    classifyCancel.value = true
    classifyAbort.value?.abort()
  }

  async function runClassify(list: LocalEmail[]): Promise<LocalEmail[]> {
    if (classifying.value) return list
    classifying.value = true
    classifyCancel.value = false
    classifyHint.value = '正在归类…'
    const controller = new AbortController()
    classifyAbort.value = controller
    let next = list
    try {
      // 循环的终止判定放在 email-classify-loop.ts：那里有「零进展就停」和批次数
      // 上限，原来的 do/while 只判 remaining<=0，在分类器故障时会**无限打服务端**
      // （详见该模块头部与 backend/.../server_email_classify_progress_test.go）。
      const loop = await runClassifyLoop<EmailClassifyReport>({
        fetchBatch: () => emailApi.classifyInbox(20, controller.signal),
        onBatch: async (report) => {
          const done = report.classified ?? 0
          const remain = report.remaining ?? 0
          classifyHint.value = classifyProgressLabel(Math.max(1, done), done + remain)
          for (const row of report.results ?? []) {
            next = next.map((m) => applyClassifyResult(m, row))
            const category = normalizeEmailCategory(row.category)
            if (row.emailId && category && !row.error) {
              await emailsStore.setAiClassification(
                row.emailId, category, row.importance || '', row.summary || '', '',
              )
            }
          }
        },
        isCancelled: () => classifyCancel.value,
      })
      classifyHint.value = classifyStopHint(
        loop,
        next.filter((m) => isUncategorized(m.category)).length,
      )
    } catch (e) {
      if (controller.signal.aborted) {
        // 用户主动中止：已落库的部分保留，如实说明停在哪
        const leftover = next.filter((m) => isUncategorized(m.category)).length
        classifyHint.value = leftover ? `已取消，仍有 ${leftover} 封未归类` : '归类完成'
      } else {
        const raw = e instanceof Error ? e.message : '归类失败'
        const tr = (k: string, p?: Record<string, unknown>) =>
          (p ? i18n.global.t(k, p) : i18n.global.t(k)) as string
        classifyHint.value = sanitizeFetchHint(raw, tr) === raw ? raw : '归类中断，已保存已完成的分类'
      }
    } finally {
      classifyAbort.value = null
      classifying.value = false
    }
    return next
  }

  return {
    selectMode, selected, selectedCount, searchOpen, search, moreOpen,
    classifying, classifyHint, classifyCancel, purgeBusy,
    visibleEmails, enterSelect, exitSelect, toggle, toggleSearch, confirmSearch, clearSearch, confirmPurge,
    runClassify, cancelClassify,
  }
}
