/**
 * update-state 契约测试 —— 守住「无感更新」的本地状态机。
 *
 * 覆盖那些「写错了不会报错、只会让用户每天被同一个假更新弹窗拦住」的规则：
 *   - 归一版本号：构建变体后缀必须剥掉，否则服务端判成旧版
 *   - 限频：间隔内不查；换包/时钟回拨/强制 ⇒ 要查
 *   - 检查失败**不得**抹掉「有新版」这个事实
 *   - 预下载的包：版本或字节数对不上就作废
 *   - 持久化读回一律当不可信输入校验
 *
 * Run: node --test src/utils/__tests__/update-state.test.mjs
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import {
  FINGERPRINT_VERSION,
  DEFAULT_CHECK_INTERVAL_MS,
  emptyFingerprint,
  normalizeComparableVersion,
  validateFingerprint,
  decideCheck,
  describeAvailable,
  applyCheckResult,
  applyCheckFailure,
  applyPrefetched,
  hasKnownUpdate,
  canInstallPrefetched,
  readFingerprint,
  writeFingerprint,
} from '../update-state.ts'

const HOUR = 60 * 60 * 1000
const ID = { version: '1.2.0', buildNumber: 3 }

// ---------------------------------------------------------------------------
// 1. 归一：构建变体后缀必须剥掉
// ---------------------------------------------------------------------------
test('归一版本号剥掉 v 前缀 / +元数据 / -构建变体', () => {
  // gradle 写死的是 "1.2.0-openpocket"，sttdev 构建是 "1.2.0-sttdev"。
  // 服务端 splitVersion 把 `-xxx` 当预发布 ⇒ 原样上报会被判成更旧。
  assert.equal(normalizeComparableVersion('1.2.0-openpocket'), '1.2.0')
  assert.equal(normalizeComparableVersion('1.2.0-sttdev'), '1.2.0')
  assert.equal(normalizeComparableVersion('v1.2.0'), '1.2.0')
  assert.equal(normalizeComparableVersion('V1.2.0'), '1.2.0')
  assert.equal(normalizeComparableVersion('1.2.0+build7'), '1.2.0')
  assert.equal(normalizeComparableVersion('  1.2.0-openpocket  '), '1.2.0')
  assert.equal(normalizeComparableVersion(''), '')
})

test('归一后与服务端 splitVersion 对齐：1.2.0-openpocket 不再被判成更旧', () => {
  // 这条是「无感」的关键：归一前后同一个发布版，比较结果必须从
  // 「有更新」翻转成「无更新」。
  const raw = normalizeComparableVersion('1.2.0-openpocket')
  // 服务端 versionLess 的判定：数字分量全等 + a 无预发布后缀 ⇒ 不更旧
  const isLess = (a, b) => {
    const av = a.split('.'), bv = b.split('.')
    for (let i = 0; i < Math.max(av.length, bv.length); i++) {
      const an = Number(av[i] ?? 0), bn = Number(bv[i] ?? 0)
      if (an !== bn) return an < bn
    }
    return false
  }
  assert.equal(isLess(raw, '1.2.0'), false, '归一后不该再被判成更旧')
})

// ---------------------------------------------------------------------------
// 2. 限频裁决
// ---------------------------------------------------------------------------
test('限频：从未查过要查；间隔内不查；间隔到了要查', () => {
  const never = emptyFingerprint()
  assert.equal(decideCheck(never, { now: 1000, identity: ID }).check, true)

  const fp = { ...emptyFingerprint(), currentVersion: '1.2.0', currentBuild: 3, lastCheckedAt: 1000 }
  const within = decideCheck(fp, { now: 1000 + HOUR, identity: ID })
  assert.equal(within.check, false)
  assert.equal(within.reason, 'within-interval')

  const elapsed = decideCheck(fp, { now: 1000 + DEFAULT_CHECK_INTERVAL_MS, identity: ID })
  assert.equal(elapsed.check, true)
  assert.equal(elapsed.reason, 'interval-elapsed')
})

test('限频：换了 App 版本（刚升级完）必须重新查', () => {
  const fp = { ...emptyFingerprint(), currentVersion: '1.2.0', currentBuild: 3, lastCheckedAt: 1000 }
  // 同一 build 号但版本串变了（构建变体不同）
  const r = decideCheck(fp, { now: 1100, identity: { version: '1.3.0', buildNumber: 3 } })
  assert.equal(r.check, true)
  assert.equal(r.reason, 'identity-changed')
  // build 号变了
  const r2 = decideCheck(fp, { now: 1100, identity: { version: '1.2.0', buildNumber: 4 } })
  assert.equal(r2.reason, 'identity-changed')
})

test('限频：时钟被往前调 ⇒ 重新查（否则永久卡在「间隔内」）', () => {
  // lastCheckedAt 比 now 还大 ⇒ 用户把系统时间调到了未来
  const fp = { ...emptyFingerprint(), currentVersion: '1.2.0', currentBuild: 3, lastCheckedAt: 10_000_000 }
  const r = decideCheck(fp, { now: 1000, identity: ID })
  assert.equal(r.check, true, '时钟不可信时不能拿时间算「间隔内」')
  assert.equal(r.reason, 'clock-moved-back')
})

test('限频：force 永远查', () => {
  const fp = { ...emptyFingerprint(), currentVersion: '1.2.0', currentBuild: 3, lastCheckedAt: 1000 }
  assert.equal(decideCheck(fp, { now: 1100, identity: ID, force: true }).reason, 'forced')
})

// ---------------------------------------------------------------------------
// 3. 检查结果的合并
// ---------------------------------------------------------------------------
test('检查成功：有新版就记下来', () => {
  const fp = applyCheckResult(emptyFingerprint(), {
    now: 5000,
    identity: ID,
    result: { hasUpdate: true, latest: { version: '1.3.0', buildNumber: 4 } },
  })
  assert.equal(fp.latestVersion, '1.3.0')
  assert.equal(fp.latestBuild, 4)
  assert.equal(fp.lastCheckedAt, 5000)
  assert.equal(hasKnownUpdate(fp), true)
})

test('检查成功：服务端说没新版 ⇒ 清掉旧的新版记录', () => {
  const prev = { ...emptyFingerprint(), currentVersion: '1.2.0', currentBuild: 3, latestVersion: '1.3.0', latestBuild: 4, lastCheckedAt: 1 }
  const fp = applyCheckResult(prev, { now: 9000, identity: ID, result: { hasUpdate: false } })
  assert.equal(fp.latestVersion, null, '确认无新版后不该继续报有新版')
  assert.equal(fp.prefetchedApk, null, '目标没了，已预下载的包也作废')
  assert.equal(hasKnownUpdate(fp), false)
})

test('关键规则：检查失败**不得**抹掉「有新版」这个事实', () => {
  const prev = {
    ...emptyFingerprint(),
    currentVersion: '1.2.0', currentBuild: 3, lastCheckedAt: 1,
    latestVersion: '1.3.0', latestBuild: 4, forceUpdate: true,
    prefetchedApk: { path: 'u.apk', bytes: 100, version: '1.3.0', build: 4, savedAt: 2 },
  }
  const fp = applyCheckFailure(prev, { now: 5000, identity: ID })
  assert.equal(fp.latestVersion, '1.3.0', '网络抖一下不等于「已是最新」')
  assert.equal(fp.prefetchedApk?.path, 'u.apk')
  assert.equal(fp.forceUpdate, true, '强制更新标记不该被一次失败撤销')
  assert.equal(fp.lastCheckedAt, 5000, '但要推进时间戳，否则每次启动都重试')
})

// ---------------------------------------------------------------------------
// 4. 预下载 APK 的作废规则
// ---------------------------------------------------------------------------
test('预下载：版本与字节数都对得上 ⇒ 保留', () => {
  let fp = applyCheckResult(emptyFingerprint(), {
    now: 1, identity: ID,
    result: { hasUpdate: true, latest: { version: '1.3.0', buildNumber: 4, fileSize: 4200000 } },
  })
  fp = applyPrefetched(fp, { now: 2, path: 'opencode-pocket-1.3.0.apk', bytes: 4200000 })
  assert.equal(canInstallPrefetched(fp), true)

  // 下一次检查仍报同一个版本与同一个大小 ⇒ 继续复用
  const again = applyCheckResult(fp, {
    now: 10, identity: ID,
    result: { hasUpdate: true, latest: { version: '1.3.0', buildNumber: 4, fileSize: 4200000 } },
  })
  assert.equal(again.prefetchedApk?.path, 'opencode-pocket-1.3.0.apk', '同一个包不该被白扔重下')
})

test('预下载：服务端改了 fileSize 却没改版本号 ⇒ 旧包作废（可能装错版本）', () => {
  let fp = applyCheckResult(emptyFingerprint(), {
    now: 1, identity: ID,
    result: { hasUpdate: true, latest: { version: '1.3.0', buildNumber: 4, fileSize: 4200000 } },
  })
  fp = applyPrefetched(fp, { now: 2, path: 'a.apk', bytes: 4200000 })

  const next = applyCheckResult(fp, {
    now: 10, identity: ID,
    result: { hasUpdate: true, latest: { version: '1.3.0', buildNumber: 4, fileSize: 4300000 } },
  })
  assert.equal(next.prefetchedApk, null, '字节数对不上就不能拿旧的装')
})

test('预下载：没有已知目标版本时不接受包', () => {
  const fp = applyPrefetched(emptyFingerprint(), { now: 1, path: 'a.apk', bytes: 10 })
  assert.equal(fp.prefetchedApk, null, '没有目标版本的包装上去是随机版本')
})

test('canInstallPrefetched：预下载的包与当前目标不匹配时为 false', () => {
  const fp = {
    ...emptyFingerprint(), latestVersion: '1.3.0', latestBuild: 4,
    prefetchedApk: { path: 'a.apk', bytes: 1, version: '1.2.9', build: 3, savedAt: 1 },
  }
  assert.equal(canInstallPrefetched(fp), false)
})

// ---------------------------------------------------------------------------
// 5. 持久化：读回一律当不可信输入
// ---------------------------------------------------------------------------
function memStorage(seed) {
  const m = new Map(seed ? Object.entries(seed) : [])
  return {
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => m.set(k, String(v)),
    removeItem: (k) => m.delete(k),
    _m: m,
  }
}

test('持久化：写入后能读回同一个指纹', () => {
  const s = memStorage()
  const fp = applyCheckResult(emptyFingerprint(), {
    now: 7, identity: ID, result: { hasUpdate: true, latest: { version: '1.3.0', buildNumber: 4 } },
  })
  assert.equal(writeFingerprint(s, fp), true)
  const back = readFingerprint(s)
  assert.equal(back?.latestVersion, '1.3.0')
  assert.equal(back?.lastCheckedAt, 7)
})

test('持久化：半截 JSON / 错版本 / 坏结构 ⇒ 当作没有指纹，不半恢复', () => {
  assert.equal(readFingerprint(memStorage({ 'pocket:update-fingerprint': '{"version":2,"lastChe' })), null)
  assert.equal(validateFingerprint({ version: 1, lastCheckedAt: 0 }), null, '版本不符直接丢弃')
  assert.equal(validateFingerprint({ version: FINGERPRINT_VERSION, lastCheckedAt: NaN }), null)
  assert.equal(validateFingerprint({ version: FINGERPRINT_VERSION, lastCheckedAt: 1, latestBuild: 'x' }), null)
  // 预下载结构坏 ⇒ 整个预下载当没有，但其余字段仍可用（不是整体丢弃）
  const partlyBad = validateFingerprint({
    version: FINGERPRINT_VERSION, lastCheckedAt: 1, currentVersion: '1.2.0', currentBuild: 3,
    latestVersion: '1.3.0', latestBuild: 4, forceUpdate: false, prefetchedApk: { path: 'a.apk' },
  })
  assert.equal(partlyBad?.prefetchedApk, null)
  assert.equal(partlyBad?.latestVersion, '1.3.0', '坏的是预下载，不该连累已知的新版信息')
})

test('持久化：storage 不可用时静默降级（指纹是优化，不是正确性依赖）', () => {
  const throwing = {
    getItem() { throw new Error('denied') },
    setItem() { throw new Error('quota') },
  }
  assert.equal(readFingerprint(throwing), null)
  assert.equal(writeFingerprint(throwing, emptyFingerprint()), false)
  assert.equal(readFingerprint(null), null)
  assert.equal(writeFingerprint(null, emptyFingerprint()), false)
})

// ---------------------------------------------------------------------------
// 6. describeAvailable
// ---------------------------------------------------------------------------
test('describeAvailable：latest 缺失 ⇒ 「不知道」而不是「无更新」', () => {
  assert.deepEqual(describeAvailable(undefined), { hasUpdate: false, version: null, build: null })
  assert.deepEqual(describeAvailable(null), { hasUpdate: false, version: null, build: null })
  assert.deepEqual(describeAvailable({ version: '', buildNumber: 1 }), { hasUpdate: false, version: null, build: null })
  assert.deepEqual(
    describeAvailable({ version: '1.3.0-openpocket', buildNumber: 4 }),
    { hasUpdate: true, version: '1.3.0', build: 4 },
  )
})