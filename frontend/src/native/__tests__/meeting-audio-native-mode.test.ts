// meeting-audio-native-mode.test.ts — §826：**原生后台录音下压根没有整段音频可存**。
//
// ── 缺陷链（每一步都在生产代码里可核对）──
//   1. `recordingRuntime.start()`：`isBackgroundMicSupported()` 在原生端恒为真
//      （= `Capacitor.isNativePlatform()`），`startBackgroundMic()` 成功 ⇒
//      `this.nativeMode = true`；而 `new VadSegmenter(...)` 在 `if (!this.nativeMode)`
//      里 ⇒ **native 模式下 VAD 段器根本不存在**。
//   2. `stop()`：`const fullBlob = (await this.vadSegmenter?.stop()) ?? null`
//      ⇒ native 模式下恒为 `null`。
//   3. `saveMeetingAudio` **全仓只有这一处调用** ⇒ 完整音频从不落盘。
//   4. 原生侧也补不上这个洞：`MeetingRecordService` 用 `AudioRecord` 直接推 PCM，
//      **没有任何** `FileOutputStream`／`MediaMuxer`／容器文件 ⇒ 它只发分片。
//
// ⇒ Android（生产主路径）上，`meeting.audioPath` 恒为 null、`loadMeetingAudio` 恒返回 null
// ⇒ **整段高精度重转永远不发生**，精校只能基于录音期间逐段落库的文本。
//
// ⭐ 而这件事此前**没有任何门或注释守着**：`background-mic-contract.test.mjs` 的 18 条
//   用例全部关于 start 的结论、失败可见性与 Android 14+ 合规，**零条**涉及整段音频。
//
// ⚠ 本文件要守的是**诚实性**，不是「把音频变出来」：
//   把已落盘的分片拼成整段音频是可行且不难的（分片是**完整 WAV**，PCM 拼接只涉及去头
//   与重写头，见 §826.3），但那会改存储形态 ⇒ **属产品口径，待属主拍板，本轮不代拍**。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { resolvePersistedAudio } from '../meeting-audio.ts'

const HERE = dirname(fileURLToPath(import.meta.url))
const read = (p: string) => readFileSync(join(HERE, p), 'utf8')

describe('§826 · 原生后台录音：没有整段音频 ≠ 落盘失败', () => {
  it('★ native 模式且没有异常 ⇒ 必须出声，并说清「不是存失败，是压根没有」', () => {
    const d = resolvePersistedAudio({
      objectUrl: '', persisted: false, error: null, nativeMode: true,
    })
    assert.ok(d.warning, '原生模式下没有整段音频，却一声不吭 —— 与 §820 修掉的是同一类沉默')
    const w = String(d.warning)
    assert.match(w, /原生/, '没说清这是原生后台录音这一特定成因')
    assert.match(w, /分片/, '没说出「只有分片」这个事实，排查的人会去查落盘/配额')
    assert.match(w, /整段/, '没说清整段重转不会发生 —— 这才是真正的后果')
  })

  it('★★ 成因不同的两件事必须给不同的文案（顺序不能反）', () => {
    // 落盘失败（IndexedDB 配额）⇒ 说的是「存不进去」
    const failed = resolvePersistedAudio({
      objectUrl: '', persisted: false, error: new Error('QuotaExceededError'), nativeMode: true,
    })
    // 原生模式压根没有整段音频 ⇒ 说的是「没有」
    const native = resolvePersistedAudio({
      objectUrl: '', persisted: false, error: null, nativeMode: true,
    })
    assert.notEqual(failed.warning, native.warning,
      '两种成因给了同一句文案 ⇒ 排查的人会去查错的地方（这是 §818「根因 vs 症状」那条纪律）')
    assert.match(String(failed.warning), /QuotaExceededError/, '落盘失败文案里应保留原始异常')
    assert.doesNotMatch(String(native.warning), /QuotaExceededError/,
      '原生模式不该被说成配额问题')
  })

  it('★★ 仍然不许写假的 audioPath（这一条 §820 已经定了，不能因为换成因就退让）', () => {
    const d = resolvePersistedAudio({
      objectUrl: '', persisted: false, error: null, nativeMode: true,
    })
    assert.equal(d.audioPath, null)
  })

  it('web 模式（nativeMode 缺省/false）不得被这条分支误伤', () => {
    const d = resolvePersistedAudio({ objectUrl: '', persisted: false, error: null })
    assert.doesNotMatch(String(d.warning), /原生/,
      '普通 web 录音被说成「原生后台录音」⇒ 诊断会误导排查')
  })

  it('⚠ 后更正（§826.2）：落盘失败文案不得再说「本次会话内仍可回放」', () => {
    // 前端**没有任何音频播放界面**（全仓无 <audio> / new Audio / 播放器组件），
    // audioPath 只被 API 序列化、笔记徽章与整段重转消费 ⇒ 「能播」不成立。
    const d = resolvePersistedAudio({
      objectUrl: '', persisted: false, error: new Error('QuotaExceededError'),
    })
    assert.doesNotMatch(String(d.warning), /仍可回放/,
      '文案仍在承诺「能回放」，而前端根本没有播放入口 —— 这正是 §826.2 更正的那句')
    assert.match(String(d.warning), /播放界面|精校质量降级/,
      '应说清真实后果：没有播放界面，用户看到的是精校质量降级')
  })
})

// ── 接线层：源码扫描（⚠ 按 §818 的教训对「信号变死代码」零牙，只保证证据没被搬走）──
describe('§826 · 接线（扫描层，零牙已如实标注）', () => {
  const rt = read('../../native/recordingRuntime.ts')
  const svc = read('../../../android/app/src/main/java/com/kaixuan/opencode/pocket/plugins/MeetingRecordService.java')

  // ⚠ 判据必须**限定在 stop() 的函数体内**：全文件 indexOf 会先命中
  //   switchDevice() 里那次 await this.cleanupMedia()，于是「捕获早于清理」这条
  //   断言在**正确的代码上也变红** —— 第一版就是这么写的，当场自曝。
  //   判据自己抓自己，比没有判据更费时间。
  //
  // ⚠⚠ 也**没用** balancedSpan 取函数体（第二版试过）：它的 `startCh` 分支靠
  //   「锚点后面紧跟一个 `(`」来先配平参数表，而本函数签名里的
  //   `Promise<{ audioPath: string; ... }>` 自带一对花括号 ⇒
  //   `indexOf('{', at)` 命中的是**返回类型里那对**，inner 只有 64 字符。
  //   ⇒ 改用「从函数锚点起的有界切片」：范围显式、行为一眼可查。
  const sliceFrom = (anchor: string, len = 12_000) => {
    const at = rt.indexOf(anchor)
    assert.ok(at > 0, `锚点没找到：${anchor}`)
    return rt.slice(at, at + len)
  }
  const stopSrc = sliceFrom('async stop(): Promise<{ audioPath: string; durationMs: number; audioStartsAtMs: number }')
  const startSrc = sliceFrom('async start(meetingId: string, opts?: { deviceId?: string; resume?: boolean })')

  it('★ nativeMode 必须在 cleanupMedia() 之前取值（否则恒为 false，诊断成死代码）', () => {
    const cap = stopSrc.indexOf('const wasNativeMode = this.nativeMode')
    const cleanup = stopSrc.indexOf('await this.cleanupMedia()')
    assert.ok(cap > 0, 'stop() 里没有提前捕获 nativeMode')
    assert.ok(cleanup > 0, 'stop() 里找不到 cleanupMedia 调用')
    assert.ok(cap < cleanup,
      '捕获发生在 cleanupMedia 之后 ⇒ nativeMode 已被置回 false ⇒ 这条诊断永远不触发且不报错')
  })

  it('★ cleanupMedia 必须真的会把 nativeMode 清掉（上面那条的前提）', () => {
    const cm = sliceFrom('private async cleanupMedia()', 2_000)
    assert.match(cm, /this\.nativeMode = false/,
      'cleanupMedia 不再清 nativeMode ⇒ 「必须提前取值」这条前提变了，判据要重写')
  })

  it('★ 捕获到的值必须真的传进决策', () => {
    assert.match(rt, /nativeMode: wasNativeMode/,
      '决策拿的是 this.nativeMode 而不是提前捕获的值 ⇒ 同上，永远是 false')
  })

  it('native 模式下 VAD 段器不存在 —— 这是「没有整段音频」的根据', () => {
    const inStart = startSrc.indexOf('new VadSegmenter(')
    const guard = startSrc.indexOf('if (!this.nativeMode)')
    assert.ok(inStart > 0, 'start() 里找不到 VadSegmenter 的创建点')
    assert.ok(guard > 0 && guard < inStart,
      'VAD 段器的创建不再受 nativeMode 约束 —— 本节的前提（没有整段音频）需要重新取证')
  })

  it('原生侧确实不写整段文件（AudioRecord 直推 PCM，没有容器封装）', () => {
    assert.doesNotMatch(svc, /MediaMuxer|FileOutputStream|MediaRecorder\(/,
      '原生侧出现了容器写入 ⇒ 说明存在「整段音频其实有、只是没人取」的可能，本节结论要重写')
  })

  it('⚠ 前提登记：整段音频的落盘点全仓只有一个（丢了就没别的出口）', () => {
    const n = (rt.match(/saveMeetingAudio\(/g) || []).length
    assert.equal(n, 1, `saveMeetingAudio 出现了 ${n} 处 —— 若新增了落盘点，本节的前提要重写`)
  })
})
