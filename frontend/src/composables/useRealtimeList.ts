/**
 * useRealtimeList.ts — 🦞 实时列表订阅 composable
 *
 * 职责：
 *   - 把 store 层暴露的 server-push handler 包装成一个"轻量通知器"
 *   - 视图层不需要直接把新对象塞进列表（避免破坏滚动位置、动画状态）
 *   - 只累加计数；用户点按 banner 时由视图自己重新拉数据并清零
 *
 * 用法（视图层）：
 *   const { pendingCount, bannerVisible, refresh } = useRealtimeList({
 *     subscribe: (onItem) => registerNoteServerHandler(() => onItem()),
 *     refresh:   async () => { await listNotes().then(setNotes) },
 *   })
 *
 * 设计取舍：
 *   - 选择"banner + 显式刷新"而不是"自动插入列表"：
 *     自动插入会让用户失去当前阅读位置，且无法让用户对更新时机有掌控感。
 *   - 选择"轻量回调"而不是把整个 note/email 传进 composable：
 *     视图层只需要"有几条新东西"这个信号；具体数据视图自己拉最新即可。
 *   - 30 秒自动淡出 banner 但保留计数：避免持续打扰，同时下次 refresh 仍能拉取到。
 */
import { ref, onMounted, onUnmounted, type Ref } from 'vue'
import { registerNoteServerHandler } from '../features/notes/notes-store'
import { registerEmailClassifiedHandler } from '../features/email/emails-store'

/** composable 入参 */
export interface UseRealtimeListOptions {
  /**
   * 订阅 store 的 server-push handler。
   * 入参 `onItem` 是轻量回调：每次收到推送调用一次即可（计数 +1）。
   * 必须返回一个反注册函数（onUnmounted 时调用）。
   */
  subscribe: (onItem: () => void) => () => void
  /** 用户点按"刷新"时调用，视图层自己重新拉数据。 */
  refresh: () => Promise<void> | void
  /**
   * 可选：banner 自动淡出毫秒数。
   * 默认 30000（30 秒）。设为 0 表示不自动淡出。
   * 注意：淡出不重置 pendingCount，下次 refresh 时仍能感知。
   */
  autoFadeMs?: number
}

/** composable 返回值 */
export interface UseRealtimeListReturn {
  /** 当前累积的未读更新条数（refresh 后清零） */
  pendingCount: Ref<number>
  /** 最近一次推送到达的时间戳（毫秒）；首次为 0 */
  lastUpdateAt: Ref<number>
  /** banner 是否可见（false 时元素不渲染，且 CSS 控制 transform） */
  bannerVisible: Ref<boolean>
  /** 视图层点按 banner 后调用：执行 refresh + 清零 + 隐藏 */
  refresh: () => Promise<void>
}

/**
 * 通用实时列表订阅器。
 * 用 `registerXxxServerHandler(() => onItem())` 把 store 的推送转成计数信号。
 */
export function useRealtimeList(opts: UseRealtimeListOptions): UseRealtimeListReturn {
  const pendingCount = ref(0)
  const lastUpdateAt = ref(0)
  const bannerVisible = ref(false)

  let unsub: () => void = () => {}
  let fadeTimer: ReturnType<typeof setTimeout> | null = null

  /** 自动淡出毫秒数；显式传 0 表示禁用 */
  const fadeMs = opts.autoFadeMs === 0 ? null : (opts.autoFadeMs ?? 30000)

  function bump() {
    pendingCount.value += 1
    lastUpdateAt.value = Date.now()
    bannerVisible.value = true

    if (fadeMs !== null) {
      if (fadeTimer) clearTimeout(fadeTimer)
      fadeTimer = setTimeout(() => {
        // 仅淡出，不丢计数；下次 refresh 时仍能感知
        bannerVisible.value = false
      }, fadeMs)
    }
  }

  async function refresh() {
    try {
      await opts.refresh()
    } finally {
      pendingCount.value = 0
      bannerVisible.value = false
      if (fadeTimer) {
        clearTimeout(fadeTimer)
        fadeTimer = null
      }
    }
  }

  onMounted(() => {
    unsub = opts.subscribe(bump)
  })

  onUnmounted(() => {
    unsub()
    if (fadeTimer) {
      clearTimeout(fadeTimer)
      fadeTimer = null
    }
  })

  return {
    pendingCount,
    lastUpdateAt,
    bannerVisible,
    refresh,
  }
}

/**
 * 便捷封装：订阅 notes 的 server-push 事件。
 * 视图层只需传入自己的 refresh 函数。
 */
export function useNotesRealtime(refresh: () => Promise<void> | void): UseRealtimeListReturn {
  return useRealtimeList({
    subscribe: (onItem) => registerNoteServerHandler(() => onItem()),
    refresh,
  })
}

/**
 * 便捷封装：订阅 emails 的 server-push 事件。
 * 视图层只需传入自己的 refresh 函数。
 */
export function useEmailsRealtime(refresh: () => Promise<void> | void): UseRealtimeListReturn {
  return useRealtimeList({
    subscribe: (onItem) => registerEmailClassifiedHandler(() => onItem()),
    refresh,
  })
}