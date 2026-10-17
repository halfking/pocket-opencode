// action-failure-notice.test.ts — §838：对已有记录的五个动作失败不得无声；
// 附件清理失败不得被 `catch { /* ok */ }` 吞掉；笔记软删必须排在文件清理之前。
//
// **D1 五个处理器此前都没有 try/catch**：会议列表的归档/恢复/删除、
// 会议详情菜单的归档/恢复/删除、笔记元信息保存/删除、笔记详情删除。
// 列表变更刻意排在 await **之后**（不能假装成功，这点是对的），但**零反馈**。
//
// **D2 两处 `catch { /* ok */ }`**：会议删除后的音频清理。
//   deleteMeetingAudio 写的是 IndexedDB（openDB + 事务）⇒ 失败时音频 blob
//   **永久残留**且零痕迹 ⇒ 那是静默的存储泄漏，而注释声称没关系。
//
// **D3 `deleteNote` 的顺序**：原来是「deleteNoteFiles → 标记 deleted_at」
//   ⇒ 文件删不掉时**连软删都没发生** ⇒ 用户点删除，笔记原封不动且零提示。
//   ⚠ 同一个文件在媒体替换那处写下了相反的原则（「删除永远不能排在写之前——
//     写失败就没有回滚了」）⇒ 这条路径是遗漏，不是取舍。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import {
  describeRecordActionFailure,
  RECORD_ACTION_FAIL_TAG,
  RECORD_MEDIA_CLEANUP_FAIL_TAG,
  type RecordAction,
} from '../../utils/action-failure-notice.ts'

const HERE = dirname(fileURLToPath(import.meta.url))
const read = (rel: string) => readFileSync(join(HERE, rel), 'utf8')
const LIST = read('../../features/meetings/MeetingListView.vue')
const STUDIO = read('../../features/meetings/use-meeting-studio.ts')
const NOTES = read('../../features/notes/NoteListView.vue')
const DETAIL_NOTE = read('../../features/notes/NoteDetailView.vue')
const PERSIST = read('../../features/notes/notes-persist.ts')

const codeOnly = (text: string): string =>
  text.split('\n').filter((l) => {
    const t = l.trim()
    return !(t.startsWith('//') || t.startsWith('*') || t.startsWith('/*'))
  }).join('\n')

const fnBody = (code: string, name: string, max = 2000): string => {
  const at = code.indexOf(name)
  assert.ok(at > 0, `★ 阳性对照失败：找不到 ${name}`)
  const rest = code.slice(at + name.length, at + name.length + max)
  const next = rest.search(/\n(?:export )?(?:async )?function [A-Za-z_$]/)
  return next > 0 ? code.slice(at, at + name.length + next) : code.slice(at, at + name.length + max)
}

describe('§838 · 文案（行为，纯函数）', () => {
  const ACTIONS: RecordAction[] = ['archive', 'restore', 'delete', 'meta-save', 'meta-delete']

  it('★★★ 每个动作都必须说清「东西还在不在」（用户据此决定要不要再点一次）', () => {
    for (const a of ACTIONS) {
      const t = describeRecordActionFailure(a, { error: new Error('SQLITE_BUSY') })
      assert.match(t, /仍在|还在|没有变动|已从列表删除/, `${a} 没说清数据现在的状态`)
    }
  })

  it('★★★ 每个动作都必须给可行动作', () => {
    for (const a of ACTIONS) {
      const t = describeRecordActionFailure(a, { error: new Error('boom') })
      assert.match(t, /重试|打开|检查/, `${a} 只报错不给路`)
    }
  })

  it('★★★ 「主体已删但媒体没清干净」必须与「删除失败」是两句不同的话', () => {
    const leaked = describeRecordActionFailure('delete', { error: new Error('x'), mediaCleaned: false })
    const failed = describeRecordActionFailure('delete', { error: new Error('x') })
    assert.notEqual(leaked, failed, '两种处境给了同一句文案')
    assert.match(leaked, /不必重试|删除本身不必重试/, '残留情形没告诉用户「不用再点删除」')
    assert.match(leaked, /空间/, '没说清残留会占空间（用户会以为没影响）')
  })

  it('★★ 元信息保存失败必须说清面板还开着（这正是修复带来的行为）', () => {
    const t = describeRecordActionFailure('meta-save', { error: new Error('x') })
    assert.match(t, /保持打开/, '没说清面板状态')
    assert.match(t, /输入框/, '没说清刚填的内容还在')
  })

  it('★ 未知动作仍必须给一句人话（不得空串 / undefined）', () => {
    const t = describeRecordActionFailure('nope' as RecordAction)
    assert.ok(t.length > 0, '未知动作产出空串')
    assert.doesNotMatch(t, /undefined|\[object|NaN/, `未知动作产出假读数：${t}`)
  })

  it('负控：任意异常形态都不得把技术串漏给用户', () => {
    for (const e of [null, undefined, 42, {}, { message: 'SQLITE_FULL: database or disk is full' }]) {
      for (const a of ACTIONS) {
        const t = describeRecordActionFailure(a, { error: e })
        assert.doesNotMatch(t, /undefined|\[object Object\]|NaN|SQLITE/, `${a}/${String(e)} 漏了技术串：${t}`)
      }
    }
  })

  it('★ 两个诊断前缀必须区分得开（主体操作失败 vs 媒体残留）', () => {
    assert.match(RECORD_ACTION_FAIL_TAG, /操作失败/, '')
    assert.match(RECORD_MEDIA_CLEANUP_FAIL_TAG, /媒体|文件/, '')
    assert.notEqual(RECORD_ACTION_FAIL_TAG, RECORD_MEDIA_CLEANUP_FAIL_TAG, '两个前缀撞了')
  })
})

// ── 接线（源码扫描，⚠ 对「信号变死代码」零牙，只保证真的接上了）──
describe('§838 · 五个处理器必须 try/catch（扫描层）', () => {
  const cases: [string, string, string][] = [
    ['会议列表 归档', LIST, 'async function onArchive(id: string) {'],
    ['会议列表 恢复', LIST, 'async function onRestore(id: string) {'],
    ['会议列表 删除', LIST, 'async function onDelete(id: string) {'],
    ['笔记 元信息保存', NOTES, 'async function onMetaSave('],
    ['笔记 元信息删除', NOTES, 'async function onMetaDelete() {'],
    ['笔记详情 删除', DETAIL_NOTE, 'async function onDelete() {'],
  ]
  for (const [name, src, anchor] of cases) {
    it(`★★ ${name} 必须 try/catch 且失败要出声`, () => {
      const body = fnBody(codeOnly(src), anchor)
      assert.match(body, /catch \(e\)/, `${name} 没有 catch ⇒ 写库失败时零反馈`)
      assert.match(body, /console\.error\(RECORD_ACTION_FAIL_TAG/, `${name} 的失败没有进日志`)
      assert.match(body, /toast\.error\(describeRecordActionFailure\(/, `${name} 没有用纯函数出文案`)
    })
  }

  it('★★ 菜单侧的归档/恢复也必须兜住（此前是裸 await + 无条件成功 toast）', () => {
    const code = codeOnly(STUDIO)
    for (const [action, label] of [['archive', '已归档'], ['restore', '已恢复']] as const) {
      assert.match(code, new RegExp(`catch \\(e\\)[\\s\\S]{0,300}RECORD_ACTION_FAIL_TAG, '${action}'`),
        `菜单侧 ${label} 没有兜住`)
      assert.match(code, new RegExp(`catch \\(e\\)[\\s\\S]{0,300}describeRecordActionFailure\\('${action}'`),
        `菜单侧 ${label} 没有用纯函数出文案`)
    }
  })

  it('★★★ 本轮经手的文件里不得再有「吞掉失败」的 catch', () => {
    // ⚠ 判据形状是**刻意收窄**的：第一版写成「任何裸 catch 都禁止」，结果命中了
    //   一堆**正当**的既有写法（`catch { draftBanner.value = null }`、
    //   `catch { return [] }`、`catch { /* empty */ }` …）⇒ 那是一条会逼着人去改
    //   无辜代码的假规矩。
    //   ⇒ 真正的缺陷形状只有两个：**完全空的** catch，和**声称「没关系」**的 catch。
    //     裸 catch 但有显式兜底（设成 null / 返回空 / 留注释说明为何不重复记日志）
    //     是合法写法，本门不管。
    // ⚠ 逐个 catch 块检查，而不是用一条跨行正则 —— 正则会跨注释行误判
    //   （notes-persist 有一处「块内只有解释性注释」的正当空 catch，
    //    剥掉注释后它就长得跟「完全空」一模一样）。
    const bareBlocks = (src: string): { line: number; raw: string }[] => {
      const lines = src.split('\n')
      const out: { line: number; raw: string }[] = []
      for (let i = 0; i < lines.length; i += 1) {
        if (!/\}\s*catch \{\s*$/.test(lines[i])) continue
        const body: string[] = []
        let j = i + 1
        for (; j < lines.length && j <= i + 8; j += 1) {
          body.push(lines[j])
          if (/^\s*\}/.test(lines[j])) break
        }
        out.push({ line: i + 1, raw: body.join('\n') })
        i = j
      }
      return out
    }

    for (const [name, src] of [
      ['会议列表', LIST], ['会议菜单', STUDIO], ['笔记 store', PERSIST],
      ['笔记列表', NOTES], ['笔记详情', DETAIL_NOTE],
    ] as const) {
      const code = codeOnly(src)
      const claimed = code.match(/catch \{\s*\/\*\s*ok\s*\*\/\s*\}/g) ?? []
      assert.equal(claimed.length, 0,
        `${name} 有 ${claimed.length} 处写着 /* ok */ 的 catch ⇒ 那是本节要修的谎报`)
      for (const b of bareBlocks(src)) {
        const bodyCode = codeOnly(b.raw).trim()
        // 空体（含「只有注释」的体）必须**带一句说明为什么可以不处理**。
        const justified = b.raw.includes('//') || b.raw.includes('/*') && /\*\/[^*]/.test(b.raw)
        assert.ok(bodyCode !== '' || justified,
          `${name}:${b.line} 是一个**没有任何说明**的空 catch ⇒ 失败被无声吞掉。` +
          '若确实可以不处理，请在块内写一句理由（仓库里有这种正当写法）。')
      }
    }
  })
})

describe('§838 D2/D3 · 媒体清理与软删顺序（扫描层）', () => {
  it('★★★ 会议音频清理失败必须说清「主体已删、但音频残留」', () => {
    const code = codeOnly(LIST)
    assert.match(code, /catch \(e\) \{[\s\S]{0,400}RECORD_MEDIA_CLEANUP_FAIL_TAG/,
      '会议列表删除的音频清理失败没有独立诊断')
    assert.match(code, /describeRecordActionFailure\('delete', \{ error: e, mediaCleaned: false \}\)/,
      '会议列表没有按「媒体未清干净」出文案')
    const studio = codeOnly(STUDIO)
    // ⚠ 锚点必须是**调用点之后**的那次出现：文件开头还有一条同名 import，
    //   用 indexOf 会落在 import 上（第一版阳性对照就是这么翻的）。
    const atDel = studio.indexOf('await deleteMeetingAudio(m.id)')
    assert.ok(atDel > 0, '★ 阳性对照失败：找不到音频清理的调用点')
    const tagAt = studio.indexOf('RECORD_MEDIA_CLEANUP_FAIL_TAG', atDel)
    assert.ok(tagAt > atDel, '会议菜单侧的音频清理失败仍被吞掉（调用点之后没有诊断）')
    // ⚠ 只判「文件里有这个前缀」不够：把 `catch (e) {` 改成 `catch {` 而诊断行
    //   仍在时照样通过（实测 0 红）⇒ 这里取**该前缀之前最近的 catch**，
    //   要求它绑定了异常对象。
    //   ⚠ 刻意不用「往前 200 字符」这种固定窗口：本会话已连续 4 次因窗口越界翻车。
    const head = studio.slice(0, tagAt)
    const lastCatch = head.lastIndexOf('catch')
    assert.ok(lastCatch >= 0, '★ 阳性对照失败：该诊断行之前没有 catch')
    assert.match(head.slice(lastCatch, lastCatch + 12), /catch \(e\)\s*\{/,
      '音频清理的 catch 没有绑定异常对象（拿不到失败原因）')
  })

  it('★★★ deleteNote 必须先软删、再清理文件（否则文件删不掉 ⇒ 笔记根本没删掉）', () => {
    const body = fnBody(codeOnly(PERSIST), 'export async function deleteNote(', 2600)
    const atSoft = body.indexOf('deleted_at')
    const atFiles = body.indexOf('deleteNoteFiles(')
    const atVector = body.indexOf('vectorIndex.remove(')
    assert.ok(atSoft > 0, '★ 阳性对照失败：找不到软删那一步')
    assert.ok(atFiles > 0, '★ 阳性对照失败：找不到文件清理那一步')
    assert.ok(atSoft < atFiles, '文件清理排在软删**之前** ⇒ 删不掉时整条删除没生效（原缺陷）')
    assert.ok(atSoft < atVector, '向量清理排在软删之前 ⇒ 清理失败会让整条删除没生效')
  })

  it('★★★ 文件与向量清理各自必须兜住，且 catch 体里必须有诊断', () => {
    // ⚠ 不能只判「有 catch」：把 `catch (e) {` 改成 `catch {` 时那条断言照样通过。
    //   ⇒ 这里要求 catch 体内出现该有的诊断（变异 G/H 实测 0 红）。
    const body = fnBody(codeOnly(PERSIST), 'export async function deleteNote(', 2600)
    const atFiles = body.indexOf('deleteNoteFiles(')
    const atVector = body.indexOf('vectorIndex.remove(')
    assert.ok(atFiles > 0 && atVector > 0, '★ 阳性对照失败：找不到两处清理')
    // ⚠ 一律取「锚点之后**最近**的那个 catch」，不用「锚点起 N 字符」：
    //   两处清理只隔几行，固定窗口会把**下一处**的 catch (e) 算进来（变异 G 实测 0 红）。
    const nearestCatch = (from: number): string => {
      const at = body.indexOf('catch', from)
      assert.ok(at > from, '★ 阳性对照失败：锚点之后没有 catch')
      return body.slice(at, at + 12)
    }
    assert.match(nearestCatch(atFiles), /catch \(e\)\s*\{/,
      '文件清理没有绑定异常对象的 catch ⇒ 拿不到原因')
    assert.match(body.slice(atFiles, atVector), /RECORD_MEDIA_CLEANUP_FAIL_TAG/,
      '文件清理的 catch 体里没有诊断 ⇒ 孤儿文件仍是零痕迹')
    assert.match(nearestCatch(atVector), /catch \(e\)\s*\{/,
      '向量清理没有绑定异常对象的 catch')
    // ⚠ 区域用**相邻锚点**划界，不用「往后 N 字符」，也不靠第一个 '}'：
    //   前者会越界（§837 已栽过），后者会撞在 `} catch` 的那个 '}' 上
    //   （第一版就是这么恒红的）。向量那段是本函数最后一步 ⇒ 取到末尾。
    assert.match(body.slice(atVector), /console\.warn\(/,
      '向量清理的 catch 体里没有诊断 ⇒ 残留命中无从排查')
  })

  it('⚠ 前提登记：本文件在媒体替换处写下了同一条原则（D3 是遗漏，不是取舍）', () => {
    // ⚠ 这里必须读**原文**而不是 codeOnly：判的是一句注释，
    //   而 codeOnly 的定义就是剥掉注释行（第一版犯了这个错，恒红）。
    assert.match(PERSIST, /清理排在\*\*写成功之后\*\*/, '那句原则注释被删了 ⇒ 请重新判断 D3 的正当性')
  })
})
