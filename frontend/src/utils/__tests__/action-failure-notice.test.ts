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

// ─────────────────────────────────────────────────────────────────────────────
// §884 D54：本地编辑路径可以**静默改写墓碑行**，而「软删」在本仓是单向不可逆的。
//
// 修复前的完整链条（每一步都有代码证据）：
//   1. enex 导入 ⇒ local_assets 一行 kind='note', source='enex_import'
//      （import-service.ts:36-51）
//   2. PKM 的 kind 常量 NOTE_KIND 同样等于 'note'（pkm-store.ts:56），
//      且 pkm 的 listNotes 调 assetStore.search 时**不按 source 过滤**
//      ⇒ 这条导入的笔记也会出现在 PKM 列表里，点进去就是 /pkm/n/:id
//   3. /notes 侧能列出它（notes-persist.getNote 第 328 行那条
//      `source === 'enex_import'` 回退就是为此存在的）⇒ 可以删掉它，
//      于是 assetStore.softDeleteForWorkspace 把 deleted_at 置上，界面提示删除成功。
//   4. 浏览器后退 / 分享深链 /pkm/n/:id 重新打开（NotesHubView.vue:29 的注释
//      明写「详情仍走独立深链……分享与历史不受影响」⇒ 深链是**被支持的**访问方式）
//      ⇒ PkmEditor.load → pkm-store.getNote → assetStore.get(id)
//      ⚠ 那条读路径**既不过滤 deleted_at 也不校验 workspace_id**
//      ⇒ 已删除笔记的全文照常显示在编辑器里。
//   5. 改一个字 ⇒ 800ms 后 doSave ⇒ lastSaved=true 界面显示「已保存」
//      ⇒ assetStore.upsert 的 UPDATE 分支里**既没有 deleted_at 也不检查它**
//      ⇒ 墓碑保持，且 pkm.saveNote 不传 source ⇒ source 被写成 NULL。
//   6. 结果：用户保存成功，那条内容却**永远不再出现在 PKM 列表、notes 列表、
//      任何 FTS 搜索里**；又因 source 变成 NULL，notes 侧那条
//      `source === 'enex_import'` 回退也不再成立 ⇒ 它从 notes 侧一并消失。
//      而全仓**没有任何一处**把 deleted_at 清回 NULL
//      （schema.ts:9 写着「保留可恢复」，那个「恢复」从未实现）⇒ 不可恢复。
//
// 一句话：**「保存成功」与「被完成的事」不是同一件**（与 §879 D51 同一族）。
// ─────────────────────────────────────────────────────────────────────────────
describe('§884 · 墓碑不可逆（源码结构，assetStore 在 node 里跑不起来）', () => {
  const ASSET_STORE = read('../../native/asset-store.ts')
  const PKM_STORE = read('../../features/pkm/pkm-store.ts')
  const CONTACTS_STORE = read('../../features/contact/contacts-store.ts')
  const PKM_NOTE_VIEW = read('../../features/pkm/PkmNoteView.vue')
  const PKM_EDITOR = read('../../features/pkm/PkmEditor.vue')

  /** 剥掉注释：判据锚点必须落在真代码上，不能落在自己写的说明文字上。 */
  const strip = (t: string): string =>
    t.replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
     .replace(/(^|[^:])\/\/[^\n]*/g, '$1')
     .replace(/<!--[\s\S]*?-->/g, (m) => m.replace(/[^\n]/g, ' '))

  const upsertBody = (): string => {
    const code = strip(ASSET_STORE)
    const at = code.indexOf('async upsert(')
    assert.ok(at > 0, '★ 阳性对照失败：找不到 assetStore.upsert')
    const rest = code.slice(at)
    const next = rest.slice(1).search(/\n  (?:async |private |static )/)
    return next > 0 ? rest.slice(0, next + 1) : rest
  }

  it('★★★ 改写一行 asset 之前必须先看 deleted_at（不看就等于没看见墓碑）', () => {
    const body = upsertBody()
    // ⚠ 不能只断言「出现过 deleted_at」—— 修复前的 SELECT 里没有它，
    //   而把它加进 SELECT 却不做任何判断，同样能让「出现过」这条通过
    //   ⇒ 这里要求**条件判断形态**（拿不到墓碑就跟没查一样）。
    assert.match(body, /\.deleted_at\s*(?:===|!==|==|!=)\s*(?:null|undefined)/,
      'upsert 没有对 deleted_at 做任何判断 ⇒ 已软删的行会被静默改写（D54）')
  })

  it('★★★ 存在性检查必须把 deleted_at 一起取出来', () => {
    // 与上一条配对：判断要成立，前提是查得到。
    // 钉**读数**（§880 M37 教训：只钉「出现过」会被「只改了量」顶穿）。
    const body = upsertBody()
    const sel = body.slice(0, body.indexOf('if (existing)'))
    assert.match(sel, /SELECT[^;]*deleted_at[^;]*FROM local_assets/,
      '存在性检查的 SELECT 没有取 deleted_at ⇒ 后面就算写了判断也永远拿不到值')
  })

  it('★★★ 墓碑拒绝必须排在 UPDATE 之前', () => {
    const body = upsertBody()
    const atReject = body.search(/\.deleted_at\s*(?:===|!==|==|!=)/)
    const atUpdate = body.search(/UPDATE local_assets/)
    assert.ok(atReject >= 0, '★ 阳性对照失败：找不到墓碑判断')
    assert.ok(atUpdate >= 0, '★ 阳性对照失败：找不到 UPDATE 语句')
    assert.ok(atReject < atUpdate, '墓碑判断排在 UPDATE **之后** ⇒ 先改了再说，等于没拦')
  })

  it('★★★ 不得用「静默清墓碑」的方式修（那是未告知的复活）', () => {
    // 修复必须是**显式拒绝**，不是顺手在 UPDATE 里写 deleted_at=NULL。
    // 后者会把 D54 从「静默改写」变成「静默复活」——用户删掉的东西自己回来了，
    // 而他从没要求过恢复。这条钉住的是**修法的形状**。
    const body = upsertBody()
    const atUpdate = body.search(/UPDATE local_assets/)
    assert.ok(atUpdate >= 0, '★ 阳性对照失败：找不到 UPDATE 语句')
    assert.doesNotMatch(body.slice(atUpdate), /deleted_at\s*=/,
      'UPDATE 语句里写了 deleted_at ⇒ 若目的是清空它，那是静默复活；' +
      '要恢复必须走显式入口并让用户知道')
  })

  it('★★★ PKM 读单条不得再走「不过滤墓碑、也不校验 workspace」的 assetStore.get(id)', () => {
    // 钉**性质**不钉调用写法：要求 getNote 用的是 getForWorkspace
    // （它同时带 workspace_id 与 deleted_at IS NULL 两个条件）。
    const code = strip(PKM_STORE)
    const at = code.indexOf('export async function getNote(')
    assert.ok(at > 0, '★ 阳性对照失败：找不到 pkm-store.getNote')
    const body = code.slice(at, at + 1200)
    assert.match(body, /getForWorkspace\(/,
      'PKM 的 getNote 又用回了不过滤墓碑的读法（D54）')
    assert.doesNotMatch(body, /assetStore\.get\(/,
      'PKM 的 getNote 直接用 assetStore.get ⇒ 已删除笔记仍能被深链打开并改写')
  })

  it('★★★ PKM 的两个读取调用点必须传 workspaceId（否则真机读不到）', () => {
    // getForWorkspace 会校验 workspace_id。列表与保存都用 auth.workspaceId，
    // 只读若退回 'default'，真机 `ws_user-admin` 分区的笔记全部读不到 ——
    // 那正是 PkmNoteView.vue:46 注释记的「保存成功但列表看不到」的同族坑。
    //
    // ⚠⚠ 参数必须**逐字符配平**地取，不能用 `getNote\(([^)]*)\)`：
    //   实参里有嵌套括号 `currentWorkspaceId()`，第一版用 `[^)]*` 取到的是
    //   `'id, currentWorkspaceId('`（在那个 '(' 上就停了，闭合括号没进来）
    //   ⇒ 断言恒红，而代码是对的。这是 §880「要跨结构就配平，不要正则」
    //   在调用实参上的同形复现 —— 量具栽第 12 次。
    const callArgs = (code: string, callee: string): string => {
      const at = code.indexOf(`${callee}(`)
      assert.ok(at > 0, `★ 阳性对照失败：找不到 ${callee}( 调用`)
      const from = at + callee.length // 指向 '('
      let depth = 0
      for (let i = from; i < code.length; i++) {
        if (code[i] === '(') depth++
        else if (code[i] === ')') {
          depth--
          if (depth === 0) return code.slice(from + 1, i)
        }
      }
      assert.fail(`★ 阳性对照失败：${callee}( 的括号没配平`)
    }
    for (const [name, src] of [['PkmNoteView', PKM_NOTE_VIEW], ['PkmEditor', PKM_EDITOR]] as const) {
      const args = callArgs(strip(src), 'getNote')
      assert.match(args, /currentWorkspaceId\(\)/,
        `${name} 的 getNote 实参是 (${args}) ⇒ 没传 workspaceId，真机会读不到（退回 'default'）`)
    }
  })

  it('★★ 反向断言：contacts 的 getContact 同型，但当前没有软删入口（阴性结论登记）', () => {
    // contacts 的 getContact 同样不过滤墓碑，**但 kind='contact' 目前没有任何
    // 软删写入点** ⇒ 读出墓碑不可达，本节不改它（改了也只是纯防御，
    // 还会引入 workspace 回归风险）。
    // 这条钉的是那个**前提**：一旦有人给 contact 接上删除，本条会红，
    // 提醒必须同时把读路径对齐（与 PKM 一样）。
    const softDeleteCallers = countMatches(
      ASSET_STORE + CONTACTS_STORE,
      /softDelete(?:ForWorkspace)?\(/g,
    )
    assert.equal(softDeleteCallers, 2,
      'local_assets 的软删入口不再是 2 个（softDelete + softDeleteForWorkspace 的定义）：' +
      '若新增了针对 contact 的删除入口，必须同时让 contacts-store.getContact 过滤 deleted_at')
  })
})

/** 数一处源码里某个模式的匹配次数（按 §880 的教训，计数类读数要逐条钉）。 */
function countMatches(src: string, re: RegExp): number {
  return (src.match(re) || []).length
}
