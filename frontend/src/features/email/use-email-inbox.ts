import { computed, ref } from 'vue'
import { emailApi } from '../../api/email'
import { TimeoutError } from '../../api/http'
import i18n from '../../i18n'
import { normalizeEmailCategory } from './email-categories'
import { applyClassifyResult, classifyDoneHint, classifyProgressLabel, DEFAULT_CLASSIFY_MAX_ROUNDS, isUncategorized, MAX_NO_PROGRESS_PASSES, shouldContinueClassify } from './email-classify-run'
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
    let noProgressPasses = 0
    let stalled = false
    try {
      // 轮次上限与终止条件都在纯函数里（email-classify-run.ts），可单测。
      // 原实现在这里只有一个 `while (!classifyCancel)`：分类器逐封调 LLM，
      // 失败是常态，而后端逐封失败仍返回 200、remaining 不变 ⇒ 无限重试。
      const MAX_ROUNDS = DEFAULT_CLASSIFY_MAX_ROUNDS
      const PER_ROUND = 20
      let round = 0
      let firstError = ''
      let allFailed = false
      let continueLoop = true
      while (continueLoop) {
        round++
        const report = await emailApi.classifyInbox(PER_ROUND, controller.signal)
        const done = report.classified ?? 0
        const remain = report.remaining ?? 0
        const rows = report.results ?? []
        const errs = rows.map((r) => r.error).filter((e): e is string => !!e)
        classifyHint.value = classifyProgressLabel(Math.max(1, done), done + remain)
        if (errs.length > 0 && !firstError) firstError = errs[0]
        allFailed = rows.length > 0 && errs.length === rows.length
        for (const row of rows) {
          next = next.map((m) => applyClassifyResult(m, row))
          const category = normalizeEmailCategory(row.category)
          if (row.emailId && category && !row.error) {
            await emailsStore.setAiClassification(
              row.emailId, category, row.importance || '', row.summary || '', '',
            )
          }
        }
        // 零进展即停：没配 LLM provider 时 classified 恒为 0，remaining 也恒
        // 等于总数。shouldContinueClassify 的「整批全失败」分支要求 rowCount>0，
        // 覆盖不到「服务端一行都没返回」这种形态，于是循环会一直打到轮次上限
        // （20 轮 × 20 封）才停——不无限，但白烧 20 次请求，且最终提示说的是
        // 「达到单次上限」，把真正的原因（provider 没配）指错了方向。
        // 2026-10-02 模拟器实测：刷新一次收件箱，服务端日志每分钟多出上百行
        // 相同的 llmbff: no provider configured，界面进度条纹丝不动。
        // 连着 MAX_NO_PROGRESS_PASSES 轮一封都没归类成功即判定链路跑不通；
        // 留两轮而不是一轮，是为了容忍单次网络抖动。
        noProgressPasses = done > 0 ? 0 : noProgressPasses + 1
        if (noProgressPasses >= MAX_NO_PROGRESS_PASSES) {
          stalled = true
          break
        }
        continueLoop = shouldContinueClassify({
          round, maxRounds: MAX_ROUNDS, remaining: remain,
          cancelled: classifyCancel.value, rowCount: rows.length, errorCount: errs.length,
        })
      }
      const leftover = next.filter((m) => isUncategorized(m.category)).length
      classifyHint.value = classifyDoneHint({
        leftover,
        firstError: firstError ? sanitizeFetchHint(firstError) : '',
        // stalled 与 allFailed 在提示上同义：都是「这条链路一次都没跑通」，
        // 所以复用 classifyDoneHint 的「归类失败」分支，而不是新造一句文案。
        allFailed: allFailed || stalled,
        hitMaxRounds: round >= MAX_ROUNDS,
        maxRounds: MAX_ROUNDS,
        perRound: PER_ROUND,
      })
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
