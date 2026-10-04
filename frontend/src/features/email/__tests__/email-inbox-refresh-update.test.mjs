/**
 * 下拉刷新必须「更新」已有行，而不只是「插入」新行（2026-10-02）。
 *
 * 缺陷：`applyRefreshPage` 只收「全新 id」（`freshPage.filter(e => !existingIds.has(e.id))`），
 * id 已存在的行直接丢弃。表现是「下拉刷新后列表仍显示旧摘要」——而本地库里
 * 的值其实**已经是新的**（emails-store 的 ON CONFLICT 会刷新 snippet），
 * 丢在合并这一步。所以同一个页面上「库是对的、列表是旧的」。
 *
 * 用例的写法刻意遵守两条纪律：
 *  1. 断言**新值真的出现**，而不只是断言「旧值消失」——
 *     只查反向条件的话，实现退化成「返回空列表」也能照样通过。
 *  2. 断言「没有变化时必须原样返回原数组引用」——这是既有语义
 *     （`email-inbox-pagination.test.mjs` 里有同款断言），修复不能牺牲它，
 *     否则 v-for 每次刷新都全量重渲。
 */
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { applyRefreshPage } from '../email-inbox-pagination.ts'

const here = dirname(fileURLToPath(import.meta.url))

// 真实的 LocalEmail 形状（用到 id/date/snippet/isRead/isStarred/category）
const mail = (id, date, over = {}) => ({
  id,
  date,
  snippet: '旧摘要',
  isRead: false,
  isStarred: false,
  category: '',
  ...over,
})

// ── 核心：同一 id 的字段变更必须传播 ──────────────────────────────────────────

test('服务端改写摘要后，刷新必须让列表显示新摘要（缺陷本身的回归锁）', () => {
  const existing = [mail('e1', 300), mail('e2', 200)]
  // 本地库已被 syncEmailsFromServer 刷新过：同 id、同 date，snippet 变了。
  const fresh = [mail('e1', 300, { snippet: '修复后的可读正文' }), mail('e2', 200)]

  const { list } = applyRefreshPage(existing, fresh)

  const e1 = list.find((e) => e.id === 'e1')
  assert.ok(e1, '行不能被刷新丢掉')
  assert.equal(e1.snippet, '修复后的可读正文',
    '同 id 的行必须取刷新页的新值；只收全新 id 会让列表永远停在旧摘要')
})

test('任何字段的变更都要能传播，不只是摘要', () => {
  const fields = [
    ['isRead', true],
    ['isStarred', true],
    ['category', '账单'],
    ['snippet', '新的摘要'],
  ]
  for (const [key, value] of fields) {
    const existing = [mail('e1', 300)]
    const fresh = [mail('e1', 300, { [key]: value })]
    const { list, updatedCount } = applyRefreshPage(existing, fresh)
    assert.equal(list[0][key], value, `字段 ${key} 的变更未传播到列表`)
    assert.equal(updatedCount, 1, `字段 ${key} 变更应计为一次更新`)
  }
})

// ── 计数语义 ────────────────────────────────────────────────────────────────

test('addedCount 只算真正新增，不把「被更新」算成新邮件', () => {
  // 否则「新增 N 封邮件」提示会虚高，且分页游标语义被污染。
  const existing = [mail('a', 200), mail('b', 100)]
  const fresh = [
    mail('a', 200, { snippet: '改了' }),
    mail('b', 100),
    mail('c', 300),
  ]
  const { addedCount, updatedCount } = applyRefreshPage(existing, fresh)
  assert.equal(addedCount, 1, '只有 c 是新邮件')
  assert.equal(updatedCount, 1, '只有 a 变了')
})

test('完全没有变化时原样返回原数组（避免 v-for 全量重渲）', () => {
  const existing = [mail('a', 200), mail('b', 100)]
  const fresh = [mail('a', 200), mail('b', 100)]
  const { list, addedCount, updatedCount } = applyRefreshPage(existing, fresh)
  assert.equal(list, existing, '内容没变就不该重建数组引用')
  assert.equal(addedCount, 0)
  assert.equal(updatedCount, 0)
})

// ── 与「保留已加载分页」的既有语义并存 ──────────────────────────────────────

test('更新 + 新增 + 已翻页的行三者同时正确', () => {
  // 用户已翻到第 2 页（n3/n4 不在刷新页里），刷新页带回 n1 的新摘要 + 一封新邮件。
  const existing = [mail('n1', 600), mail('n2', 500), mail('n3', 400), mail('n4', 300)]
  const fresh = [mail('n1', 600, { snippet: '新摘要' }), mail('n2', 500), mail('new', 700)]

  const { list, addedCount, updatedCount } = applyRefreshPage(existing, fresh)

  assert.deepEqual(list.map((e) => e.id), ['new', 'n1', 'n2', 'n3', 'n4'],
    '新邮件置顶、更新就位、第 2 页的 n3/n4 必须还在')
  assert.equal(list[1].snippet, '新摘要')
  assert.equal(addedCount, 1)
  assert.equal(updatedCount, 1)
})

test('刷新页里重复出现的 id 只取第一次', () => {
  const existing = [mail('a', 200)]
  const fresh = [mail('a', 200, { snippet: '第一次' }), mail('a', 200, { snippet: '第二次' })]
  const { list, updatedCount } = applyRefreshPage(existing, fresh)
  assert.equal(list.filter((e) => e.id === 'a').length, 1, '同 id 不得重复进列表')
  assert.equal(updatedCount, 1)
  assert.equal(list[0].snippet, '第一次')
})

test('空 id 的脏数据既不新增也不覆盖别人的行', () => {
  const existing = [mail('a', 200)]
  const fresh = [mail('', 999, { snippet: '脏数据' }), mail('a', 200, { snippet: '正常更新' })]
  const { list, addedCount } = applyRefreshPage(existing, fresh)
  assert.deepEqual(list.map((e) => e.id), ['a'])
  assert.equal(list[0].snippet, '正常更新')
  assert.equal(addedCount, 0, '空 id 不算新增邮件')
})

// ── 接线护栏：纯函数测试证明不了「刷新真的走了这条路径」 ─────────────────────

test('接线：刷新走内核的 merge，且页面侧不再自行合并分页', (t) => {
  const src = readFileSync(join(here, '..', 'EmailInboxView.vue'), 'utf8')
  // 与本仓其他源码扫描护栏一致：先剥注释，否则「删掉接线只留注释」也能满足断言。
  const code = src
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/(^|[^:])\/\/[^\n]*/g, '$1 ')

  // ① 合并语义由内核承担，策略必须显式声明成 'merge'。
  //    默认是 'replace'，漏写会让下拉刷新把用户弹回第 1 页
  //    （并把已翻开的分页丢掉）——那是 §2.8 修的那个缺陷换了个形态。
  assert.match(code, /refreshPolicy:\s*'merge'/,
    '必须显式声明 refreshPolicy: \'merge\'，否则刷新会走 replace 语义')
  assert.match(code, /mergeRows:\s*inboxMergeRows/,
    '必须把 mergeRows 交给 inboxMergeRows：排序是领域知识，内核不替调用方决定')

  // ② 页面侧**不得**再自行调 applyRefreshPage / mergeInboxPages 合并分页——
  //    那是迁移前的实现，留着就等于同时存在两套合并逻辑（07 §2.9）。
  const legacy = code.match(/applyRefreshPage\(|mergeInboxPages\(/g) || []
  assert.equal(legacy.length, 0,
    `页面侧残留 ${legacy.length} 处旧合并调用；合并已由内核 merge 策略承担`)

  // ③ 两条刷新入口都要真的走内核 refresh。
  const refreshCalls = code.match(/await refreshList\(\)/g) || []
  assert.ok(refreshCalls.length >= 2,
    'showLocal 与下拉刷新两处都必须调用内核的 refreshList()')
  t.diagnostic(`refreshList 调用点数 = ${refreshCalls.length}`)
})
