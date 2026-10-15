// meeting-meta-glossary.test.ts — §119：界面上「术语表」说明承诺的字段集合，
// 必须与后端 `metaTermHint` **真正读到**的字段集合一致。
//
// ── 这道门量的是什么 ────────────────────────────────────────────────
//
// 用户原始诉求是「录音转写的不太准确」。准确率有一部分是**用户自己能改的**：
// 会议标题 / 地点 / 参会人会作为术语表喂进录音后的精校（server_meeting.go
// 的 metaTermHint → buildRefinePrompt，§106 已钉住逐字等价）。
//
// 此前界面上**一个字都没提**。加了说明之后，新的风险出现了：
// 说明一旦开始列字段，它就会腐烂，而且**腐烂时没人知道** ——
// 后端改了字段集合，界面照旧承诺一个已经不用的字段。
//
// ⇒ 字段集合不是手抄的：Go 侧 `TestMetaTermHintFieldContract`
//   用**行为 + 反射**逐个字段单喂 metaTermHint（空输出 = 没被读），
//   落成 fixtures/meeting-meta-glossary-fields.json，每次 go test 比对。
//   本门消费那份制品。
//
// ⚠ §119.2 第一版踩的坑：**必须先剥 HTML 注释**。
//   说明块上方的注释里写着「它读 Title/Participants/Location，不读 Topic
//   ⇒ 这里提「主题」就是撒谎」—— 不剥注释的话，`主题` 在源码里出现，
//   「不许提到未用字段」这条断言会恒红，而「提到全部已用字段」恒绿。
//   与 §109.4 同一类：判据扫到注释里的字面量就失明。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { FRONTEND_ROOT } from './_wire-helpers.mjs'

const SHEET = 'src/features/meetings/MeetingSettingsSheet.vue'

const contract = JSON.parse(
  readFileSync(
    join(FRONTEND_ROOT, 'src/api/__tests__/fixtures/meeting-meta-glossary-fields.json'),
    'utf8',
  ),
) as { declared: string[]; used: string[]; unused: string[] }

/** 后端字段名 → 界面上的标签文字。 */
const FIELD_LABEL: Record<string, string> = {
  title: '标题',
  participants: '参与人',
  location: '地点',
  // §159 补：Topic 早已接进 metaTermHint（Go 夹具 used 里含 topic），
  // 此前这里缺它 ⇒ 判据对 topic 是失明的（`assert.ok(label)` 会一直红，
  // 而那条红被当成「fixture 坏了」忽略过一次）。
  topic: '主题',
}

/**
 * 界面上有输入框、但**后端 meetingMetaIn 里没有对应字段**的那些。
 *
 * ⚠ 2026-10-07（§159）：**「主题」已从这里移出**，此前它登记在此。
 *   当时的三条理由（① 界面有输入框 ② 后端只有三个字段 ③ 所以提到它就是撒谎）
 *   在 ①② 上都成立，但 ② 已经被推翻：`meetingMetaIn.Topic` 早已接入
 *   （§138.3/§143），Go 契约夹具现在是 `used=[location participants title topic]`。
 *   ⇒ 主题**现在必须被说明提到**（FIELD_LABEL 那条判据在管），
 *     留在本表里只会让「不许提」这条判据靠 `declared` 短路，形成假象。
 *
 * 「标签」仍在：它只有界面输入框，**到不了**术语表 —— 说明里提它才是撒谎。
 */
const UI_ONLY_LABEL: Record<string, string> = {
  tags: '标签',
}

/** 剥 HTML 注释（等长空白替换，保持偏移）。漏了它判据就会读到自己写的注释。 */
function stripHtmlComments(src: string): string {
  return src.replace(/<!--[\s\S]*?-->/g, (m) => m.replace(/[^\n]/g, ' '))
}

function glossaryNoteText(): string {
  const src = stripHtmlComments(readFileSync(join(FRONTEND_ROOT, SHEET), 'utf8'))
  // 取 class="glossary-note" 那个 <p> 的正文。class 可能排在 v-if 之后，
  // 所以不假设属性顺序。
  const m = src.match(/<p[^>]*class="[^"]*glossary-note[^"]*"[^>]*>([\s\S]*?)<\/p>/)
  if (!m) {
    throw new Error(
      `在 ${SHEET} 里找不到 class="glossary-note" 的说明段落。\n` +
      `若确实是有意删掉了这条说明，请连同本测试一起删 —— ` +
      `留着一条找不到锚点的门，下次有人重写这个文件时它会报一句看不懂的错。`,
    )
  }
  return m[1].replace(/<!--[\s\S]*?-->/g, ' ').replace(/\s+/g, ' ').trim()
}

describe('§119 术语表说明与后端字段一致', () => {
  it('契约夹具有效（量具先自证）', () => {
    assert.ok(Array.isArray(contract.used) && contract.used.length >= 2,
      `used 只有 ${contract.used?.length} 个，取值路径可能坏了`)
    assert.ok(Array.isArray(contract.declared), '契约缺 declared')
    assert.ok(Array.isArray(contract.unused), '契约缺 unused')
  })

  it('后端没有声明却无人读的字段（unused 必须为空）', () => {
    // Go 侧同一断言也有一份；这里再钉一次，因为它直接决定
    // 「界面承诺的字段」到底该以什么为准。
    assert.deepEqual(contract.unused, [],
      `meetingMetaIn 声明了但 metaTermHint 不读：${contract.unused.join(', ')}` +
      ` —— 这些字段会随请求发上来又静静丢掉`)
  })

  it('说明必须提到**全部**真正被读到的字段', () => {
    const text = glossaryNoteText()
    assert.ok(text.length > 0, '说明段落是空的')
    const missing = contract.used.filter((f) => {
      const label = FIELD_LABEL[f]
      assert.ok(label, `未登记标签的后端字段 ${f} —— 请在 FIELD_LABEL 里补，否则判据失明`)
      return !text.includes(label)
    })
    assert.deepEqual(missing, [],
      `这些字段真的被 metaTermHint 读了，说明里却没提：${missing.join(', ')}\n` +
      `用户按说明填字段 ⇒ 漏掉的字段他不会填 ⇒ 准确率提升白拿。`)
  })

  it('★ 说明不得提到后端根本没声明的字段', () => {
    const text = glossaryNoteText()
    const lying = Object.entries(UI_ONLY_LABEL).filter(([field, label]) => {
      if (contract.declared.includes(field)) return false
      return text.includes(label)
    })
    assert.deepEqual(lying.map(([, l]) => l), [],
      `说明里提到了后端 meetingMetaIn 没有的字段：` +
      `${lying.map(([, l]) => l).join(', ')}。用户会照着填，但那些值根本不会参与精校 —— ` +
      `这是撒谎，比不说更糟。`)
  })

  it('说明必须就位在参与人之后、标签之前（对应它承诺的那几个字段）', () => {
    // 纯位置断言：说明若被挪到别处，它与「上面那几个字段」的对应关系就断了。
    const src = stripHtmlComments(readFileSync(join(FRONTEND_ROOT, SHEET), 'utf8'))
    const participantsAt = src.indexOf('v-model="form.participants"')
    const noteAt = src.indexOf('class="glossary-note"')
    const tagsAt = src.indexOf('v-model="form.tags"')
    assert.ok(participantsAt > 0 && noteAt > 0 && tagsAt > 0, '三个锚点必须都在')
    assert.ok(participantsAt < noteAt && noteAt < tagsAt,
      '说明必须夹在「参与人」与「标签」之间，否则读起来不像是在讲上面那几个字段')
  })
})