/**
 * useSessionDrafts — 会话输入草稿 composable（P1 输入系统，设计 v2 §4.4-5）。
 *
 * 职责：
 *   - 按 sessionId 读写草稿（load / 500ms 防抖落盘 / send 时 clear）
 *   - 存储运行时解析：SQLite（龙虾库已解锁）→ 内存 Map 降级，永不抛错
 *   - 会话切换（targets 模式）时旧草稿先落盘、新会话草稿再载入
 *
 * 本文件同时承载 SessionComposer 的可测纯逻辑（工程无组件测试基建，
 * 行为规则集中在此供 node --test 覆盖）：
 *   - QUICK_COMMANDS / shouldConfirmCommand：指令模板与"停下"二次确认
 *   - appendToDraft / applyInitialText：转写与 ?prompt= 预填的合并纪律
 *     （追加到现有草稿后，不直发、不覆盖用户已输入内容）
 *   - truncateChipLabel：目标 chip 截断
 */

import { ref, watch, onBeforeUnmount, getCurrentInstance, type Ref } from 'vue'
import {
  MemoryDraftStore,
  SqliteDraftStore,
  type DraftStore,
} from '../../native/draftStore.ts'

/** 输入防抖落盘窗口（契约 §4：500ms）。 */
export const DRAFT_SAVE_DEBOUNCE_MS = 500

/** 目标 chip 截断长度（契约 §4：~12 字符）。 */
export const CHIP_LABEL_MAX = 12

// ─────────────────────────────────────────────────────────────
// 纯逻辑（可测）
// ─────────────────────────────────────────────────────────────

export interface QuickCommand {
  /** chip 展示文案。 */
  label: string
  /** emit('send', message) 的模板文本。 */
  message: string
  /** 非空 = 点击需先二次确认（仅"停下"）。 */
  confirmText?: string
}

/** 指令模板（契约 §4 冻结文案与顺序）。 */
export const QUICK_COMMANDS: readonly QuickCommand[] = [
  { label: '继续', message: '继续' },
  { label: '停下', message: '停下', confirmText: '确定要停止当前任务吗？停止后本轮输出不会继续。' },
  { label: '总结当前进展', message: '总结当前进展' },
  { label: '跑测试', message: '跑测试' },
  { label: '忽略错误继续', message: '忽略错误继续' },
]

/** 仅 confirmText 非空的指令（"停下"）需要二次确认。 */
export function shouldConfirmCommand(cmd: QuickCommand): boolean {
  return cmd.confirmText !== undefined
}

/**
 * 追加文本到草稿（voice/STT 转写、深链预填共用）：
 * 已有内容时以空格衔接，空草稿直接填充——与 P0 会话页转写纪律一致。
 */
export function appendToDraft(current: string, addition: string): string {
  const add = addition.trim()
  if (!add) return current
  return current ? `${current.trimEnd()} ${add}` : add
}

/** initialText 一次性预填（同 appendToDraft，语义别名便于阅读）。 */
export function applyInitialText(current: string, initial: string): string {
  return appendToDraft(current, initial)
}

/** 目标 chip 截断：超长以 … 结尾。 */
export function truncateChipLabel(label: string, max: number = CHIP_LABEL_MAX): string {
  return label.length > max ? `${label.slice(0, max)}…` : label
}

// ─────────────────────────────────────────────────────────────
// 存储运行时解析
// ─────────────────────────────────────────────────────────────

let sharedMemoryStore: MemoryDraftStore | null = null

/** 内存降级存储的进程级单例：同页多实例共享，切页草稿不丢。 */
export function sharedMemoryDraftStore(): MemoryDraftStore {
  if (sharedMemoryStore === null) sharedMemoryStore = new MemoryDraftStore()
  return sharedMemoryStore
}

let cachedSqliteStore: DraftStore | null = null

/**
 * 解析草稿存储：龙虾库已解锁 → SQLite；否则（web 未解锁 / 无 sqlDb 基建）
 * 降级内存，不抛错。动态 import 隔离 Capacitor 依赖，保证 Node 测试可加载。
 */
export async function resolveDraftStore(): Promise<DraftStore> {
  if (cachedSqliteStore !== null) return cachedSqliteStore
  try {
    const [{ isLobsterReady }, { localDB, localDbAsSql }] = await Promise.all([
      import('../../native/lobster-init.ts'),
      import('../../native/local-db.ts'),
    ])
    if (isLobsterReady()) {
      cachedSqliteStore = new SqliteDraftStore(localDbAsSql(localDB))
      return cachedSqliteStore
    }
  } catch {
    // web / 测试环境没有 SQLite 基建：降级内存
  }
  return sharedMemoryDraftStore()
}

// ─────────────────────────────────────────────────────────────
// composable
// ─────────────────────────────────────────────────────────────

export interface UseSessionDraftsOptions {
  /** 草稿 key（固定模式 = sessionId；targets 模式 = 当前选中目标 id）。 */
  sessionId: () => string
  /** 防抖窗口，默认 500ms；测试可注入小值。 */
  debounceMs?: number
  /** 注入存储（测试）；缺省运行时解析（SQLite → 内存降级）。 */
  store?: DraftStore
  /** 时钟注入（测试）。 */
  now?: () => number
}

export interface UseSessionDraftsReturn {
  /** 草稿文本（v-model 目标）。 */
  text: Ref<string>
  /** 从存储载入草稿；仅在 text 为空时填充，不覆盖预填/已输入内容。 */
  hydrate(): Promise<void>
  /** 立即落盘（取消挂起的防抖）；卸载与发送前调用。 */
  flush(): Promise<void>
  /** 清空草稿：text 置空 + 删行 + 取消挂起的防抖（send 时调用）。 */
  clear(): Promise<void>
}

export function useSessionDrafts(options: UseSessionDraftsOptions): UseSessionDraftsReturn {
  const text = ref('')
  const debounceMs = options.debounceMs ?? DRAFT_SAVE_DEBOUNCE_MS
  const now = options.now ?? Date.now

  let store: DraftStore | null = options.store ?? null
  let resolving: Promise<DraftStore> | null = null
  let saveTimer: ReturnType<typeof setTimeout> | null = null

  async function ensureStore(): Promise<DraftStore> {
    if (store !== null) return store
    if (resolving === null) {
      resolving = resolveDraftStore().then((resolved) => {
        store = resolved
        return resolved
      })
    }
    return resolving
  }

  function cancelPendingSave(): void {
    if (saveTimer !== null) {
      clearTimeout(saveTimer)
      saveTimer = null
    }
  }

  /** 载入草稿：空文本才填充，避免覆盖 initialText 预填与用户已输入。 */
  async function hydrate(): Promise<void> {
    const key = options.sessionId()
    try {
      const row = await (await ensureStore()).getDraft(key)
      if (row !== null && row.text !== '' && text.value === '') {
        text.value = row.text
      }
    } catch {
      // 草稿是增强能力，读失败静默降级为空草稿
    }
  }

  /** 把当前 text 写到指定 key（空文本 → 删行，不留空草稿）。 */
  async function persistTo(key: string): Promise<void> {
    try {
      const s = await ensureStore()
      if (text.value.trim() === '') await s.clearDraft(key)
      else await s.saveDraft(key, text.value, now())
    } catch {
      // 写失败不阻断输入
    }
  }

  function scheduleSave(): void {
    cancelPendingSave()
    saveTimer = setTimeout(() => {
      saveTimer = null
      void persistTo(options.sessionId())
    }, debounceMs)
  }

  async function flush(): Promise<void> {
    cancelPendingSave()
    await persistTo(options.sessionId())
  }

  async function clear(): Promise<void> {
    cancelPendingSave()
    const key = options.sessionId()
    text.value = ''
    try {
      await (await ensureStore()).clearDraft(key)
    } catch {
      // 清失败不阻断发送流程
    }
  }

  // 输入 → 防抖落盘
  watch(text, scheduleSave)

  // 草稿 key 切换（targets 模式换目标）：旧 key 先落盘，再载入新 key
  watch(
    () => options.sessionId(),
    (next, prev) => {
      if (next === prev) return
      const pendingText = text.value
      cancelPendingSave()
      text.value = ''
      if (prev !== undefined && prev !== next) {
        // 旧会话草稿立即落盘（带旧 key）；空文本则顺手清行
        if (pendingText.trim() === '') {
          void ensureStore().then((s) => s.clearDraft(prev)).catch(() => undefined)
        } else {
          void ensureStore()
            .then((s) => s.saveDraft(prev, pendingText, now()))
            .catch(() => undefined)
        }
      }
      void hydrate()
    },
  )

  // 卸载兜底落盘（切走/关页不丢草稿；fire-and-forget，写入在 JS 侧排队）。
  // 组件外调用（Node 测试）无卸载钩子，守卫掉 Vue 的无实例告警。
  if (getCurrentInstance() !== null) {
    onBeforeUnmount(() => {
      void flush()
    })
  }

  // 挂载即异步载入（不阻塞首帧）
  void hydrate()

  return { text, hydrate, flush, clear }
}
