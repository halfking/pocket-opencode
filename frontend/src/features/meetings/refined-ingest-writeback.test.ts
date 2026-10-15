// refined-ingest-writeback.test.ts — 门禁：**精校降级时不得用原文顶掉已有摘要**（§108）。
//
// ── 缺陷的事实链 ───────────────────────────────────────────────────
//
// `ingestMeetingArtifacts` 原本无条件写：
//
//   summary: refine.refinedTranscript.slice(0, 500) || meeting.summary
//
// 而 `normalizeRefine` 里降级时装的是**原文**：
//
//   refinedTranscript: String(serverRefined ?? fallback)   // fallback = renderTranscript(segments)
//
// ⇒ 对一段**非空原文**做 `slice(0, 500)` 必然非空
// ⇒ 那个 `|| meeting.summary` **永远不生效**
// ⇒ 滚动摘要链已经写好的摘要被刚说过的 500 字原话覆盖，
//    并且 `syncMeeting` 还把它同步到云端。
//
// 与 §107 同族：**降级产物躺在「成功」字段里**。
// §107 是「不展示」，本条是「展示了，还顶掉了更好的那份」。
//
// ⚠ 与 §107 同一处纪律：这里测的是**纯函数的返回值**，
//   而不是「updateMeeting 被调用了几次」—— 后者需要把整个 ingest 链
//   （createNote / createLocalTodos / ensureNextMeetingEvent / syncMeeting）
//   全部 mock 掉，而那会让判据依赖 mock 的正确性而不是产品语义。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { resolveIngestWriteback } from './refined-ingest-writeback.ts'

const HERE = dirname(fileURLToPath(import.meta.url))
const read = (f: string) => readFileSync(join(HERE, f), 'utf8')

const GOOD_SUMMARY = '对齐了悬界芯片二期量产节点，张伟负责跟进。'
const REFINED = '张伟：悬界芯片二期十一月底量产。\n李娜：我们下周三对齐。'
/** 降级时 refine.refinedTranscript 里装的就是这个 —— 原文，不是修正结果 */
const RAW = '张伟：悬界这边二期十月底量产。李那我们下周三对一下。'

const helperSrc = read('refined-ingest-writeback.ts')
const blank = (m: string) => m.replace(/[^\n]/g, ' ')
const stripComments = (t: string) => t
  .replace(/\/\*[\s\S]*?\*\//g, blank)
  .split('\n').map((l) => { const i = l.indexOf('//'); return i < 0 ? l : l.slice(0, i) }).join('\n')

describe('A · 判定层：降级不得改摘要', () => {
  it('★ status=refine-failed ⇒ summary 必须是 null（调用方一个字都不许改）', () => {
    const r = resolveIngestWriteback({
      existingSummary: GOOD_SUMMARY,
      refine: { refinedTranscript: RAW },
      status: 'refine-failed',
    })
    assert.equal(r.summary, null,
      '降级时仍返回了一个摘要值 ⇒ 原文会被写进去，顶掉滚动摘要链那一版')
    assert.equal(r.status, 'refine-failed')
    assert.equal(r.refinedTranscript, RAW, '精校全文仍要回写（§107 之后它是要展示的）')
  })

  it('成功路径：行为不变（精校全文前 500 字当预览）', () => {
    const r = resolveIngestWriteback({
      existingSummary: GOOD_SUMMARY,
      refine: { refinedTranscript: REFINED },
      status: 'refined',
    })
    assert.equal(r.status, 'refined')
    assert.equal(r.summary, REFINED.slice(0, 500))
  })

  it('成功但精校结果为空 ⇒ 回落用已有摘要，不写空串', () => {
    const r = resolveIngestWriteback({
      existingSummary: GOOD_SUMMARY,
      refine: { refinedTranscript: '' },
      status: 'refined',
    })
    assert.equal(r.summary, GOOD_SUMMARY,
      '空串会把已有摘要**清成空** ⇒ 必须回落到已有摘要，且调用方不得传空串')
    assert.notEqual(r.summary, '')
  })

  it('★ 本函数不得自己再判一次降级（判定只归调用方）', () => {
    // 如果它按 refine 里的 fromFallback 自己判一遍，那么「落点没按判定取值」
    // 这件事就会隐形 —— 那正是 refine-rejected-notice 那道门要防的。
    assert.ok(
      !/fromFallback/.test(helperSrc.replace(/\/\*[\s\S]*?\*\//g, '')),
      'helper 里出现了 fromFallback ⇒ 它在自己判降级，判定被复制了一份',
    )
  })

  it('负控：旧写法在降级样本上确实会把摘要改掉', () => {
    // §108 之前的原样实现
    const legacy = (refined: string, existing: string | null) => refined.slice(0, 500) || existing
    assert.equal(legacy(RAW, GOOD_SUMMARY), RAW.slice(0, 500),
      '负控前提：旧实现会用原文顶掉摘要')
    assert.equal(
      resolveIngestWriteback({
        existingSummary: GOOD_SUMMARY,
        refine: { refinedTranscript: RAW },
        status: 'refine-failed',
      }).summary,
      null,
      '新实现必须拒绝 —— 与上面是同一件事的正反面',
    )
  })
})

describe('B · 接线层：ingest 必须真的按判定结果写', () => {
  const src = stripComments(read('meeting-ingest.ts'))

  it('★ 不得再出现「slice(0, 500) || meeting.summary」这种无条件回写', () => {
    assert.equal(
      /\.slice\(\s*0\s*,\s*500\s*\)\s*\|\|/.test(src), false,
      '那一行还在 ⇒ 降级时原文仍会顶掉摘要（本条判据是 §108 的直接形态）')
    assert.ok(/resolveIngestWriteback\s*\(/.test(src), 'ingest 没有调用判定函数')
  })

  it('summary 为 null 时必须**省略该键**，而不是传空串', () => {
    // ⚠ 传空串 ≠ 不传：空串会把已有摘要清成空。
    assert.match(
      src, /writeback\.summary\s*===\s*null\s*\?\s*\{\}\s*:\s*\{\s*summary/,
      '未找到「summary 为 null 就省略键」的写法 ⇒ 降级会把摘要清空',
    )
  })

  it('负控：判据必须在本文件上真的咬得住', () => {
    assert.ok(/resolveIngestWriteback/.test(src), '正控失败：真源码里找不到 resolveIngestWriteback')
    assert.ok(/writeback\.status/.test(src), '正控失败：真源码里没用 writeback.status')

    const withLegacyLine = src.replace(
      /const writeback = resolveIngestWriteback\(\{/,
      'const writeback = resolveIngestWriteback({ summaryHint: refine.refinedTranscript.slice(0, 500) || meeting.summary,',
    )
    assert.equal(
      /\.slice\(\s*0\s*,\s*500\s*\)\s*\|\|/.test(withLegacyLine), true,
      '负控失效：把旧写法塞回去后判据仍不命中',
    )

    const withoutOmit = src.replace(
      /writeback\.summary\s*===\s*null\s*\?\s*\{\}\s*:\s*\{\s*summary/,
      'writeback.summary ?? "" ,',
    )
    assert.equal(
      /writeback\.summary\s*===\s*null\s*\?\s*\{\}\s*:\s*\{\s*summary/.test(withoutOmit), false,
      '负控失效：去掉「省略键」后判据仍命中',
    )
  })
})