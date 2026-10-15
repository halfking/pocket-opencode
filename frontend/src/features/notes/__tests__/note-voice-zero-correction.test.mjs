// note-voice-zero-correction.test.mjs — §419：随手记语音草稿**零纠错**这条事实，
// 必须在它被修掉的那一刻响一次，好让人回来更新待拍板 #15。
//
// ── 为什么这不是「补一个功能」而是一道门 ──
//
// §150.2 已经把链逐环核过（不是推断）：同一段录音，
//   会议/会话路径：finalizeRecording → 全量重转 → 说话人归因 → 人名同音纠正
//                  → LLM 精校 → 入库
//   随手记路径：  useNoteRecording.create() 直接落 content = 原始 ASR 文本
// ⇒ 用户需求第一条是「转写不太准…录音完成后可能还需要一次精校」，
//   而**捕获门槛最低的那条路径产出质量最低**。
//
// §150 的结论是「随手记侧要纠错必须先有一个『名字从哪来』的产品决定」，明确**不代拍**。
// 但那条决定此前只躺在 5 万多行的设计文档里，**从未进入待拍板简报**
// ⇒ 已识别的产品决定缺一个「决策面」上的入口。本轮把它补进 #15，
//   并用这道门守住「它还是现在这样」这个前提。
//
// ⚠ 判据转红时的正确读法：**不是「代码坏了」**，而是
//   「随手记已经接上纠错了 ⇒ 简报 #15 该撤或改写」（同 check:refine-summary-meta-split）。
import assert from 'node:assert/strict'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..', '..', '..')
const NOTES = path.join(SRC, 'features', 'notes')
const MEETINGS = path.join(SRC, 'features', 'meetings')
const SESSIONS = path.join(SRC, 'features', 'sessions')

/** 判据自身文件名 —— 豁免**只有**这一个，且必需：本文件必然提到那些符号。 */
const SELF = 'note-voice-zero-correction.test.mjs'
/** 判据里要查的「精校链」符号。⚠ 判据文件里不留完整字面量，按需拼接。 */
const REFIN_CHAIN = [
  ['finalize', 'Recording'],
  'meetingsApi',
  ['build', 'Refine', 'Prompt'],
  ['.refine('],
].map((s) => (Array.isArray(s) ? s.join('') : s))

/** 递归读一个目录下的全部文本（跳过测试与非文本）。 */
function readTree(dir) {
  const out = new Map()
  const walk = (d) => {
    for (const name of readdirSync(d)) {
      try {
        if (name === 'node_modules' || name.startsWith('.')) continue
        const abs = path.join(d, name)
        if (statSync(abs).isDirectory()) { walk(abs); continue }
        if (name.endsWith('.test.mjs') || name.endsWith('.test.ts')) continue
        if (!/\.(ts|vue|mjs)$/.test(name)) continue
        out.set(path.relative(SRC, abs), readFileSync(abs, 'utf8'))
      } catch (err) {
        // §632：并发下别的用例会往 `frontend/src/` 下**临时建目录再删掉**
        // （z-index-ladder 的 `__probe_fixture__`）。递归撞上那个窗口会抛
        // ENOENT ⇒ **整条门假红**。只吞 ENOENT，其余照抛。
        if (err?.code !== 'ENOENT') throw err
      }
    }
  }
  walk(dir)
  return out
}

const notesTree = readTree(NOTES)

describe('§419 · 随手记语音草稿零纠错（待拍板 #15 的前提）', () => {
  it('前提自证：扫描面不是空的（空 ⇒ 下面几条会变成恒真）', () => {
    assert.ok(notesTree.size >= 20, `只扫到 ${notesTree.size} 个文件，路径可能不对`)
    // 对照组：会议/会话两条路径**确实**接了 finalizeRecording ——
    // 没有这一条，「随手记没接」可能只是「我找错地方了」。
    const meeting = readFileSync(path.join(MEETINGS, 'meeting-recording-finalize.ts'), 'utf8')
    assert.ok(meeting.includes('finalizeRecording'), '对照组失效：会议侧那个函数名变了')
    // ⚠⚠ 消费者这两处必须查**调用形状**（带左括号），不能查裸名字 ——
    //   第一版查的是裸名，于是 `import { finalizeRecording }` 这一行**单独就能满足它**，
    //   把调用整段删掉（import 留着）时断言照样为真。
    //   M4 变异实测：那一版门禁**没有红** ⇒ 这是量具自己的洞。
    //   这正是「参数存在≠分支活着」：import 存在 ≠ 有人调它。
    const callShape = /finalizeRecording\s*\(/
    for (const [what, file] of [
      ['会议详情页', path.join(MEETINGS, 'MeetingDetailView.vue')],
      ['会话录音', path.join(SESSIONS, 'useSessionLiveRecord.ts')],
    ]) {
      const src = readFileSync(file, 'utf8')
      assert.ok(callShape.test(src),
        `对照组失效：${what} 不再**调用** finalizeRecording（只留 import 也会通过 —— 那不算接上了）`)
    }
  })

  it('★ 随手记模块**零处**引用精校链（现状：没有纠错这一层）', () => {
    const hits = []
    for (const [rel, src] of notesTree) {
      if (path.basename(rel) === SELF) continue
      for (const sym of REFIN_CHAIN) {
        if (src.includes(sym)) hits.push(`${rel}  ← ${sym}`)
      }
    }
    assert.deepEqual(hits, [],
      '随手记开始接精校链了 ⇒ 待拍板简报 #15 的前提已变：\n  ' + hits.join('\n  '))
  })

  it('★ 语音草稿落的就是原始转写（未经任何加工）', () => {
    const src = readFileSync(path.join(NOTES, 'useNoteRecording.ts'), 'utf8')
    assert.match(src, /content:\s*text\s*\|\|\s*'（语音草稿）'/,
      '语音草稿的落库正文不再是原始转写 ⇒ 读法变了，请连带复核 #15')
    assert.match(src, /createdByVoice:\s*true/,
      'createdByVoice 标记不见了 ⇒ 这道门可能已经找错对象，先查清再改')
  })

  it('★ 豁免只有本文件一个（豁免表自己会烂成通行证）', () => {
    // 与 §416 那条教训同源：先写了一个并不需要的豁免，实测无用后删掉。
    // 这条把「豁免只有一个且必需」钉成事实。
    const exempt = [...notesTree.keys()].filter((rel) => rel.includes('__tests__'))
    assert.deepEqual(exempt, [],
      `测试文件没有在扫描面里（应被 readTree 直接跳过）：${exempt.join(', ')}`)
  })

  it('★★ 负控：把 finalizeRecording 接进随手记，本判据必须立刻翻脸', () => {
    const wired = readFileSync(path.join(NOTES, 'useNoteRecording.ts'), 'utf8')
      .replace("import { noteRecorderRuntime as rt, type VoiceDraftSink } from '../../native/recordingRuntime'",
        "import { noteRecorderRuntime as rt, type VoiceDraftSink } from '../../native/recordingRuntime'\nimport { finalizeRecording } from '../meetings/meeting-recording-finalize'")
    assert.ok(wired.includes('finalizeRecording'), '负控没造出「接上了」的形态')
    // 用与上面那条**完全相同**的判别式跑一遍，证明它不是恒真。
    const hits = []
    for (const [rel, original] of notesTree) {
      if (path.basename(rel) === SELF) continue
      const s = rel.endsWith('useNoteRecording.ts') ? wired : original
      for (const sym of REFIN_CHAIN) if (s.includes(sym)) hits.push(rel)
    }
    assert.ok(hits.length > 0, '负控失败：判别式对「接上了」的样本也放行')
  })
})