/**
 * Run: node --test src/features/email/email-fetch-run.test.ts
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { decideFetchKick, emailFetchStages, formatFetchHint, resolveFetchApiBase, sanitizeFetchHint, shouldRetryFullListPull } from './email-fetch-plan.ts'

describe('delegated email fetch', () => {
  it('formats sync hint and optional classify count', () => {
    assert.equal(formatFetchHint('已同步 1 个账户，新邮件 2', 0), '已同步 1 个账户，新邮件 2')
    assert.equal(formatFetchHint('已同步 1 个账户，新邮件 2', 3), '已同步 1 个账户，新邮件 2，已归类 3')
  })

  it('节流：成功后走长间隔，失败后走短重试间隔', () => {
    const gap = { successGapMs: 60_000, retryGapMs: 10_000 }
    // 从未发起 → 立刻跑（这就是冷启动那次 kick）
    assert.equal(decideFetchKick({ lastAttemptAt: 0, lastAttemptFailed: false, inFlight: false }, 1000, gap), 'run')
    // 上一次成功，60s 未到 → 压住
    assert.equal(decideFetchKick({ lastAttemptAt: 1000, lastAttemptFailed: false, inFlight: false }, 30_000, gap), 'throttled')
    // 上一次成功，60s 到了 → 跑
    assert.equal(decideFetchKick({ lastAttemptAt: 1000, lastAttemptFailed: false, inFlight: false }, 70_000, gap), 'run')
    // 上一次失败：10s 未到 → 仍压住（防抖）
    assert.equal(decideFetchKick({ lastAttemptAt: 1000, lastAttemptFailed: true, inFlight: false }, 5_000, gap), 'throttled')
    // 上一次失败：10s 到了 → 立刻可重试（**这条是本轮修的核心**）
    assert.equal(decideFetchKick({ lastAttemptAt: 1000, lastAttemptFailed: true, inFlight: false }, 12_000, gap), 'run')
    // 在途 → 不并发起第二轮
    assert.equal(decideFetchKick({ lastAttemptAt: 1000, lastAttemptFailed: true, inFlight: true }, 999_999, gap), 'in-flight')
  })

  it('uses native HTTP on device and JS fetch on H5', () => {
    assert.deepEqual(emailFetchStages(true), ['native-sync', 'pull-list'])
    assert.deepEqual(emailFetchStages(false), ['js-sync', 'pull-list'])
  })

  it('falls back to pocket host when resolved API base is empty', () => {
    assert.equal(resolveFetchApiBase('', 'https://pocket.itestu.cn'), 'https://pocket.itestu.cn')
    assert.equal(resolveFetchApiBase('https://pocket.itestu.cn/', 'x'), 'https://pocket.itestu.cn')
  })

  it('hides raw Failed to fetch from the inbox list', () => {
    assert.equal(sanitizeFetchHint('Failed to fetch'), '后台收信未完成，已显示已同步邮件')
    assert.equal(sanitizeFetchHint('同步失败：Failed to fetch'), '后台收信未完成，已显示已同步邮件')
    assert.equal(sanitizeFetchHint('已同步 1 个账户，新邮件 2'), '已同步 1 个账户，新邮件 2')
  })

  it('hides raw backend error payloads from the inbox list', () => {
    // 真机实测：/api/emails/classify 未配置分类器时返回 503，
    // 收件箱顶部曾原样显示这串 JSON。原始负载绝不能上屏。
    const raw = '同步失败：HTTP 503 {"error":"classifier not configured"}'
    const out = sanitizeFetchHint(raw)
    assert.ok(!/classifier/i.test(out), `英文内部术语泄漏：${out}`)
    assert.ok(!/HTTP \d{3}/.test(out), `状态码/JSON 泄漏：${out}`)
    assert.ok(!/\{|\}/.test(out), `JSON 原文泄漏：${out}`)
    assert.ok(out.length > 0, '必须给出可读文案而不是空串')
  })

  it('maps 401/403 and other status codes to readable hints', () => {
    assert.equal(sanitizeFetchHint('同步失败：HTTP 401 invalid token'), '登录状态已失效，请重新登录')
    assert.equal(sanitizeFetchHint('同步失败：HTTP 500 boom'), '后台收信未完成（500），已显示已同步邮件')
  })

  it('uses the injected translator so hints follow the active language', () => {
    const t = (k: string, p?: Record<string, unknown>) =>
      p ? `T:${k}:${p.code}` : `T:${k}`
    assert.equal(
      sanitizeFetchHint('同步失败：HTTP 503 {"error":"classifier not configured"}', t),
      'T:email.fetchHintClassifierOff',
    )
    assert.equal(sanitizeFetchHint('同步失败：HTTP 502 x', t), 'T:email.fetchHintHttpError:502')
  })

  it('leaves normal successful hints untouched', () => {
    const ok = '已同步 1 个账户，新邮件 2，已归类 3'
    assert.equal(sanitizeFetchHint(ok), ok)
    assert.equal(sanitizeFetchHint(''), '')
  })

  it('retries a full list pull when local is empty and since skipped everything', () => {
    assert.equal(shouldRetryFullListPull(0, 0, 1_700_000_000_000), true)
    assert.equal(shouldRetryFullListPull(3, 0, 1_700_000_000_000), false)
    assert.equal(shouldRetryFullListPull(0, 0, 0), false)
  })
})
