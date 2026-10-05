/**
 * update-state.ts — 更新通道的**本地指纹**与限频裁决（纯逻辑，无 IO）。
 *
 * ## 它解决什么
 *
 * 用户要的是「无感更新」：不该在每次冷启动弹一个模态框把用户拦住，
 * 也不该每次启动都打一次服务端。而本仓原来的 `UpdateChecker.vue` 是
 * `onMounted` 里直接 `checkUpdate()` → 有新版就 `showUpdateDialog = true`
 * ——既没有限频，也没有本地记忆，于是「有新版」这个事实每次冷启动
 * 都重新弹一次，且服务端每次都被冷启动打一次。
 *
 * 本模块把「什么时候该查」「查到什么该记」「记住的东西什么时候作废」
 * 三件事从 Vue 组件里抽出来，做成可单测的纯函数。
 *
 * ## 为什么单独一个模块而不是塞进 version.ts
 *
 * version.ts 负责**与设备/服务端对话**（读 BuildConfig、发 HTTP），
 * 本模块负责**本地状态机**。两者混在一起时，「限频」这种纯时间算术
 * 就只能靠 mock 定时器来测，抽出来之后时间完全由入参决定。
 *
 * ## 三条容易写错、因此在实现里显式成立的规则
 *
 * 1. **时钟回拨不得把限频卡死。** 用户把系统时间往前调一天，
 *    `lastCheckedAt - now` 变成巨大正数 ⇒ 「距上次检查才过了很久」
 *    ⇒ 永远不再检查。判据：`lastCheckedAt > now` 一律视为不可信，重新查。
 * 2. **检查失败不得抹掉「有新版」这个事实。** 网络抖一下就把已知的
 *    新版信息清空，用户下次进设置看到「已是最新」——错的信息比没有更糟
 *    （与后端 version_config 那条注释同一个立场）。
 * 3. **换了 App 版本 ⇒ 指纹作废。** 设备刚升级完，旧的「有新版」记录
 *    已经不成立（那个新版就是它自己），必须重新查一次。
 *
 * 持久化读回一律当**不可信输入**校验：localStorage 同源可改写，
 * 也可能损坏。schema 不过就当没有指纹，而不是「尽力修复」。
 */

export const FINGERPRINT_VERSION = 2
export const FINGERPRINT_STORAGE_KEY = 'pocket:update-fingerprint'

/** 默认检查间隔：6 小时。冷启动通常一天好几次，6h 足够新鲜又不至于打爆服务端。 */
export const DEFAULT_CHECK_INTERVAL_MS = 6 * 60 * 60 * 1000

/** 设备上已预下载、可直接安装的 APK。 */
export interface PrefetchedApk {
  /** 相对 PocketFilesystem 的 data 目录的路径。 */
  path: string
  /** 落盘时的字节数。 */
  bytes: number
  /** 对应的服务端版本号（已归一）。 */
  version: string
  build: number
  savedAt: number
}

export interface UpdateFingerprint {
  version: typeof FINGERPRINT_VERSION
  /** 上次**成功**发起检查的时刻（epoch ms）。0 = 从未查过。 */
  lastCheckedAt: number
  /** 设备自己的真实身份（已归一）。指纹与它不符 ⇒ 换包了，指纹作废。 */
  currentVersion: string
  currentBuild: number
  /** 已知存在的最新版本；null = 不知道（不是「已是最新」）。 */
  latestVersion: string | null
  latestBuild: number | null
  forceUpdate: boolean
  prefetchedApk: PrefetchedApk | null
}

export function emptyFingerprint(): UpdateFingerprint {
  return {
    version: FINGERPRINT_VERSION,
    lastCheckedAt: 0,
    currentVersion: '',
    currentBuild: 0,
    latestVersion: null,
    latestBuild: 0,
    forceUpdate: false,
    prefetchedApk: null,
  }
}

/**
 * 归一版本号：去掉 `v` 前缀、`+build` 元数据、`-变体` 后缀。
 *
 * 为什么必须归一：服务端 `splitVersion`（app_version_compare.go:111）
 * 把 `-xxx` 当**预发布**，于是 `1.2.0-openpocket` 会被判成**比 `1.2.0` 旧**
 * ⇒ 明明是同一个发布版，客户端每次都被告知「有更新」。
 * 这正是 `app-version-identity.test.mjs` 里记的那条约束
 * （「比较层必须留在常量上」）的另一半解法：**不是退回常量，而是先归一再上报**。
 *
 * 归一规则与服务端 `splitVersion` 对齐，保证两边看到的是同一个字符串。
 */
export function normalizeComparableVersion(raw: string): string {
  let s = String(raw ?? '').trim()
  if (s === '') return ''
  if (s[0] === 'v' || s[0] === 'V') s = s.slice(1)
  const plus = s.indexOf('+')
  if (plus >= 0) s = s.slice(0, plus)
  const dash = s.indexOf('-')
  if (dash >= 0) s = s.slice(0, dash)
  return s.trim()
}

/** schema 校验。localStorage 内容同源可改写，也可能半截 JSON。 */
export function validateFingerprint(input: unknown): UpdateFingerprint | null {
  if (typeof input !== 'object' || input === null) return null
  const o = input as Record<string, unknown>
  if (o.version !== FINGERPRINT_VERSION) return null
  if (typeof o.lastCheckedAt !== 'number' || !Number.isFinite(o.lastCheckedAt)) return null
  if (typeof o.currentVersion !== 'string') return null
  if (typeof o.currentBuild !== 'number' || !Number.isFinite(o.currentBuild)) return null
  if (o.latestVersion !== null && typeof o.latestVersion !== 'string') return null
  if (o.latestBuild !== null && (typeof o.latestBuild !== 'number' || !Number.isFinite(o.latestBuild))) return null
  if (typeof o.forceUpdate !== 'boolean') return null

  let prefetchedApk: PrefetchedApk | null = null
  if (o.prefetchedApk !== null && o.prefetchedApk !== undefined) {
    const p = o.prefetchedApk as Record<string, unknown>
    if (
      typeof p?.path === 'string' && p.path !== '' &&
      typeof p.bytes === 'number' && Number.isFinite(p.bytes) && p.bytes > 0 &&
      typeof p.version === 'string' &&
      typeof p.build === 'number' && Number.isFinite(p.build) &&
      typeof p.savedAt === 'number' && Number.isFinite(p.savedAt)
    ) {
      prefetchedApk = { path: p.path, bytes: p.bytes, version: p.version, build: p.build, savedAt: p.savedAt }
    }
    // 结构不对就当没预下载过，不抛错也不半恢复。
  }

  return {
    version: FINGERPRINT_VERSION,
    lastCheckedAt: o.lastCheckedAt,
    currentVersion: o.currentVersion,
    currentBuild: o.currentBuild,
    latestVersion: o.latestVersion,
    latestBuild: o.latestBuild,
    forceUpdate: o.forceUpdate,
    prefetchedApk,
  }
}

export type CheckDecisionReason =
  | 'never-checked'
  | 'interval-elapsed'
  | 'identity-changed'
  | 'clock-moved-back'
  | 'forced'
  | 'within-interval'
  | 'clock-suspect'

/**
 * 要不要现在查一次更新。
 *
 * 判据优先级：forced > 换包 > 从未查过/时钟可疑 > 间隔已过 > 不查。
 * 「时钟可疑」排在「间隔已过」之前，因为时钟不可信时**任何**基于
 * 时间的结论都不可信，宁可多查一次。
 */
export function decideCheck(
  fp: UpdateFingerprint,
  opts: { now: number; identity: { version: string; buildNumber: number }; intervalMs?: number; force?: boolean },
): { check: boolean; reason: CheckDecisionReason } {
  const interval = opts.intervalMs ?? DEFAULT_CHECK_INTERVAL_MS
  const curVersion = normalizeComparableVersion(opts.identity.version)

  if (opts.force) return { check: true, reason: 'forced' }
  if (fp.currentVersion !== curVersion || fp.currentBuild !== opts.identity.buildNumber) {
    return { check: true, reason: 'identity-changed' }
  }
  if (fp.lastCheckedAt <= 0) return { check: true, reason: 'never-checked' }
  if (fp.lastCheckedAt > opts.now) return { check: true, reason: 'clock-moved-back' }
  if (opts.now - fp.lastCheckedAt >= interval) return { check: true, reason: 'interval-elapsed' }
  return { check: false, reason: 'within-interval' }
}

/** 服务端说「有更新」时该记什么。 */
export function describeAvailable(latest: { version: string; buildNumber: number } | undefined | null): {
  hasUpdate: boolean
  version: string | null
  build: number | null
} {
  if (!latest || !latest.version) return { hasUpdate: false, version: null, build: null }
  return { hasUpdate: true, version: normalizeComparableVersion(latest.version), build: latest.buildNumber }
}

/**
 * 合并一次**成功**的检查结果。
 *
 * 预下载 APK 的作废规则：只有当服务端仍在报同一个版本、且字节数对得上时
 * 才继续复用。服务端改了 fileSize 却没改版本号 ⇒ 旧的那份已不是它说的那个包，
 * 留着就可能装错版本。
 */
export function applyCheckResult(
  prev: UpdateFingerprint,
  args: {
    now: number
    identity: { version: string; buildNumber: number }
    result: { hasUpdate: boolean; latest?: { version: string; buildNumber: number; fileSize?: number } | null; forceUpdate?: boolean }
  },
): UpdateFingerprint {
  const curVersion = normalizeComparableVersion(args.identity.version)
  const next: UpdateFingerprint = {
    ...prev,
    version: FINGERPRINT_VERSION,
    lastCheckedAt: args.now,
    currentVersion: curVersion,
    currentBuild: args.identity.buildNumber,
    latestVersion: null,
    latestBuild: null,
    forceUpdate: args.result.forceUpdate === true,
    prefetchedApk: null,
  }
  if (!args.result.hasUpdate || !args.result.latest) return next

  const avail = describeAvailable(args.result.latest)
  next.latestVersion = avail.version
  next.latestBuild = avail.build
  next.forceUpdate = args.result.forceUpdate === true

  // 服务端仍报同一个版本且字节数一致 ⇒ 保留已预下载的包。
  const p = prev.prefetchedApk
  if (
    p &&
    p.version === avail.version &&
    p.build === (avail.build ?? 0) &&
    (args.result.latest.fileSize === undefined || args.result.latest.fileSize === p.bytes)
  ) {
    next.prefetchedApk = p
  }
  return next
}

/**
 * 合并一次**失败**的检查。
 *
 * 关键：只推进 lastCheckedAt，**不清空** latestVersion / prefetchedApk。
 * 网络抖一下就把「有新版」抹掉，用户看到的就成了假的「已是最新」。
 * forceUpdate 也保留——服务端上次说强制更新，那件事还没被撤销。
 */
export function applyCheckFailure(
  prev: UpdateFingerprint,
  args: { now: number; identity: { version: string; buildNumber: number } },
): UpdateFingerprint {
  return {
    ...prev,
    version: FINGERPRINT_VERSION,
    lastCheckedAt: args.now,
    currentVersion: normalizeComparableVersion(args.identity.version),
    currentBuild: args.identity.buildNumber,
  }
}

/** 记一次预下载成功。 */
export function applyPrefetched(
  prev: UpdateFingerprint,
  args: { now: number; path: string; bytes: number },
): UpdateFingerprint {
  if (!prev.latestVersion) return prev // 没有已知目标版本就不该有包
  return {
    ...prev,
    prefetchedApk: {
      path: args.path,
      bytes: args.bytes,
      version: prev.latestVersion,
      build: prev.latestBuild ?? 0,
      savedAt: args.now,
    },
  }
}

/** 已知存在新版（用于决定要不要提示）。注意「不知道」与「没有」是两回事。 */
export function hasKnownUpdate(fp: UpdateFingerprint): boolean {
  return fp.latestVersion !== null
}

/** 预下载的包现在还能用吗（版本与目标一致、未被作废）。 */
export function canInstallPrefetched(fp: UpdateFingerprint): boolean {
  const p = fp.prefetchedApk
  if (!p) return false
  if (!fp.latestVersion) return false
  return p.version === fp.latestVersion && p.build === (fp.latestBuild ?? 0)
}

/** 读回持久化。损坏/版本不符 ⇒ null（当作没有指纹）。 */
export function readFingerprint(storage: Storage | null): UpdateFingerprint | null {
  if (!storage) return null
  let raw: string | null = null
  try {
    raw = storage.getItem(FINGERPRINT_STORAGE_KEY)
  } catch {
    return null
  }
  if (!raw) return null
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    return null
  }
  return validateFingerprint(parsed)
}

/** 写入。配额满/隐私模式 ⇒ 静默降级，指纹只是优化，不是正确性依赖。 */
export function writeFingerprint(storage: Storage | null, fp: UpdateFingerprint): boolean {
  if (!storage) return false
  try {
    storage.setItem(FINGERPRINT_STORAGE_KEY, JSON.stringify(fp))
    return true
  } catch {
    return false
  }
}

/** 安全取 localStorage（隐私模式/禁用 cookie 时为 null）。 */
export function safeLocalStorage(): Storage | null {
  if (typeof window === 'undefined') return null
  try {
    const s = window.localStorage
    const probe = '__pocket_update_probe__'
    s.setItem(probe, '1')
    s.removeItem(probe)
    return s
  } catch {
    return null
  }
}