import { computed, ref } from 'vue'
import { emailApi, type EmailClassifyReport } from '../../api/email'
// 合并说明：main 侧还有一个 TimeoutError 的 import，解冲突时取了本分支这一侧
// 把它弄丢了，而下面 classify 那段的 AbortError 分支仍在用它（typecheck 报
// TS2304）。两侧的 import 取并集。
import { TimeoutError } from '../../api/http'
import i18n from '../../i18n'
import { classifyStopHint, runClassifyLoop } from './email-classify-loop'
import { normalizeEmailCategory } from './email-categories'
import { applyClassifyResult, classifyDoneHint, classifyProgressLabel, DEFAULT_CLASSIFY_MAX_ROUNDS, isUncategorized, shouldContinueClassify } from './email-classify-run'
import { cancelClassifyRun, emailJobs, finishClassifyRun, startClassifyRun } from './email-job-runtime'
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
  const purgeBusy = ref(false)

  // 归类作业的状态与中止器放在**进程级单例**里（见 email-job-runtime.ts）：
  // 早先是本 composable 的局部 ref，于是切页卸载后进度与「取消」按钮一起
  // 消失，而后台循环照跑；切回来是新实例、看着像没跑过，再点一次就起第二个
  // 并发作业。需求「切页后仍能执行 + 后台 api 可强行终止」两条都因此落空。
  const { running: classifying, hint: classifyHint, cancelRequested: classifyCancel } = emailJobs.classify

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
   *
   * 与局部 ref 版本的另一个差别：中止器在进程级单例上，所以切页之后回来点
   * 「取消」依然能停掉**正在跑的那一轮**，而不是一个已经作废的旧实例的请求。
   */
  function cancelClassify() {
    cancelClassifyRun()
  }

  async function runClassify(list: LocalEmail[]): Promise<LocalEmail[]> {
    if (classifying.value) return list
    classifyHint.value = '正在归类…'
    const controller = startClassifyRun()
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
      const leftover = next.filter((m) => isUncategorized(m.category)).length
      if (e instanceof TimeoutError) {
        // 2026-10-03：这一支原先落到 else，于是把 http 层抛的
        // `请求超时（120s）：/api/emails/classify` 原样显示给用户。
        // 那是带路由名的技术串——用户既不知道发生了什么，也不知道该做什么。
        // 而且它掩盖了真正的事实：**服务端是被我们断连杀掉的**，不是它坏了。
        // 已落库的部分照实保留并说明停在哪。
        classifyHint.value = leftover
          ? `归类超时中断，仍有 ${leftover} 封未归类（已完成的已保存，可再点一次继续）`
          : '归类完成'
      } else if (controller.signal.aborted) {
        // 用户主动中止：已落库的部分保留，如实说明停在哪
        classifyHint.value = leftover ? `已取消，仍有 ${leftover} 封未归类` : '归类完成'
      } else {
        const raw = e instanceof Error ? e.message : '归类失败'
        const tr = (k: string, p?: Record<string, unknown>) =>
          (p ? i18n.global.t(k, p) : i18n.global.t(k)) as string
        classifyHint.value = sanitizeFetchHint(raw, tr) === raw ? raw : '归类中断，已保存已完成的分类'
      }
    } finally {
      finishClassifyRun()
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
