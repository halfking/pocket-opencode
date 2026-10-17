// meeting-finalize-persist-notice.test.ts — §834：会议收尾**写库失败**不得无声，
// 且「状态卡在 recording」**不得**自动开麦。
//
// ── D1：`updateMeeting(id, { status: 'completed', … })` 是会议收尾里三处落库中
//   **唯一没有保护**的一处（`saveMeetingAudio` 有 try/catch + §820 落盘失败写 null；
//   `syncMeetingMetadata` 被 `runBestEffort` 包着）。抛错的连锁后果：
//     · 异常冒泡出 stop() ⇒ RecordingPill 的「会议录音已结束并保存」**不执行**；
//     · `activeMeetingId = ''` 在它之后 ⇒ runtime 仍认为在录；
//     · `status: 'completed'` 全仓只写这一处 ⇒ 会议永久停在 `recording`。
//
// ── D2：同一个 `recording` 状态，仓库里有**两套相反契约** ——
//   meetings-live 的注释与 useSessionLiveRecord 的行为都写明「提示恢复，
//   **不自动重开采集**」，而 MeetingDetailView 却是 `|| m.status === 'recording'`
//   ⇒ 一打开就自动开麦，麦克风弹窗 + 把新录音混进同一场会议。
//
// ⚠ 本文件里所有「反向断言」都用**行首锚**（`^\s*…`）而不是子串匹配：
//   本会话已被「注释里引述的代码字面量被扫描当真代码」骗过 5 次（§830 是第 4 次），
//   而本节的修复注释里**必然**要引述旧代码形态 ⇒ 不锚行首就必然自曝。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import {
  describeMeetingFinalizePersistFailure,
  LEFTOVER_RECORDING_HINT,
  MEETING_FINALIZE_PERSIST_FAIL_TAG,
} from '../meeting-finalize-persist-notice.ts'

const HERE = dirname(fileURLToPath(import.meta.url))
const read = (rel: string) => readFileSync(join(HERE, rel), 'utf8')
const RUNTIME = read('../../../native/recordingRuntime.ts')
const PILL = read('../../../components/RecordingPill.vue')
const DETAIL = read('../MeetingDetailView.vue')
const SESSION = read('../../sessions/useSessionLiveRecord.ts')

/**
 * 只保留**代码行**：剔除整行注释与块注释的星号行。
 *
 * ⚠ 这是本文件所有扫描断言的地基，也是本会话反复踩的那个坑的直接对策：
 *   「注释里引述的代码字面量会被扫描当真代码」。本节的修复注释**必然**要引述
 *   旧写法（「没有 try/catch」、旧的 auto 判定），于是第一版判据在自己的注释上
 *   报红（实测：把 catch 的「这一处没有 try 包住」判成了「try 与字段之间出现了 catch」）。
 *   ⇒ 一律先剥注释再判。行首形如 // 、* 、/* 的行视为注释。
 *
 * ⚠ 边界：这是**行级**启发式，只挡整行注释。行尾注释里引述的标识符仍可能被命中
 *   （本轮未踩到，因为相关代码都以整块注释书写）。要彻底覆盖得写真正的词法器，
 *   成本与收益不相称 ⇒ 据实标注，不假装它是完整的。
 */
const codeOnly = (text: string): string =>
  text.split('\n').filter((l) => {
    const t = l.trim()
    return !(t.startsWith('//') || t.startsWith('*') || t.startsWith('/*'))
  }).join('\n')

describe('§834 D1 · 文案层（行为，纯函数）', () => {
  const w = describeMeetingFinalizePersistFailure({ error: new Error('SQLITE_FULL') })

  it('★★ 必须说清没存下来的是「已结束」这个状态，而不是音频或文字', () => {
    assert.match(w, /状态|已结束/, '没说清失败的是状态写入')
    assert.doesNotMatch(w, /录音.*(丢|没了|不见)/,
      '文案把后果说成录音丢失 ⇒ 用户会以为白录了（音频与分段都还在）')
  })

  it('★★★ 必须说清内容仍然在本地（否则用户会去重录已有内容）', () => {
    assert.match(w, /还在本地|仍在本地|都还在/, '没告诉用户内容还在 ⇒ 会导致无谓重录')
  })

  it('★★ 必须给可执行动作（这是唯一能自救的一步）', () => {
    assert.match(w, /录音中|手动结束|重开/, '只报错不给路')
  })

  it('负控：任意异常形态都不得产出 undefined / [object Object] / NaN', () => {
    for (const e of [null, undefined, 42, {}, { message: 'boom' }]) {
      const t = describeMeetingFinalizePersistFailure({ error: e })
      assert.doesNotMatch(t, /undefined|\[object Object\]|NaN/, `异常形态 ${String(e)} 产出了假读数：${t}`)
    }
  })

  it('★ 诊断前缀必须与 §820 的落盘失败前缀区分得开', () => {
    assert.match(MEETING_FINALIZE_PERSIST_FAIL_TAG, /收尾写库失败|状态没写成 completed/, '前缀没点明是收尾状态写入')
    assert.doesNotMatch(MEETING_FINALIZE_PERSIST_FAIL_TAG, /音频落盘失败/, '与 §820 的落盘失败前缀撞了')
  })
})

describe('§834 D2 · 遗留录音提示（行为，纯函数）', () => {
  it('★★★ 必须逐字保住仓库既有那句承诺「不会自动重开采集」', () => {
    // 这句话是 useSessionLiveRecord 原有的用户可见文案；把它钉成常量后，
    // 「不自动重开采集」这条契约就有了单一来源。
    assert.equal(
      LEFTOVER_RECORDING_HINT,
      '上次录音未正常结束，点麦克风可开始新的实时记录（不会自动重开采集）',
      '共享常量与原句不再逐字一致 ⇒ 契约的措辞被悄悄改掉了',
    )
  })

  it('★★ 必须说清「不自动」这件事，且不得暗示会自动开始', () => {
    assert.match(LEFTOVER_RECORDING_HINT, /不会自动重开采集/, '承诺丢失')
    assert.doesNotMatch(LEFTOVER_RECORDING_HINT, /将自动|会自动开始/, '文案暗示会自动开麦')
  })

  it('★ 必须点明这是「上次没正常结束」（否则用户以为是新会议）', () => {
    assert.match(LEFTOVER_RECORDING_HINT, /上次录音未正常结束/, '没说清是上一次遗留的')
  })
})

// ── D1 接线（源码扫描，⚠ 对「信号变死代码」零牙，只保证真的接上了）──
describe('§834 D1 · 收尾写库（扫描层，零牙已如实标注）', () => {
  /** stop() 收尾里写 completed 那一段（已剥注释）。 */
  const tailWindow = (): string => {
    const from = RUNTIME.indexOf('async stop(): Promise<{ audioPath: string; durationMs: number;')
    assert.ok(from > 0, '★ 阳性对照失败：找不到会议 stop 的签名，此刻本门是假阴性')
    const to = RUNTIME.indexOf('voicePrompt().announce(\'stop\')', from)
    assert.ok(to > from, '★ 阳性对照失败：找不到收尾窗口的终点')
    return codeOnly(RUNTIME.slice(from, to))
  }

  it('★★★ 写 completed 的那次 updateMeeting 必须被 try 包住，且失败要出声', () => {
    const w = tailWindow()
    const at = w.indexOf('status: \'completed\'')
    assert.ok(at > 0, '★ 阳性对照失败：收尾窗口里找不到 status: completed')
    // ⚠ 窗口宽度不是随手取的：`try {` 与 `status: 'completed'` 之间夹着 §820 那四行
    //   中文注释（实测 246 字符）。第一版取 400 字符仍漏掉了 await 那一行 ⇒ 判据自己翻车。
    //   ⇒ 改成「以最后一次出现的 `try {` 为界，检查它与该字段之间没有 catch」。
    const head = w.slice(Math.max(0, at - 900), at)
    const tryAt = head.lastIndexOf('try {')
    assert.ok(tryAt >= 0, '写 completed 的 updateMeeting 仍未被 try 包住')
    assert.doesNotMatch(head.slice(tryAt), /catch/,
      'try 与 status: completed 之间出现了 catch ⇒ 写库根本不在这个 try 里')
    assert.match(head.slice(tryAt), /await updateMeeting\(meetingId, \{/, '锚点不对：本节判据已与真实写法脱节')
  })

  it('★★ 失败必须同时诊断 + 对用户说话（原缺陷是完全没保护）', () => {
    const w = tailWindow()
    assert.match(w, /console\.error\(MEETING_FINALIZE_PERSIST_FAIL_TAG, e\)/, '写库失败没有进日志')
    assert.match(w, /this\.toast\.error\(describeMeetingFinalizePersistFailure\(/,
      '写库失败没有对用户说话 ⇒ 用户面对「点了完全没反应」')
  })

  it('★★★ 失败后收尾仍必须做完：activeMeetingId 清零不能被跳过', () => {
    // 这正是原缺陷的连锁点：updateMeeting 抛错 ⇒ 清零与 return 全被跳过。
    // ⚠ 判据锚在「清零点必须排在这个 catch **之后**」，而不是「排在那句
    //   `= true` 之后」—— 后者会被「在 try 内提前清零」骗过（变异实测 0 红）。
    const w = tailWindow()
    const at = w.indexOf("status: 'completed'")
    assert.ok(at > 0, '★ 阳性对照失败：收尾窗口里找不到 status: completed')
    const tryAt = w.lastIndexOf('try {', at)
    assert.ok(tryAt >= 0, '写库那处没有 try')
    const catchAt = w.indexOf('} catch', tryAt)
    assert.ok(catchAt > tryAt, '★ 阳性对照失败：找不到写库那处的 catch')
    const clearedAt = w.indexOf("this.activeMeetingId.value = ''")
    assert.ok(clearedAt > 0, '★ 阳性对照失败：找不到 activeMeetingId 清零点')
    assert.ok(clearedAt > catchAt,
      'activeMeetingId 的清零仍排在 try 内部 ⇒ 写库失败会跳过它（runtime 继续认为在录）')
  })

  it('★★ 结论必须外传给调用方（否则 pill 无从说真话）', () => {
    const w = tailWindow()
    assert.match(w, /lastFinalizePersistFailed = true/, '失败时没有把结论记下来')
    // ⚠ 通道是**公开字段**而不是返回值里的一个字段：§825/§826 两道既有门
    //   把 stop() 的签名与返回对象逐字钉住，加字段会把它们一起打红
    //   （本轮实测：正是这么撞上的）。⇒ 这里同时钉住「字段是公开的」。
    assert.match(RUNTIME, /^\s{2}lastFinalizePersistFailed = false/m,
      '收尾结论不是公开字段 ⇒ 外部（pill）读不到')
    assert.match(RUNTIME, /async stop\(\): Promise<\{ audioPath: string; durationMs: number; audioStartsAtMs: number \} \| null>/,
      'stop() 的签名被改动 ⇒ 与 §825/§826 两道既有门冲突（它们逐字钉住了这一行）')
  })

  it('★★ 每次 stop 必须先清零（否则上一轮的失败会挂到这一轮的 toast 上）', () => {
    const w = tailWindow()
    const startAt = w.indexOf('this.stopping = true')
    const clearAt = w.indexOf('this.lastFinalizePersistFailed = false')
    assert.ok(startAt > 0 && clearAt > startAt,
      '清零点不在 stop() 开头 ⇒ 无会议归属的分支会把上一轮的结论带出去')
  })

  it('★★ pill 不得再无条件宣告「已结束并保存」', () => {
    // ⚠ 「无条件」不是「这一行不存在」——它现在还**存在**，只是被挪进了 if 里。
    //   （第一版写成 doesNotMatch(那一行存在) ⇒ 修复本身让判据变红，属于断言写错。）
    //   ⇒ 真正要拦的是「stop() 之后紧接着就播成功」那种写法。
    assert.doesNotMatch(codeOnly(PILL),
      /await meetingRecorderRuntime\.stop\(\)\s*\n\s*toast\.success\(/,
      '成功提示仍是无条件 toast ⇒ 写库失败时它在撒谎')
    const pillCode = codeOnly(PILL)
    const at = pillCode.indexOf('await meetingRecorderRuntime.stop()')
    assert.ok(at > 0, '★ 阳性对照失败：pill 不再 await 会议 stop')
    assert.match(pillCode.slice(at, at + 400), /if \(!meetingRecorderRuntime\.lastFinalizePersistFailed\)/,
      'pill 没有据收尾结论决定说不说「已保存」')
    assert.match(pillCode.slice(at, at + 400), /toast\.success\('会议录音已结束并保存'\)/,
      '★ 阳性对照失败：条件分支里的成功提示不见了')
  })

  it('★ pill 的停止入口必须有 try/catch（UI 边界要自己说真话）', () => {
    const pillCode2 = codeOnly(PILL)
    const at = pillCode2.indexOf('async function onStop()')
    assert.ok(at > 0, '★ 阳性对照失败：找不到 onStop')
    assert.match(pillCode2.slice(at, at + 1600), /try \{/, 'onStop 没有 try ⇒ stop() 抛错时零反馈')
    assert.match(pillCode2.slice(at, at + 1600), /catch \(e\)/, 'onStop 没有 catch')
  })
})

// ── D2 接线（源码扫描）──
describe('§834 D2 · 不得自动开麦（扫描层）', () => {
  it('★★★ 详情页的自动开麦条件只能是显式的 record=1', () => {
    assert.match(codeOnly(DETAIL), /^\s*const auto = route\.query\.record === '1'\s*$/m,
      'auto 的判定已不是「只看显式 record=1」⇒ 又有一条路径会静默开麦')
    assert.doesNotMatch(codeOnly(DETAIL), /^\s*const auto = [^\n]*m\.status === 'recording'/m,
      'status=recording 又被算作自动开麦条件 ⇒ 重开卡住的会议会再次静默开麦')
  })

  it('★★★ 遗留状态必须提示、且逐字复用共享常量（两处不再各说各话）', () => {
    assert.match(codeOnly(DETAIL), /else if \(m\.status === 'recording'\) \{/, '遗留状态没有单独分支')
    assert.match(codeOnly(DETAIL), /toast\.warning\(LEFTOVER_RECORDING_HINT\)/, '遗留状态没有提示，或没走共享常量')
    assert.match(codeOnly(SESSION), /resumeHint\.value = LEFTOVER_RECORDING_HINT/, '会话侧不再复用共享常量')
    assert.doesNotMatch(codeOnly(SESSION), /resumeHint\.value = '上次录音未正常结束/,
      '会话侧又把提示语内联回一个字面量 ⇒ 两处会各自漂移')
  })

  it('⚠ 前提登记：三个「开始录音」入口都带 record=1（删掉自动分支不会破坏合法启动）', () => {
    for (const rel of ['../MeetingListView.vue', '../MeetingRecordView.vue']) {
      assert.match(read(rel), /record: '1'/, `${rel} 不再带 record=1 ⇒ 删掉自动分支会破坏启动入口`)
    }
    assert.match(read('../../../app/router-mobile.ts'), /record: '1'/, 'router-mobile 不再补 record=1')
  })
})
