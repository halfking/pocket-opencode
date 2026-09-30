/**
 * 邮件详情「发件邮箱 + 按需总结」的契约测试（2026-10-01 需求）。
 *
 * 这两条需求都容易在后续改动中被悄悄改坏，而它们本身几乎没有运行时分支
 * （就是模板上的条件 + 几个函数），所以直接对真实源码做断言：
 * 比起为了测试而把逻辑拆散，锁住契约更能防止回归。
 *
 * 覆盖：
 *  1. 发件邮箱始终可见（不再依赖 fromName 是否存在）。
 *  2. 总结按钮只在没有摘要时出现；总结后撤掉而不是置灰。
 *  3. 摘要展示有明确的标题，不是裸文本。
 */
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const view = readFileSync(
  fileURLToPath(new URL('../EmailDetailView.vue', import.meta.url)),
  'utf8',
)
const api = readFileSync(
  fileURLToPath(new URL('../../../api/email.ts', import.meta.url)),
  'utf8',
)
const store = readFileSync(
  fileURLToPath(new URL('../emails-store.ts', import.meta.url)),
  'utf8',
)

// ── 发件邮箱 ────────────────────────────────────────────────────────────────

test('发件邮箱在详情头部渲染', () => {
  assert.match(view, /class="from-addr"/, '详情头部应渲染发件邮箱')
  assert.match(view, /\{\{ email\.fromAddress \}\}/, '应展示 fromAddress')
})

test('发件邮箱不再依赖 fromName 存在（旧逻辑只在此条件下显示）', () => {
  // 旧代码是 v-if="email.fromName"：没有显示名时地址与名字重复，于是整行不显示，
  // 用户确认不了邮件来自哪个地址。需求要求「显示发件的邮箱」。
  assert.doesNotMatch(
    view,
    /v-if="email\.fromName"\s+class="from-addr"/,
    '不应再以 fromName 存在为显示发件邮箱的前提',
  )
  // 新条件：地址存在且与显示名不同才重复渲染。
  assert.match(
    view,
    /v-if="email\.fromAddress && email\.fromAddress !== email\.fromName"/,
    '应仅在地址存在且不与显示名重复时额外渲染一行',
  )
})

// ── 按需总结 ────────────────────────────────────────────────────────────────

test('总结按钮只在没有摘要时出现（总结后不再需要总结）', () => {
  assert.match(
    view,
    /v-if="!summary"[\s\S]{0,400}runSummarize/,
    '总结按钮应由「尚无摘要」控制',
  )
})

test('总结请求有进行中状态保护，避免重复点击并发触发', () => {
  assert.match(view, /if \(!mail \|\| summarizing\.value\) return/, '应防止重复触发')
  assert.match(view, /:disabled="summarizing"/, '进行中应禁用按钮')
  assert.match(view, /summarizing \? '总结中…'/, '进行中应有明确文案反馈')
})

test('总结成功后写回本地库，保证重进详情不必再请求', () => {
  assert.match(view, /emailsStore\.setAiSummary\(mail\.id, text\)/, '应落本地库')
  assert.match(view, /mail\.aiSummary = text/, '应同步进内存对象')
  // 落库必须是「只写摘要」，不能连带覆盖分类字段。
  // 注意把范围限定在 setAiSummary 函数体内：若用整文件正则，
  // 会跨到 setAiClassification 上误判（那是全量覆盖，属于另一个函数）。
  const setAiSummaryBody = /export async function setAiSummary\([\s\S]*?\n}/.exec(store)?.[0] || ''
  assert.ok(setAiSummaryBody, '应能找到 setAiSummary 函数体')
  assert.match(
    setAiSummaryBody,
    /UPDATE local_emails SET ai_summary = \? WHERE id = \?/,
    'setAiSummary 只能更新 ai_summary',
  )
  assert.doesNotMatch(
    setAiSummaryBody,
    /importance|category|suggested_action/,
    'setAiSummary 不得连带覆盖分类字段',
  )
})

test('摘要以带标题的区块展示，而非裸文本', () => {
  assert.match(view, /class="ai-label">邮件总结</, '应给出「邮件总结」标签')
  assert.match(view, /class="ai-text">\{\{ summary \}\}/, '应展示摘要正文')
})

test('接口层使用长超时（总结要调 LLM）并带 cached 语义', () => {
  assert.match(api, /summarizeEmail\(id: string, signal\?: AbortSignal\)/, '应暴露 summarizeEmail')
  assert.match(api, /\/api\/emails\/\$\{encodeURIComponent\(id\)\}\/summarize/, '路径应转义 id')
  assert.match(api, /timeoutMs: LONG_REQUEST_TIMEOUT_MS/, '总结是慢接口，应放宽超时')
  assert.match(api, /cached: boolean/, '响应应带 cached 标记')
})

test('空摘要不写入、给出失败反馈（避免把空白当总结）', () => {
  assert.match(
    view,
    /if \(!text\) \{[\s\S]{0,200}?toast\.error/,
    '服务端返回空摘要时应报错而不是展示空白',
  )
})
