/**
 * store / runtime 暂存原始 error 的架构约定护栏。
 *
 * 约定（2026-09 审计确立）：store 与 native runtime 只保存**原始**错误
 * （`error.value = e?.message`），由视图在渲染前经 toUserMessage / useApiError 归一。
 * 这样避免了「让 store 依赖全局 i18n 访问器」的架构决策。
 *
 * 风险点：只要有**任何一个**消费视图直接 `{{ store.error }}` 渲染出去，
 * 约定就被打破、原始错误就会漏到界面上。这个测试就是守住那条线。
 *
 * 真实踩过的例子：
 *   AgentMarketView / SkillMarketView / WorkbuddyView / ScheduledTaskListView /
 *   TasksView / AIChatView / ToolCallCard / RoundTimeline
 *   —— 这 8 个视图都曾直接渲染未归一的 store error。
 *
 * Run: node --test src/api/__tests__/store-error-consumers.test.mjs
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, dirname, extname, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..', '..', '..')
const SRC = join(ROOT, 'src')
const SKIP = new Set(['node_modules', '.git', 'dist', '__tests__', 'android', 'ios'])

/** 模板里把 `xxx.error` 直接渲染出来的写法。 */
const RAW_RENDER = /\{\{\s*[A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*\.error\s*\}\}|v-if\s*=\s*"[^"]*\.[A-Za-z_$][\w$]*\.error\b/
const NORMALIZED = /\b(apiError|toUserMessage)\s*\(/

function collectVue(dir, out = []) {
  for (const name of readdirSync(dir)) {
    if (SKIP.has(name)) continue
    const full = join(dir, name)
    if (statSync(full).isDirectory()) collectVue(full, out)
    else if (extname(full) === '.vue') out.push(full)
  }
  return out
}

const vueFiles = collectVue(SRC)

function findOffenders() {
  const out = []
  for (const file of vueFiles) {
    const text = readFileSync(file, 'utf8')
    if (!RAW_RENDER.test(text)) continue
    if (NORMALIZED.test(text)) continue
    text.split(/\r?\n/).forEach((l, i) => {
      if (RAW_RENDER.test(l)) out.push(`${relative(SRC, file).replace(/\\/g, '/')}:${i + 1}  ${l.trim().slice(0, 90)}`)
    })
  }
  return out
}

describe('store error 的消费视图必须归一后再渲染', () => {
  const offenders = findOffenders()

  it('扫描到了足够的 .vue 文件（防止路径写错导致空跑通过）', () => {
    assert.ok(vueFiles.length > 100, `只扫描到 ${vueFiles.length} 个 .vue，扫描范围可能失效`)
  })

  it('没有视图直接渲染未归一的 store error', () => {
    assert.equal(
      offenders.length,
      0,
      `发现 ${offenders.length} 处直接渲染未归一的 store error：\n  - ${offenders.join('\n  - ')}\n` +
      '  修法：模板里改 {{ apiError(store.error, \'errors.xxx\') }}，并确保该文件已接入 useApiError()。',
    )
  })
})
