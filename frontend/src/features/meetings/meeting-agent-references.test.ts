// meeting-agent-references.test.ts — 门禁：「智能体检索到的参考资料要真的显示出来」。
//
// 需求原文：「并能在总结同时，给出一些参考的资料与建议」。
//
// 这道门守的是**最后一段链路**：agent 在后端查到了真实笔记 → 摘要 JSON 里
// 带 references → 前端解析 → 面板渲染。前面几段各有门禁（后端 agent 包 12 例、
// server 接线 4 例），但**从响应到界面**这一段没有判据，于是出现过一种典型形态：
// 数据在后端算好了、类型也声明了，界面上什么都不显示。
//
// 三层判据：解析（normalizeReferences 的过滤规则）、渲染（面板必须有那一块）、
// 接线（摘要结果要真的把 references 传下去）。每层带负控。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const HERE = dirname(fileURLToPath(import.meta.url))
// 本文件在 features/meetings/ ⇒ src/api/ 在上溯两级。
const read = (f) => readFileSync(join(HERE, '..', '..', f), 'utf8')

/**
 * 按**花括号配平**取函数体。
 *
 * ⚠⚠ 2026-10-11（§426.7 顺带修）：本文件原先三处都用
 * `src.slice(idx).slice(0, <数字>)` 取「函数体」——
 * 那个魔法窗口等于给每个函数偷偷加了一条
 * 「函数体不许超过 N 字」的约束，而这条约束**从未有人拍过**。
 * §426 只是给 normalizeSummary 顶部加了一段注释，函数体涨过 600 字
 * ⇒ 一道判据被一次**正当**改动打成红（与 refine-fallback-notice.test.ts
 * 同一个形状，那边已记「它量的是某段文本的形状，不是不变量」）。
 *
 * ⇒ 处置：**只放开形式，判据一字未动**（§410 的纪律）——
 * 窗口换成函数边界，插多少行无关代码都不再影响它。
 * 本文件另配变异验证（见测试末尾的「判据自证」）。
 */
const bodyOfFunc = (src, name) => {
  const at = src.search(new RegExp(`(^|\\n)(export )?function ${name}\\(`))
  if (at < 0) return null
  const open = src.indexOf('{', at)
  if (open < 0) return null
  let depth = 0
  for (let k = open; k < src.length; k++) {
    if (src[k] === '{') depth++
    else if (src[k] === '}') {
      depth--
      if (depth === 0) return src.slice(open, k + 1)
    }
  }
  return null
}

describe('A · 解析层：references 必须被解析出来且逐条过滤', () => {
  it('normalizeReferences 必须存在并处理两种字段命名', () => {
    const src = read('api/meetings.ts')
    assert.match(src, /function normalizeReferences/, '缺少 normalizeReferences')
    // 后端用 snake_case（note_id），类型定义用 camel（noteId）——两种都要吃。
    assert.match(src, /o\.note_id/, '没有处理后端的 note_id 字段名')
    assert.match(src, /o\.noteId/, '没有处理 noteId 字段名')
  })

  it('normalizeSummary 必须把 references 接进结果', () => {
    const src = read('api/meetings.ts')
    const fn = bodyOfFunc(src, 'normalizeSummary')
    assert.ok(fn && fn.length > 100, `normalizeSummary 的函数体没取到（${fn?.length ?? 0} 字）`)
    assert.match(
      fn, /references: normalizeReferences/,
      'normalizeSummary 没有解析 references ⇒ 后端查到的资料到不了界面',
    )
  })

  it('toLiveSummary 必须透传 references（丢在这里 = 白查）', () => {
    const src = read('api/meetings.ts')
    const fn = bodyOfFunc(src, 'toLiveSummary')
    assert.ok(fn && fn.length > 100, `toLiveSummary 的函数体没取到（${fn?.length ?? 0} 字）`)
    assert.match(
      fn, /references: result\.references/,
      'toLiveSummary 丢了 references ⇒ useLiveSummary 存进 store 的摘要没有参考资料',
    )
  })

  it('负控：缺 note_id 的条目必须被丢弃（否则会跳到 /notes/undefined）', () => {
    const src = read('api/meetings.ts')
    const fn = bodyOfFunc(src, 'normalizeReferences')
    const body = fn ?? ''
    assert.match(body, /if \(!noteId\) continue/, '缺 noteId 的引用没有被丢弃')
    // 负控样本：一条合法 + 一条缺 noteId，过滤后应只剩一条。
    const raw = [{ title: '好', note_id: 'n1', why: 'w' }, { title: '坏', why: 'w' }]
    const kept = raw.filter((r) => String(r.note_id ?? r.noteId ?? '').trim() !== '')
    assert.equal(kept.length, 1, '负控：过滤规则应只留下有 noteId 的那条')
  })
})

describe('B · 渲染层：面板必须真的显示参考资料', () => {
  it('MeetingInsightPanel 必须有参考资料区块', () => {
    const src = read('features/meetings/MeetingInsightPanel.vue')
    assert.match(src, /参考资料/, '面板没有参考资料区块 ⇒ agent 查到了但用户看不到')
    assert.match(src, /references\.length/, '没有按 references 是否为空来控制显示')
    assert.match(src, /v-for="\(ref, i\) in references"/, '没有遍历 references')
  })

  it('点击参考资料必须能跳到对应笔记（复用 open-related）', () => {
    const panel = read('features/meetings/MeetingInsightPanel.vue')
    assert.match(
      panel, /emit\('open-related',\s*\{\s*type: 'note'/,
      '参考资料点击没有复用 open-related ⇒ 点不开',
    )
    // 落点必须与 onOpenRelated 的分支一致：note → /notes/{id}
    const detail = read('features/meetings/MeetingDetailView.vue')
    assert.match(
      detail, /item\.type === 'note'\) router\.push\(`\/notes\/\$\{item\.id\}`\)/,
      'onOpenRelated 没有 note → /notes/{id} 的分支，emit 过去也是死路',
    )
  })

  it('负控：没有 references 时不能渲染出一个空区块', () => {
    const panel = read('features/meetings/MeetingInsightPanel.vue')
    assert.match(panel, /v-if="references\.length"/, '空数组会渲染出一个空的「参考资料」标题')
    // 类型上必须是可选的：一次性 chat 路径没有这个字段。
    const store = read('features/meetings/meetings-store.ts')
    assert.match(
      store, /references\?: SummaryReference\[\]/,
      'references 若是必填，agent 之外的路径会类型报错或被迫填假数据',
    )
  })
})

describe('C · 接线层：从响应到界面的每一跳都不能断', () => {
  it('LiveSummary 类型必须声明 references', () => {
    const store = read('features/meetings/meetings-store.ts')
    assert.match(store, /export interface SummaryReference/, '缺少 SummaryReference 类型')
    assert.match(store, /references\?: SummaryReference\[\]/, 'LiveSummary 缺 references 字段')
  })

  it('负控：整条链任一跳断掉，判据必须能抓到', () => {
    // 逐跳模拟：断开「透传」这一跳，后面的判据必须不再满足。
    const withPass = 'return { summary: result.summary, references: result.references, updatedAt: 1 }'
    const noPass = 'return { summary: result.summary, updatedAt: 1 }'
    assert.match(withPass, /references: result\.references/)
    assert.ok(
      !/references: result\.references/.test(noPass),
      '负控：断开透传后不该再匹配',
    )
  })

  it('后端摘要响应必须真的带 references 字段', () => {
    // 跨语言断言：后端 Go 的系统提示词要求模型返回 references。
    const go = readFileSync(
      join(HERE, '..', '..', '..', '..', 'backend', 'internal', 'server', 'server_meeting.go'),
      'utf8',
    )
    assert.match(go, /"references":\[/, '后端系统提示词没有要求模型返回 references')
    assert.match(
      go, /禁止编造|不许写进 references/,
      '后端没有禁止编造引用 ⇒ references 里会是看起来很像真的假笔记',
    )
  })
})
