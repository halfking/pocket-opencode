// meeting-agent-recommendations-restore.test.mjs — §411：智能体算出来的推荐，
// 在「打开一场已录完的会议」这条路径上必须仍然显示。
//
// ── 缺陷 ──
//
// `MeetingDetailView` 里 `noteRecs` 同时被两个来源写：
//   ① `noteRecs.value = m.recommendations`      库里的**智能体**产出
//   ② `refreshRelated()` 里 `noteRecs.value = await searchRelatedContext(...)`
//      本地 LIKE + 维基百科，**无条件整体覆盖**
// 而 `onMounted` 里 ① 在前、② 在后，中间只隔两行 ⇒ **① 是死代码**。
//
// ⚠ 为什么「打开已录完的会议」必然丢（不是边角）：
//   实时那一份 `recommendations.value` 只有两处会被填 ——
//     · `onMicToggle` 的 `seedSegments(...)`（:293，**仅录音时**）
//     · `await refresh(true)`（:282，**仅停止录音时**）
//   而 `useLiveSummary` 的 `watch(segments)` 盯的是 runtime 的 `segments`，
//   `load()` 只填 `storedSegments` ⇒ **不录音就打开 ⇒ 恒为 `[]`**。
//   实测该场景下智能体推荐存活 **0 条**。
//
// ⚠ 为什么不把 `refreshRelated()` 改成「合并」：它由
//   `watch(displaySegments.length)` 每 2.5s 触发一次，改成合并会让上一轮的
//   检索结果永久累积。⇒ 两个通道各归其主，只在渲染口合并。
//
// ── 这道门钉什么 ──
//
// 1. 行为层（用真实的 `mergeRecommendations` 跑真实的三路合并）：4 例。
// 2. 形态层（真实 .vue 里三处接线）：3 例。
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

import { mergeRecommendations } from '../meeting-related.ts'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const DETAIL = path.resolve(HERE, '..', 'MeetingDetailView.vue')
const RAW = readFileSync(DETAIL, 'utf8')

/** 与 meeting-action-items-source.test.mjs 同款：注释里的同一句话不算接线。 */
function stripJsComments(src) {
  let out = ''
  let i = 0
  let state = 'code'
  while (i < src.length) {
    const c = src[i]
    const d = src[i + 1]
    if (state === 'code') {
      if (c === '/' && d === '/') { state = 'line'; i += 2; continue }
      if (c === '/' && d === '*') { state = 'block'; i += 2; continue }
      if (c === "'") state = 'sq'
      else if (c === '"') state = 'dq'
      else if (c === '`') state = 'tpl'
      out += c
      i++
      continue
    }
    if (state === 'line') {
      if (c === '\n') { state = 'code'; out += '\n' }
      i++
      continue
    }
    if (state === 'block') {
      if (c === '*' && d === '/') { state = 'code'; i += 2; continue }
      if (c === '\n') out += '\n'
      i++
      continue
    }
    if (c === '\\') { out += c + (d ?? ''); i += 2; continue }
    if ((state === 'sq' && c === "'") || (state === 'dq' && c === '"') || (state === 'tpl' && c === '`')) {
      state = 'code'
    }
    out += c
    i++
  }
  return out
}

const CODE = stripJsComments(
  RAW.replace(/<!--[\s\S]*?-->/g, (m) => '\n'.repeat(m.split('\n').length - 1))
    .match(/<script[^>]*>([\s\S]*?)<\/script>/)[1],
)

/** 智能体的两份（实时 + 库里那份），`recommendations.value` 与 `agentRecs`。 */
const AGENT = [
  { type: 'note', id: 'n-agent-1', title: '智能体给的笔记参考' },
  { type: 'web', id: 'wiki-微服务', title: '智能体给的维基参考' },
]
/** 本地 LIKE + 维基百科检索的结果。 */
const SEARCH = [
  { type: 'note', id: 'n-like-1', title: '本地 LIKE 命中的笔记' },
  { type: 'web', id: 'wiki-SQL', title: '维基百科检索命中' },
]

/**
 * ⚠ 直接**执行** `.vue` 里那条真实的 `mergedRecs` 表达式，而不是在测试里
 * 重写一份合并。第一版写的是本地 `merge3()` 副本 ⇒ 变异「把 mergedRecs 退回
 * 两路」时**行为层全绿、只有形态层红**（实测 M1 同款弱点）。
 * ⇒ 判据必须落在出厂的那份代码上，否则它与出厂代码可以各走各的。
 */
function realMerged(live, agent, searched) {
  const m = CODE.match(/const\s+mergedRecs\s*=\s*computed\(\(\)\s*=>\s*([\s\S]*?)\)\s*\n/)
  assert.ok(m, 'MeetingDetailView.vue 里找不到 mergedRecs 的定义')
  const body = m[1].trim()
  // eslint-disable-next-line no-new-func
  const fn = new Function(
    'mergeRecommendations', 'recommendations', 'agentRecs', 'noteRecs',
    `return (${body})`,
  )
  return fn(
    mergeRecommendations,
    { value: live },
    { value: agent },
    { value: searched },
  )
}

describe('§411 · 行为层：库里那份智能体推荐必须活到面板上', () => {
  it('★ 打开已录完的会议（实时那一份为 []）⇒ 智能体的推荐一条都不能少', () => {
    const shown = realMerged([], AGENT, SEARCH)
    const survived = shown.filter((i) => AGENT.some((a) => a.id === i.id))
    assert.equal(survived.length, AGENT.length,
      `智能体推荐只剩 ${survived.length}/${AGENT.length} 条 ⇒ ` +
      `打开已录完的会议时参考资料几乎为空：${JSON.stringify(shown.map((i) => i.title))}`)
  })

  it('★ 检索那一路不能被这次修复挤掉（两路都必须显示）', () => {
    const shown = realMerged([], AGENT, SEARCH)
    const keptSearch = shown.filter((i) => SEARCH.some((s) => s.id === i.id))
    assert.equal(keptSearch.length, SEARCH.length,
      `检索结果只剩 ${keptSearch.length}/${SEARCH.length} 条：${JSON.stringify(shown.map((i) => i.title))}`)
  })

  it('★★ 负控：回到修好前的两路合并 ⇒ 判据必须当场失效', () => {
    // 缺陷版本的表达式：库里那份从来没进过渲染口。
    const before = mergeRecommendations([], SEARCH)
    assert.equal(before.length, SEARCH.length)
    const survived = before.filter((i) => AGENT.some((a) => a.id === i.id))
    assert.equal(survived.length, 0,
      '负控本身写错了：旧写法本该把智能体那份全丢掉')
    assert.notEqual(survived.length, realMerged([], AGENT, SEARCH)
      .filter((i) => AGENT.some((a) => a.id === i.id)).length,
      '真实表达式与旧写法同形 ⇒ 这次修复没带来任何差别')
  })

  it('★★ 同一 type:id 出现在两路时只算一条（去重不得因多一路而失效）', () => {
    const dup = { type: 'note', id: 'n-like-1', title: '同名但来源不同' }
    const shown = realMerged([], [{ type: 'note', id: 'n-like-1', title: '智能体给的' }], [dup])
    assert.equal(shown.filter((i) => i.id === 'n-like-1').length, 1,
      `同一个 type:id 出现了 ${shown.length} 次：${JSON.stringify(shown)}`)
    assert.equal(shown[0].title, '智能体给的', '冲突时必须智能体那一份优先')
  })
})

describe('§411 · 形态层：三处接线都要在', () => {
  it('★ mergedRecs 必须把 agentRecs 接进去', () => {
    const m = CODE.match(/const\s+mergedRecs\s*=\s*computed\(([\s\S]{0,400}?)\)\s*\n/s)
    assert.ok(m, '找不到 mergedRecs 的定义')
    assert.match(m[1], /agentRecs/,
      `mergedRecs 没有把 agentRecs 接进来 ⇒ 库里那份仍然不显示：${m[1].slice(0, 200)}`)
    assert.match(m[1], /recommendations\.value/,
      'mergedRecs 丢了实时的 recommendations 这一路')
    assert.match(m[1], /noteRecs/,
      'mergedRecs 丢了检索这一路 ⇒ 修智能体那一条时把检索挤掉了')
  })

  it('★ agentRecs 必须在 load() 里被**整份**赋值，且不得写成合并', () => {
    const at = CODE.indexOf('async function load()')
    assert.ok(at > 0, '找不到 load()')
    const body = CODE.slice(at, CODE.indexOf('\nconst ', at))
    const hit = body.match(/agentRecs\.value\s*=\s*([^;\n]+)/)
    assert.ok(hit, 'load() 里没有给 agentRecs 赋值 ⇒ 库里那份永远取不到')
    assert.doesNotMatch(hit[1], /mergeRecommendations/,
      `agentRecs 在 load() 里被写成合并 ⇒ 换会议会把上一场的推荐带过来：${hit[1]}`)
  })

  it('★★ onMounted 里不得再有那行死代码（它必然被 refreshRelated 覆盖）', () => {
    const at = CODE.indexOf('onMounted(async () => {')
    assert.ok(at > 0, '找不到 onMounted')
    const body = CODE.slice(at, CODE.indexOf('watch(() => displaySegments', at))
    assert.doesNotMatch(body, /noteRecs\.value\s*=\s*m\.recommendations/,
      '那行死代码复活了：它写在 onMounted 里，会被 refreshRelated() 整体覆盖')
    assert.match(body, /refreshRelated\(\)/,
      'onMounted 不再调用 refreshRelated ⇒ 检索这一路没了（判据锚点失效）')
  })
})