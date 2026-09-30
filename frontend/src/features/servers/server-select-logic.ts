/**
 * 后端服务器选择页的纯决策逻辑（从 ServerSelectView.vue 抽出，便于单测）。
 *
 * 背景：用户在真机把「自定义地址」保存后，重新加载时登录页底部又显示
 * https://localhost，看起来像地址没存住。这里把「读当前选择 / 落盘 / 是否
 * 需要整页重载」三步拆成可测函数，避免靠肉眼看页面下结论。
 */
import {
  PRODUCTION_API_BASE,
  BACKUP_API_BASE,
  API_BASE_STORAGE_KEY,
  isCapacitorShellOrigin,
  normalizeApiBase,
  readApiBaseOverride,
  resolveApiBase,
  type StorageLike,
} from '../../config/api-base.ts'

export type ServerChoiceKind = 'build' | 'origin' | 'production' | 'backup' | 'custom'

export interface ServerChoice {
  kind: ServerChoiceKind
  custom: string
}

/** 由 localStorage 中的 override 反推当前选中的预置项。 */
export function detectServerChoice(
  override: string | null,
  buildDefault: string,
): ServerChoice {
  if (override === null) return { kind: buildDefault ? 'build' : 'origin', custom: '' }
  if (override === '') return { kind: 'origin', custom: '' }
  if (override === PRODUCTION_API_BASE) return { kind: 'production', custom: '' }
  if (override === BACKUP_API_BASE) return { kind: 'backup', custom: '' }
  return { kind: 'custom', custom: override }
}

/** 当前选择对应的实际生效地址（未落盘前的预览）。 */
export function previewServerBase(choice: ServerChoice, buildDefault: string, pageOrigin: string): string {
  switch (choice.kind) {
    case 'build':
      return buildDefault ? normalizeApiBase(buildDefault) : ''
    case 'origin':
      // The browser uses /api on its origin; the native localhost shell needs
      // the configured backend instead. Match resolveApiBase in both cases.
      return isCapacitorShellOrigin(pageOrigin) && buildDefault
        ? normalizeApiBase(buildDefault)
        : ''
    case 'production':
      return PRODUCTION_API_BASE
    case 'backup':
      return BACKUP_API_BASE
    default:
      return normalizeApiBase(choice.custom, pageOrigin)
  }
}

/**
 * 落盘一个选择，返回归一化后要写入 localStorage 的值。
 * build 档写 null（删除 key），其余写归一化字符串——空串是「与页面同源」的
 * 显式选择，不能与「未设置」混为一谈。
 */
export function serverChoiceToPersistValue(choice: ServerChoice, buildDefault: string, pageOrigin: string): string | null {
  if (choice.kind === 'build') return null
  if (choice.kind === 'origin') return ''
  if (choice.kind === 'production') return PRODUCTION_API_BASE
  if (choice.kind === 'backup') return BACKUP_API_BASE
  return normalizeApiBase(choice.custom, pageOrigin)
}

export interface ServerSaveOutcome {
  /** 落盘值；null 表示要删除 key。 */
  persistValue: string | null
  /** 落盘后重新解析出的生效地址。 */
  resolved: string
  /** 相对落盘前是否发生变化（变化才需要清 session + 整页重载）。 */
  changed: boolean
  /** 落盘后是否仍然是「同源/未设置」——自定义地址丢失时为 true。 */
  fellBackToOrigin: boolean
}

/**
 * 完整走一遍「点保存并使用」。
 * next 始终重新从 storage 读取（而不是复用刚算出的值），
 * 这样「以为存住了、其实没存住」的情况会被 changed/fellBackToOrigin 如实反映。
 */
export function resolveServerSave(
  choice: ServerChoice,
  opts: {
    buildDefault: string
    pageOrigin: string
    storage: StorageLike
  },
): ServerSaveOutcome {
  const before = resolveApiBase({ storage: opts.storage, buildDefault: opts.buildDefault, pageOrigin: opts.pageOrigin })
  const persistValue = serverChoiceToPersistValue(choice, opts.buildDefault, opts.pageOrigin)
  if (persistValue === null) opts.storage.removeItem(API_BASE_STORAGE_KEY)
  else opts.storage.setItem(API_BASE_STORAGE_KEY, persistValue)

  const after = readApiBaseOverride(opts.storage)
  const resolved = resolveApiBase({ storage: opts.storage, buildDefault: opts.buildDefault, pageOrigin: opts.pageOrigin })
  return {
    persistValue,
    resolved,
    changed: before !== resolved,
    fellBackToOrigin: after === null || after === '',
  }
}
