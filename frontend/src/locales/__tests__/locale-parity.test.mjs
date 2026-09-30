/**
 * i18n 语言包完整性守卫（2026-09-30 真机审计）。
 *
 * 真机上发现两处漏项，都会直接暴露给用户：
 *   1. `nav.rss` 在 8 个语言包里**全部缺失**（只有 zh-CN/en-US 有），
 *      vue-i18n 找不到就回退成原始 key，底部导航直接显示字面量
 *      "nav.rss"。这正是真机截图里那个看不懂的标签。
 *   2. 设置页 RedClaw 区块整块硬编码中文，en-US 下也会冒中文。
 *
 * 本测试以 en-US 为基准（它是 key 最全的语言包），断言其余语言包
 * 不缺任何 key —— 少一个就等于那个 locale 下露出原始 key 名。
 *
 * Run: node --test src/locales/__tests__/locale-parity.test.mjs
 */
import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const LOCALES_DIR = path.join(HERE, '..')

const LOCALES = [
  'zh-CN', 'zh-TW', 'en-US', 'ja-JP',
  'ko-KR', 'de-DE', 'fr-FR', 'es-ES', 'pt-BR',
]

function load(locale) {
  return JSON.parse(fs.readFileSync(path.join(LOCALES_DIR, `${locale}.json`), 'utf8'))
}

/** 收集对象里所有叶子路径（形如 'settings.redclawDisabled'）。 */
function leafPaths(obj, prefix = '') {
  const out = []
  for (const [k, v] of Object.entries(obj || {})) {
    const p = prefix ? `${prefix}.${k}` : k
    if (v && typeof v === 'object' && !Array.isArray(v)) out.push(...leafPaths(v, p))
    else out.push(p)
  }
  return out
}

test('所有语言包都是合法 JSON 且非空', () => {
  for (const loc of LOCALES) {
    const j = load(loc)
    assert.ok(j && typeof j === 'object', `${loc}.json 解析失败或不是对象`)
    assert.ok(Object.keys(j).length > 0, `${loc}.json 为空`)
  }
})

test('各语言包与 en-US 的 key 集合完全一致（缺一个就露原始 key）', () => {
  const base = new Set(leafPaths(load('en-US')))
  assert.ok(base.size > 50, `en-US 基准 key 数量异常少：${base.size}`)

  for (const loc of LOCALES) {
    if (loc === 'en-US') continue
    const have = new Set(leafPaths(load(loc)))
    const missing = [...base].filter((k) => !have.has(k))
    assert.equal(
      missing.length, 0,
      `${loc}.json 缺少 ${missing.length} 个 key（会直接显示原始 key 名）：\n  ${missing.join('\n  ')}`,
    )
  }
})

test('真机审计回归：nav.rss 不得在任何语言包中缺失', () => {
  for (const loc of LOCALES) {
    const j = load(loc)
    assert.ok(j.nav, `${loc}.json 缺少 nav 段`)
    assert.ok(
      typeof j.nav.rss === 'string' && j.nav.rss.trim(),
      `${loc}.json 缺少 nav.rss —— 底部导航会显示字面量 "nav.rss"`,
    )
  }
})

test('真机审计回归：设置页 RedClaw 文案不得缺失（曾整块硬编码中文）', () => {
  const REQUIRED = [
    'settings.redclawIntegration',
    'settings.redclawConnected',
    'settings.redclawMisconfigured',
    'settings.redclawDisabled',
    'settings.tenant',
  ]
  for (const loc of LOCALES) {
    const paths = new Set(leafPaths(load(loc)))
    const missing = REQUIRED.filter((p) => !paths.has(p))
    assert.equal(missing.length, 0, `${loc}.json 缺少：${missing.join(', ')}`)
  }
})

test('翻译值不得为空串或纯空白（空串等于把文案吞掉）', () => {
  for (const loc of LOCALES) {
    for (const p of leafPaths(load(loc))) {
      const v = p.split('.').reduce((o, k) => (o || {})[k], load(loc))
      assert.ok(
        typeof v === 'string' && v.trim(),
        `${loc}.json 的 ${p} 是空值`,
      )
    }
  }
})
