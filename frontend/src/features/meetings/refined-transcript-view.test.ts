// refined-transcript-view.test.ts — 门禁：**精校结果必须对用户可见**（§107）。
//
// ── 这道门守的是什么 ───────────────────────────────────────────────
//
// 事实链（§107 现场核实，不是推测）：
//
//   finalizeRecording → meetingsApi.refine() → result.refinedTranscript
//     → updateMeeting(id, { refinedTranscript, status: refineStatusFor(...) })
//     → localDB（native/schema.ts 有 refined_transcript 列）
//
// `LocalMeeting.refinedTranscript` **一直有值**（meetings-store.ts:257 的 rowToMeeting），
// 而 `MeetingDetailView.vue` **从头到尾没读过这个字段** ——
// `.vue` 侧唯一的读取点是 `NotesHubView.vue:316` 的一行摘要预览。
// ⇒ 用户在会议里录完、精校真跑了、结果真存了，**打开这场会议看不到它**。
//
// 这不是「体验可以更好」，是**做了但不可见**：功能等于不存在。
// 而在 §105 之前这条链恒 404，所以**从来没人需要回答这个问题** ——
// 缺口是被上一轮的修复**暴露**出来的，不是一直在那儿没人碰。
//
// ── 为什么要拆两层（§95「跨语言契约门必须拆两半」同源）────────────
//
//   A 纯函数：`resolveRefinedView` 的判定语义（含**降级不许说成成功**）
//   B 接线：.vue 真的读了 refinedTranscript 并渲染它
//
// 只写 B ⇒ 判定逻辑改坏时门仍绿（因为扫的是"有没有出现这个词"）。
// 只写 A ⇒ 纯函数再对，页面上照样什么都不显示。
//
// ⚠ 两层都带负控，且负控**用与正例同一个判定函数** ——
//   负控另写一遍判断 = 验的是负控自己。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { resolveRefinedView } from './refined-transcript-view.ts'

const HERE = dirname(fileURLToPath(import.meta.url))
const read = (f: string) => readFileSync(join(HERE, f), 'utf8')

/**
 * ★ 剥注释但**保持长度**，这样 indexOf 量到的仍是同一篇源码的相对位置。
 *
 * 为什么必须剥：`MeetingDetailView.vue` 的模板注释里写着
 * 「`LocalMeeting.refinedTranscript` 一直有值」——
 * 而接线判据要断言的正是 `refinedTranscript` 出现在**模板绑定**里。
 * 不剥注释的话，判据会被这段**说明文字**顶住：
 * 把模板里那一行删掉，门照样绿。
 */
const blank = (m: string) => m.replace(/[^\n]/g, ' ')
const stripComments = (t: string) => t
  .replace(/\/\*[\s\S]*?\*\//g, blank)
  .split('\n').map((l) => { const i = l.indexOf('//'); return i < 0 ? l : l.slice(0, i) }).join('\n')

const REFINE_OK = '张伟：二期十一月底量产。\n李娜：那我们下周三对齐一下。'
const REFINE_DEGRADED = '张伟：二期十一月底量产。' // 回落链写进去的就是原文

describe('A · 判定层：什么算「有精校结果可展示」', () => {
  it('精校成功 ⇒ 展示精校版，并说明已精校', () => {
    const r = resolveRefinedView({ refinedTranscript: REFINE_OK, status: 'refined' })
    assert.equal(r.hasRefined, true, '精校成功却判定成没有 ⇒ 用户永远看不到精校版')
    assert.equal(r.mode, 'refined')
    assert.equal(r.badge, '已精校')
  })

  it('★ 降级（refine-failed）⇒ 即便有文本也**不当作精校版**', () => {
    // 回落链 api/meetings.ts:371/387 写的是 refinedTranscript: transcript（原文）。
    // 若照常标「已精校」，就是把一次没成功的精校说成成功了（§16 / §36 同型）。
    const r = resolveRefinedView({ refinedTranscript: REFINE_DEGRADED, status: 'refine-failed' })
    assert.equal(r.hasRefined, false, '降级产物被当成精校结果展示 ⇒ 说谎')
    assert.equal(r.mode, 'original', '降级时必须留在原文')
    assert.match(r.badge, /精校未生效/, `徽章必须说清没生效，实际 ${JSON.stringify(r.badge)}`)
  })

  it('空白与 null ⇒ 无徽章、显示原文', () => {
    for (const v of [null, '', '   ', '\n\t']) {
      const r = resolveRefinedView({ refinedTranscript: v, status: 'refined' })
      assert.equal(r.hasRefined, false, `${JSON.stringify(v)} 不该算精校结果`)
      assert.equal(r.badge, '', `${JSON.stringify(v)} 不该有徽章`)
      assert.equal(r.mode, 'original')
    }
  })

  it('status 未知但有文本 ⇒ 按精校版展示（不因缺状态而吞掉真结果）', () => {
    const r = resolveRefinedView({ refinedTranscript: REFINE_OK, status: 'completed' })
    assert.equal(r.hasRefined, true, '非 refine-failed 的状态不该被当成降级')
    assert.equal(r.mode, 'refined')
  })

  it('负控：把「有文本就是精校」当规则，会在降级样本上说谎', () => {
    // 旧实现（§107 修复前的形态）：只看 refinedTranscript 非空。
    const naive = (t: string | null) => ({ hasRefined: (t ?? '').trim() !== '' })
    assert.equal(naive(REFINE_DEGRADED).hasRefined, true, '负控前提：旧实现会把降级产物算成精校')
    assert.equal(
      resolveRefinedView({ refinedTranscript: REFINE_DEGRADED, status: 'refine-failed' }).hasRefined,
      false,
      '新实现必须拒绝 —— 与上面那条断言是同一件事的正反面',
    )
  })
})

describe('B · 接线层：详情页真的读它并渲染它', () => {
  const src = stripComments(read('MeetingDetailView.vue'))

  const template = (() => {
    const a = src.indexOf('<template>')
    const b = src.indexOf('</template>')
    assert.ok(a >= 0 && b > a, 'MeetingDetailView.vue 的模板边界没找到 —— 解析器坏了')
    return src.slice(a, b)
  })()

  it('★ 模板里必须绑定 refinedTranscript（否则又变回「看不见」）', () => {
    assert.ok(
      /\{\{\s*[^}]*\brefinedTranscript\b[^}]*\}\}/.test(template),
      '模板里没有 refinedTranscript 的插值 ⇒ 精校结果不会被显示。' +
        '判据只扫模板区（剥过注释），注释里提到它不算数。',
    )
  })

  it('必须用 resolveRefinedView 判定，而不是自己 if 一遍', () => {
    assert.match(src, /resolveRefinedView\s*\(/, '视图没有调用判定函数 ⇒ 降级会被说成成功')
    // 负控：把判定塞进模板 if 里也能「读到 refinedTranscript」，
    // 但那正是旧实现的形态 —— 负控证明本条判据不会只靠模板插值过关。
    const inlineDecision = /v-if="[^"]*refinedTranscript\s*\.trim\(\)/.test(template)
    assert.equal(inlineDecision, false, '视图里出现了内联的精校判定 ⇒ 与纯函数分叉了')
  })

  it('必须提供「切回原文」的入口，且切换态在换会议时复位', () => {
    assert.match(src, /refinedModeOverride/, '没有手动切换态 ⇒ 用户看不了原文')
    assert.match(
      src, /refinedModeOverride\.value\s*=\s*null/,
      '手动切换态没有复位点 ⇒ 在 A 会议切到原文会带到 B 会议',
    )
  })

  it('负控：判据必须在本文件上真的咬得住（先证明它能命中真源码）', () => {
    // 正控：真源码里这三个锚点都必须命中，否则「本组全绿」是无意义的。
    assert.ok(/refinedTranscript/.test(template), '正控失败：真模板里都找不到 refinedTranscript')
    assert.ok(/resolveRefinedView/.test(src), '正控失败：真源码里都找不到 resolveRefinedView')
    assert.ok(/refinedModeOverride\.value = null/.test(src), '正控失败：复位赋值不存在')

    // 负控：把三处锚点分别抽掉，同一组判据必须失败。
    const withoutToggle = src.replace(/refinedModeOverride/g, 'unusedState')
    assert.equal(
      /refinedModeOverride/.test(withoutToggle), false, '负控失效：改名后判据仍命中',
    )
    const withoutReset = src.replace(/refinedModeOverride\.value = null/g, '// removed')
    assert.equal(
      /refinedModeOverride\.value\s*=\s*null/.test(withoutReset), false, '负控失效：删掉复位后判据仍命中',
    )
    const withoutBind = template.replace(/\{\{[^}]*refinedTranscript[^}]*\}\}/, '{{ "" }}')
    assert.equal(
      /\{\{\s*[^}]*\brefinedTranscript\b[^}]*\}\}/.test(withoutBind), false,
      '负控失效：模板不再绑定 refinedTranscript 时判据仍命中',
    )
  })
})