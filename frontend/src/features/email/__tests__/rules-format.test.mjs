/**
 * 邮件过滤策略格式转换测试（纯 ESM 镜像 rules-format.ts，与 outbox.test.mjs 同约定）。
 */

import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { readFile } from 'node:fs/promises'

// ---- 镜像 rules-format.ts 的实现 ----

const parseRules = (raw) => {
  if (!raw || typeof raw !== 'object') return []
  if (Array.isArray(raw.rules)) {
    return raw.rules.map((r) => ({ ...r, actions: [...r.actions] }))
  }
  const out = []
  for (const p of raw.whitelist ?? []) {
    out.push({ type: 'sender-whitelist', pattern: p, actions: ['mark-important'] })
  }
  for (const p of raw.blacklist ?? []) {
    out.push({ type: 'sender-blacklist', pattern: p, actions: ['archive'] })
  }
  for (const p of raw.keywords ?? []) {
    out.push({ type: 'subject-keyword', pattern: p, actions: [{ name: 'label-category', category: 'work' }] })
  }
  return out
}

const isLegacyRules = (raw) => {
  if (!raw || typeof raw !== 'object') return false
  if (Array.isArray(raw.rules)) return false
  return !!(raw.whitelist?.length || raw.blacklist?.length || raw.keywords?.length)
}

const serializeRules = (entries) => ({
  rules: entries
    .filter((r) => r.pattern.trim().length > 0 && r.actions.length > 0)
    .map((r) => ({ type: r.type, pattern: r.pattern.trim(), actions: [...r.actions] })),
})

// ---- 语义测试 ----

test('legacy whitelist/blacklist/keywords converts to new rule format', () => {
  const parsed = parseRules({
    whitelist: ['boss@corp.com'],
    blacklist: ['spam@x.com'],
    keywords: ['发票'],
  })
  assert.equal(parsed.length, 3)
  assert.deepEqual(parsed[0], { type: 'sender-whitelist', pattern: 'boss@corp.com', actions: ['mark-important'] })
  assert.deepEqual(parsed[1], { type: 'sender-blacklist', pattern: 'spam@x.com', actions: ['archive'] })
  assert.deepEqual(parsed[2].actions, [{ name: 'label-category', category: 'work' }])
  assert.equal(isLegacyRules({ whitelist: ['a'], blacklist: [], keywords: [] }), true)
})

test('new format round-trips and legacy detection is false', () => {
  const input = {
    rules: [
      { type: 'domain-match', pattern: 'corp.com', actions: ['mark-important', { name: 'route-folder', folder: 'INBOX.Work' }] },
    ],
  }
  assert.equal(isLegacyRules(input), false)
  const parsed = parseRules(input)
  const out = serializeRules(parsed)
  assert.deepEqual(out, input)
})

test('serialize drops empty-pattern and actionless rules', () => {
  const out = serializeRules([
    { type: 'subject-keyword', pattern: '   ', actions: ['archive'] },
    { type: 'sender-whitelist', pattern: 'a@b.c', actions: [] },
    { type: 'sender-blacklist', pattern: ' x@y.z ', actions: ['archive'] },
  ])
  assert.equal(out.rules.length, 1)
  assert.equal(out.rules[0].pattern, 'x@y.z')
})

test('parse handles null/undefined/empty gracefully', () => {
  assert.deepEqual(parseRules(null), [])
  assert.deepEqual(parseRules(undefined), [])
  assert.deepEqual(parseRules({}), [])
  assert.equal(isLegacyRules(null), false)
  assert.equal(isLegacyRules({}), false)
})

// ---- 源一致性：组件实际使用共享模块 ----

const settingsSource = await readFile(new URL('../EmailSettingsView.vue', import.meta.url), 'utf8')
const moduleSource = await readFile(new URL('../rules-format.ts', import.meta.url), 'utf8')

test('EmailSettingsView imports the shared rules-format module', () => {
  assert.match(settingsSource, /from '\.\/rules-format'/)
  assert.match(settingsSource, /parseRules, isLegacyRules, serializeRules/)
})

test('rules-format module matches the mirrored implementation', () => {
  assert.match(moduleSource, /export function parseRules/)
  assert.match(moduleSource, /export function isLegacyRules/)
  assert.match(moduleSource, /export function serializeRules/)
})
