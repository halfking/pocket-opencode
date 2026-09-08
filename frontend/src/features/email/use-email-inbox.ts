import { computed, ref } from 'vue'
import { emailApi } from '../../api/email'
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

  async function runClassify(list: LocalEmail[]): Promise<LocalEmail[]> {
    if (classifying.value) return list
    classifying.value = true
    classifyCancel.value = false
    classifyHint.value = '正在归类…'
    let next = list
    try {
      do {
        const report = await emailApi.classifyInbox(20)
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
        if (classifyCancel.value || remain <= 0) break
      } while (!classifyCancel.value)
      const leftover = next.filter((m) => isUncategorized(m.category)).length
      classifyHint.value = leftover ? `已暂停，仍有 ${leftover} 封未归类` : '归类完成'
    } catch (e) {
      const raw = e instanceof Error ? e.message : '归类失败'
      classifyHint.value = sanitizeFetchHint(raw) === raw ? raw : '归类中断，已保存已完成的分类'
    } finally {
      classifying.value = false
    }
    return next
  }

  return {
    selectMode, selected, selectedCount, searchOpen, search, moreOpen,
    classifying, classifyHint, classifyCancel, purgeBusy,
    visibleEmails, enterSelect, exitSelect, toggle, toggleSearch, confirmSearch, clearSearch, confirmPurge, runClassify,
  }
}
