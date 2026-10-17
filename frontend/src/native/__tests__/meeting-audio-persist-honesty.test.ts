// meeting-audio-persist-honesty.test.ts — §820：落盘失败时**不许写假的 audioPath**。
//
// ⚠ 为什么这条重要（修之前的实际行为）：
//   `recordingRuntime.stop()` 里 `saveMeetingAudio` 失败被一句「ok」吞掉，
//   而随后 `updateMeeting({ audioPath, status: 'completed' })` **照样**把
//   `URL.createObjectURL(fullBlob)` 写进会议记录。
//   而 blob URL 只活在**当前文档的内存**里（本模块头注释第一句就在说这件事）
//   ⇒ 用户看到一个「完好」的会议 + 能播的音频，刷新/重启后音频彻底消失；
//   `loadMeetingAudio` 随后返回 null ⇒ 整段高精度重转永久不可用，只剩分段文本。
//   ⇒ **数据丢失，且全程零提示。**
//
// 决策放在纯函数 `resolvePersistedAudio` 里而不是类方法体，
// 因为 `MeetingRecorderRuntime.stop()` 在 node 里跑不到（§52）。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { resolvePersistedAudio } from '../meeting-audio.ts'

const HERE = dirname(fileURLToPath(import.meta.url))

describe('resolvePersistedAudio —— 落盘结果怎么反映到会议记录（§820）', () => {
  it('★ 落盘失败 ⇒ audioPath 必须是 null（不许写刷新后就死的 objectURL）', () => {
    const d = resolvePersistedAudio({
      objectUrl: 'blob:http://x/1234',
      persisted: false,
      error: new Error('QuotaExceededError'),
    })
    assert.equal(d.audioPath, null,
      '落盘失败却把 objectURL 写进记录 ⇒ 等于谎报「录音还在」，刷新后音频消失且无人知情')
  })

  it('★ 落盘失败 ⇒ 必须出声，且文案要说清后果与成因', () => {
    const d = resolvePersistedAudio({
      objectUrl: 'blob:http://x/1234',
      persisted: false,
      error: new Error('QuotaExceededError'),
    })
    assert.ok(d.warning, '落盘失败却没有告警')
    assert.match(String(d.warning), /QuotaExceededError/, '原始异常没进去，无法定位')
    assert.match(String(d.warning), /IndexedDB/, '没说清是什么没存进去')
    assert.match(String(d.warning), /刷新|重启/, '没说清后果 —— 排查的人不知道这事有多严重')
  })

  it('★ 落盘成功 ⇒ audioPath 原样保留，且一声不吭（不制造噪声）', () => {
    const d = resolvePersistedAudio({ objectUrl: 'blob:http://x/1234', persisted: true })
    assert.equal(d.audioPath, 'blob:http://x/1234')
    assert.equal(d.warning, null, '成功路径不该产生告警')
  })

  it('★ 落盘失败但没有异常对象时也必须出声（不能因为缺字段就沉默）', () => {
    const d = resolvePersistedAudio({ objectUrl: 'blob:x', persisted: false })
    assert.ok(d.warning, '缺 error 字段就沉默 ⇒ 判据把失败又藏回去了')
    assert.match(String(d.warning), /未知原因/)
  })

  it('负控：落盘失败时返回的 audioPath 不得回退成 objectURL 本身', () => {
    const url = 'blob:http://x/9999'
    const d = resolvePersistedAudio({ objectUrl: url, persisted: false })
    assert.notEqual(d.audioPath, url)
  })

  it('★ 变异防线：把失败分支的 null 改回 objectUrl 必须被抓', () => {
    // 这条钉住「修掉 bug」的那一行本身，否则改回去没人拦
    const d = resolvePersistedAudio({ objectUrl: 'blob:x', persisted: false, error: new Error('E') })
    assert.equal(d.audioPath, null)
  })

  it('★ 接线：`stop()` 必须真的用这个决策的结果写库（不是各写各的）', () => {
    // ⚠ `MeetingRecorderRuntime.stop()` 依赖 localDB + HTTP，node 里跑不到，
    //   所以接线只能源码扫描。这条只保证「决策没被绕过」，
    //   决策本身的正确性由上面那几条行为用例负责（§818 的分层判据）。
    const src = readFileSync(join(HERE, '..', 'recordingRuntime.ts'), 'utf8')
    assert.match(src, /resolvePersistedAudio\(/, 'stop() 没走这个纯函数 ⇒ 决策被绕过了')
    assert.match(src, /audioPath:\s*audioDecision\.audioPath/,
      '写库时没有用决策结果 —— 又在写自己的 audioPath 口径')
    // ⛔ 关键防线：绝不能再出现「catch 里吞掉 + 照样写 objectURL」的组合
    const stopIdx = src.indexOf('saveMeetingAudio(meetingId, fullBlob)')
    assert.ok(stopIdx > 0, 'saveMeetingAudio 调用点不见了')
    const after = src.slice(stopIdx, stopIdx + 400)
    assert.match(after, /audioPersisted\s*=\s*true/,
      '保存成功没有标记 persisted ⇒ 决策永远以为失败（或反过来）')
    assert.doesNotMatch(after, /catch\s*\{\s*\/\*\s*ok\s*\*\/\s*\}/,
      '静默 catch 又回来了')
  })
})

// ─────────────────────────────────────────────────────────────────────────
// §824：切换设备会**丢掉切换前的音频**（只丢音频、不丢文字，且完全静默）。
describe('§824 切换设备不得静默丢掉切换前的音频', () => {
  const src = readFileSync(join(HERE, '..', 'recordingRuntime.ts'), 'utf8')
  const sw = src.slice(src.indexOf('async switchDevice('), src.indexOf('private startLiveCaption'))

  it('★ switchDevice 必须接住那次 stop() 的返回值（原来被直接丢弃）', () => {
    assert.match(
      sw,
      /const preSwitchBlob = await this\.vadSegmenter\?\.stop\(\)/,
      '切换前那次 stop() 的返回值没有被接住 ⇒ 它的音频就此丢失',
    )
    assert.ok(
      !/await this\.vadSegmenter\?\.stop\(\)\s*\n\s*await this\.cleanupMedia/.test(sw),
      '仍然是「await 后直接丢弃」的旧写法',
    )
  })

  it('★ 接住之后必须出声（否则「丢音频」仍然是无声的）', () => {
    const i = sw.indexOf('preSwitchBlob')
    const after = sw.slice(i, i + 900)
    assert.match(after, /console\.warn/, '接住了 blob 却没有任何提示')
    assert.match(after, /不会进入本次保存的完整录音/, '告警没说清后果')
    assert.match(after, /分段文本不受影响/, '告警没说清什么没丢，排查时要靠猜')
  })

  it('★ 告警只在「真的有音频会丢」时触发（空 blob 或零时长不该响）', () => {
    const after = sw.slice(sw.indexOf('preSwitchBlob'), sw.indexOf('preSwitchBlob') + 900)
    assert.match(after, /preSwitchBlob\.size > 0 && keptElapsed > 0/,
      '触发条件缺失 ⇒ 每次切设备都会喊，训练人忽略告警')
  })

  it('★ 全仓只有一处 saveMeetingAudio，且它在 stop() 里拿当前 segmenter 的 blob', () => {
    // 这是「为什么丢音频无处可救」的根据：换段器之后没有第二个落盘点。
    const n = (src.match(/saveMeetingAudio\(/g) || []).length
    assert.equal(n, 1, `saveMeetingAudio 出现了 ${n} 处 —— 若新增了落盘点，本节的前提要重写`)
  })
})
