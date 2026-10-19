/**
 * Run: node --test --experimental-strip-types src/features/notes/note-edit-helpers.test.ts
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { parseTagsInput, tagsFromArray, dataUrlToBlob, attachmentsToMedia, DataUrlFormatError } from './note-edit-helpers.ts'

const HERE = dirname(fileURLToPath(import.meta.url))
// ⚠ 与 note-edit-helpers.ts **同目录**（不是上一级 features/）——第一版写成 '../'
//   报 ENOENT，而 node:test 对「suite 构造期失败」的计数是 `# fail 0`：
//   ⚠⚠ 只看 `# fail 0` 会把一条**根本没跑**的判据当成全绿。必须同时看 `not ok`。
const NOTE_EDIT_VIEW = resolve(HERE, 'NoteEditView.vue')

describe('parseTagsInput', () => {
  it('splits chinese and english commas', () => {
    assert.deepEqual(parseTagsInput('周报, OKR，待办'), ['周报', 'OKR', '待办'])
  })
})

describe('tagsFromArray', () => {
  it('joins tags or returns empty', () => {
    assert.equal(tagsFromArray(['a', 'b']), 'a, b')
    assert.equal(tagsFromArray(null), '')
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// §890 D60：「data URL → Blob」在格式坏掉时**静默**产出 0 字节附件。
//
// 实测（改动前）：下面四种输入全部 size=0、不抛错、不告警 ——
//   'data:image/png;base64'（无逗号）/ ''（空串）/ 'data:' / 'https://cdn…/a.png'
// 而 base64 被非法字符污染时 `atob` **会抛** ⇒ 格式坏掉一半大声一半无声。
// 与 §888 的 D58 同型：把「读到了但内容是空的」与「没读到」混为一谈。
// ─────────────────────────────────────────────────────────────────────────────
describe('§890 · D60：data URL 缺 payload 必须出声，不能静默产出 0 字节附件', () => {
  // ⚠ 这四条是**同一个性质**的四个入口：少一个逗号 / 整体为空 / 只有 data: 前缀 /
  //   压根不是 data URL。任一条退回静默，D60 就复活了。
  const MUST_THROW: [string, string][] = [
    ['没有逗号（payload 丢失）', 'data:image/png;base64'],
    ['空串', ''],
    ['只有 data: 前缀', 'data:'],
    ['https 外链（useAttachments 注释宣称支持、实际未被行使）', 'https://cdn.example.com/a.png'],
  ]

  for (const [name, input] of MUST_THROW) {
    it(`★ 拒绝：${name}`, () => {
      assert.throws(
        () => dataUrlToBlob(input),
        DataUrlFormatError,
        `dataUrlToBlob(${JSON.stringify(input)}) 必须抛 DataUrlFormatError，` +
          `不能返回一个 0 字节 Blob —— 那会被 persistNotePayload 当成功落盘`,
      )
    })
  }

  it('★ 抛的是具名错误（不是裸 DOMException），且信息里带得上原文摘要', () => {
    // ⚠ 为什么钉「具名」：onSave 的 catch 走 apiError(e, 'errors.saveFailed')，
    //   裸 DOMException 在日志里既不知道是哪个附件，也不知道坏在编码前还是解码时。
    try {
      dataUrlToBlob('data:image/png;base64')
      assert.fail('本该抛出却没抛')
    } catch (e) {
      assert.ok(e instanceof DataUrlFormatError, `必须是 DataUrlFormatError，实得 ${e?.constructor?.name}`)
      assert.match((e as Error).message, /data URL/, '错误信息要说明坏在 data URL 这一层')
      assert.match((e as Error).message, /base64/, '错误信息要带上原文摘要，便于定位是哪张图')
    }
  })

  it('★ payload 存在但不是合法 base64：仍然抛具名错误（不许退回无声）', () => {
    assert.throws(() => dataUrlToBlob('data:image/png;base64,@@@@'), DataUrlFormatError)
  })

  it('★★ 正常 data URL：size 与 mime 与改动前逐字节一致（零行为变化）', async () => {
    // 「iVBORw0KGgo=\" 是 1x1 PNG 的 base64 头（8 字节）。
    const blob = dataUrlToBlob('data:image/png;base64,iVBORw0KGgo=')
    assert.equal(blob.size, 8)
    assert.equal(blob.type, 'image/png')
    // 逐字节对上：改动前的实现也是 atob → charCodeAt → Uint8Array。
    assert.deepEqual([...new Uint8Array(await blob.arrayBuffer())], [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])
  })

  it('★★★ 负控：复刻改动前的实现，确认它真的静默返回 size=0（证明本门有区分力）', () => {
    // ⚠ 本仓「负控自伤」教训：必须先确认**对照本身**确实有病，
    //   否则一组「全绿」只说明两半都没测到东西。
    const legacy = (dataUrl: string): Blob => {
      const [head, body] = dataUrl.split(',')
      const mime = /data:([^;]+)/.exec(head || '')?.[1] || 'application/octet-stream'
      const bin = atob(body || '')
      const bytes = new Uint8Array(bin.length)
      for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
      return new Blob([bytes], { type: mime })
    }
    for (const [, input] of MUST_THROW) {
      const blob = legacy(input)
      assert.equal(blob.size, 0, `负控假定失效：旧实现在 ${JSON.stringify(input)} 上不再是 size=0`)
    }
  })
})

describe('§890 · D60：mime 只有一个推导口径', () => {
  it('★ mime 必须取自 blob.type，不许再从 dataUrl 字符串上切', () => {
    // 原写法 `a.dataUrl.split(';')[0]?.replace('data:','')` 对 https 外链会把
    // **整个 URL** 当成 mime 写进 local_note_files.mime。
    const [media] = attachmentsToMedia([{ dataUrl: 'data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=', name: 'a.svg' }])
    assert.equal(media.mime, 'image/svg+xml')
    assert.equal(media.blob.type, 'image/svg+xml')
  })

  it('★ 外链不会走到 mime 那一行了（先被 dataUrlToBlob 拦下）', () => {
    // 这是「先抛错」与「事后修正 mime」的取舍：§890 取前者 ⇒ mime 行只需一个推导口径。
    assert.throws(() => attachmentsToMedia([{ dataUrl: 'https://cdn.example.com/a.png', name: 'a.png' }]), DataUrlFormatError)
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// §890 的**修法自伤护栏**：把 dataUrlToBlob 改成抛错，如果 onSave 的媒体收集
// 还在 try 之外，抛错会绕过 catch ⇒ saving.value 永久卡在 true
// ⇒ 第 221 行 `if (saving.value) return` ⇒ 保存按钮再也点不动且无任何提示。
// ⚠ 这就是「把一个静默缺陷换成一个卡死缺陷」——所以这两处必须同时落地。
// ─────────────────────────────────────────────────────────────────────────────
describe('§890 · D60：onSave 的媒体收集必须在 try 之内（否则抛错会卡死保存按钮）', () => {
  const src = readFileSync(NOTE_EDIT_VIEW, 'utf8')

  it('★★ 自洽性：collectComposerMedia() 的位置晚于 try {（不是「出现过」就够）', () => {
    // ⚠ §886 的教训第四种假绿：「出现过」≠「用在这里」。
    //   这里量的是**相对位置**，正是「谁产生了值」与「谁消费了值」的连接关系。
    const start = src.indexOf('async function onSave()')
    assert.ok(start > 0, '定位失败：找不到 onSave()，源码结构与预期不符')
    const tryAt = src.indexOf('try {', start)
    const collectAt = src.indexOf('collectComposerMedia()', start)
    assert.ok(tryAt > start, 'onSave 里找不到 try {')
    assert.ok(collectAt > start, 'onSave 里找不到 collectComposerMedia()')
    assert.ok(
      collectAt > tryAt,
      `collectComposerMedia() 在 try 之前（try@${tryAt - start} collect@${collectAt - start}）—— ` +
        'dataUrlToBlob 抛错时会绕过 catch，saving 卡在 true，保存按钮永久失效',
    )
  })

  it('★★ catch 必须复位 saving.value（抛错要有出路，不能是死路）', () => {
    const start = src.indexOf('async function onSave()')
    const catchAt = src.indexOf('} catch', start)
    assert.ok(catchAt > start, 'onSave 里找不到 catch')
    const tail = src.slice(catchAt, start + 3000)
    assert.match(tail, /saving\.value\s*=\s*false/, 'catch 里必须复位 saving.value')
    assert.match(tail, /saveError\.value\s*=/, 'catch 里必须给用户提示')
  })

  it('★ 反向：不得动 submit-guard-audit 登记表钉的重入守卫', () => {
    // ⚠ submit-guard-audit.test.ts:121 登记 `NoteEditView.vue::onSave`
    //   的阴性理由是「函数体显式 if (saving.value) return」。
    //   本节的改动不得顺手删掉它 —— 那是他人判据的语义。
    const start = src.indexOf('async function onSave()')
    assert.match(src.slice(start, start + 200), /if \(!canSave\.value \|\| saving\.value\) return/,
      'onSave 的显式重入守卫必须保留（submit-guard-audit 的登记表依赖它）')
  })
})
