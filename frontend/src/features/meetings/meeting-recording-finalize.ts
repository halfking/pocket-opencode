/**
 * meeting-recording-finalize.ts — **录音收尾的单一实现**（两条录音路径共用）。
 *
 * ── 为什么要抽出来（§49）──
 * 产品里有**两条**录音路径，它们的收尾本该是同一件事：
 *   ① 随手记会话内录音  `useSessionLiveRecord.stop()` → `meetingsApi.refine(...)`
 *   ② 会议详情页录音    `MeetingDetailView.onMicToggle()` → `useMeetingRecorder.stop()`
 * 而 ② 这条是**主入口**：会议列表的「开始录音」push 到
 *   `{name:'meeting-detail', query:{record:'1'}}`，详情页 onMounted 自动开录。
 *
 * 实测（§48.8/§49）：② 路径**全程没有任何 refine 调用点** ⇒ 录完只有原始分段转写，
 * 没有精校、没有行动项、没有日程提醒。两条路径的产出质量差一个量级。
 * ⇒ 收尾编排必须只有一份，否则「修好了一条、另一条照旧」会不停发生
 * （§48 的 A/B/C 三条全是这个形状）。
 *
 * ── 调用方必须保证的两件事 ──
 * 1. **segments 必须来自持久层**（`getMeetingWithSegments`），不能来自
 *    `useMeetingRecorder` 的 `segments` computed —— 它按 `activeMeetingId` 圈定，
 *    而 `rt.stop()` 在 return 前把该 id 置空 ⇒ stop 之后读到的恒为 `[]`。
 *    后果不是少几段，而是整段归因失效（见 §49 与下面的 buildRefineBaseSegments）。
 * 2. **elapsedMs 必须来自 `rt.stop()` 的返回值**，同理不能用 recorder.elapsedMs（恒 0）。
 *
 * 这两条都由调用方保证，本模块只做纯编排 + 三处外部调用，便于单测。
 */
import { sttSettingsApi } from '../../api/stt-settings'
import { Capacitor } from '@capacitor/core'
import { isAbortError } from '../../api/http'
import { meetingsApi } from '../../api/meetings'
import { planRefine } from './meeting-refine-plan'
import {
  buildRefineBaseSegments,
  refetchFullTranscript,
  shouldAdoptFullTranscript,
  type AudioChunkSource,
  type FullSegmentReceipt,
  type FullTranscribeOutcome,
} from './meeting-final-transcript'
import { streamNativeMeetingAudioChunks } from './audio-parts'
import { deriveRoster, repairRosterHomophones } from './meeting-roster-homophone'
import { PINYIN_COMMON } from './pinyin-common'
import { toGatewayWavBytes } from '../../utils/wav-encode'
import { getMeeting, updateMeeting, type MeetingSegment } from './meetings-store'
import { ingestMeetingArtifacts } from './meeting-ingest'
import { buildIngestNotice } from './meeting-ingest-notice'
import { buildRefineNotice, refineStatusFor } from '../sessions/refine-outcome-notice'
import { assessDurationDrift, createMeasuringToWav, finalizeFailureNotice, describeDurationGuardSource, resolveDurationGuardInput, type NativeCaptureSnapshot } from './recording-duration-consistency'

export interface FinalizeDeps {
  /** 一次全量重转。注入而非直接调 API，便于单测与换实现。 */
  transcribeFull?: (blob: Blob, filename: string) => Promise<FullTranscribeOutcome>
  /**
   * §954：**分块音频源**（原生链用）。
   *
   * ⚠ 不提供它 ⇒ 退回「取整场音频」那条路（`loadMeetingAudio`）
   *   ⇒ 而原生链上它**恒返回 null**（§826）⇒ 整段重转**永不发生**。
   * ⚠ 默认值 = `streamNativeMeetingAudioChunks`（原生平台才有意义）⇒
   *   **不需要每个调用方各自接线**（§49 的教训：两条录音路径必须共用一份收尾）。
   */
  loadAudioChunks?: (meetingId: string) => AsyncIterable<AudioChunkSource> | null
  /**
   * §955：**原生采集窗口**的取值函数（原生链才有）。
   *
   * ⚠⚠ 为什么原生链需要**另一个**墙钟：护栏比的是「墙钟 vs 真实音频」⇒
   *   **两个数必须同源**，否则差的是**系统偏移**而不是「音频丢了多少」。
   *   原生链上 JS 墙钟的起点（`start()`）比原生服务真正开采的时刻**晚**
   *   （`BackgroundMicPlugin.start` 等的是服务回报 `reportStart(true)`）
   *   ⇒ 两者**不同相位** ⇒ 拿 JS 墙钟比 ⇒ 差一个**未实测的启动延迟**
   *   ⇒ 短录音（容差只有 500 ms）几乎必然越界 ⇒ **假警报** ⇒ 噪声。
   *   ⇒ 而原生侧每片带来的 `endMs` 与盘上音频**同源** ⇒ 用它。
   *
   * ⚠ 传**取值函数**而不是值：原生服务停止后还会**补发尾片**，窗口会继续增长
   *   ⇒ 取值必须发生在「音频量完之后」⇒ 由收尾链在那一刻调它。
   *
   * ⚠ 缺省（Web / iOS 链）⇒ 护栏用 JS 墙钟 ⇒ **行为一字未变**。
   */
  nativeCaptureSnapshot?: () => NativeCaptureSnapshot | null
  /**
   * 落盘音频 → 可切分的 WAV。注入便于单测。
   *
   * ⚠ 不提供它，整条「整段重转」链路在真机上是**死的**（§50）：
   *   录音产物是 webm/opus（`VadSegmenter.stop()` 产 `audio/webm`），
   *   而小米 MiMo ASR 桥接只收 wav/mp3（上游实测 400
   *   `input_audio.format must be one of: wav, mp3`）⇒ 整段重转直接失败。
   *   就算网关收下，`stt.SplitWAV` 对非 PCM-WAV **不可切**
   *   （实测 webm → `splittable=false`，0 段）⇒ 只回 1 条回执 ⇒
   *   `attributeFullTranscript` 返回 null ⇒ §40 的说话人归因整块不生效。
   *   ★ 随手记语音那条路径早就接了（`recordingRuntime` 用
   *     `audioDecoder.takeFull()`），**只有会议录音这条路没接**。
   */
  toWav?: (blob: Blob) => Promise<ArrayBuffer | null>
  /** 提示出口。注入以便单测断言「说了什么话」。 */
  notify: (kind: 'info' | 'success' | 'warning', text: string) => void
}

export interface FinalizeOutcome {
  /** 精校请求是否走完（不含入库；入库失败仍算走完） */
  refined: boolean
  /** 用户主动取消 */
  aborted: boolean
  /** 是否采纳了全量重转文本 */
  adopted: boolean
  /** 实际建出的待办数 */
  todosCreated: number
  /** §66.7：尝试建提醒的条数 / 真正建成的条数 / 期限没听清的条数。 */
  reminderPlanned: number
  reminders: number
  dueUnresolved: number
  /** §56：由 `next_meeting` 建出来的日程条数（0/1）。 */
  eventsCreated: number
  /**
   * §229.1：`next_meeting` 非空但解不出时刻（0/1）——
   * 用户说了下次会议的时间点，我们没听懂，日程没建。
   * 与上面的 `eventsCreated` **不是一回事**：它为 0 时至少有五种原因。
   */
  eventUnresolved: number
  /** §229.1：日程建失败（feed/create 抛异常）—— 我们没建成，不等于「没有下次会议」。 */
  eventFailed: boolean
  /** 入库（笔记/待办/提醒）是否失败 */
  ingestFailed: boolean
}

const defaultTranscribeFull = (blob: Blob, filename: string) =>
  // ★ segments 必须一起带回来：它是把高精度文本挂回说话人的唯一依据。
  //   只回 {text, failed} 就等于在接缝处把它扔了
  //   （见 meeting-final-transcript.ts 的 FullSegmentReceipt 头注释）。
  // ★ error 也必须带回来（§927）：服务端「一次成功段都没有」时回
  //   `{ok:false, error:…}`（HTTP 仍是 200）⇒ text/failed 都是空
  //   ⇒ 丢掉 error 就分不清「整块纯静音」与「整块真失败」
  //   ⇒ 前者会被误报成「有 N 段没转出来」。
  sttSettingsApi.transcribeFull(blob, filename).then((r) => ({
    text: r.text, failed: r.failed, segments: r.segments, error: r.error,
  }))

/**
 * 落盘录音 → 16kHz 单声道 WAV。
 *
 * 与随手记语音路径（`recordingRuntime` 用 `audioDecoder.takeFull()`）**同义**，
 * 但不依赖 runtime 单例的生命周期：两条录音路径都能在 stop 之后任意时刻调用。
 * 解码失败由 `refetchFullTranscript` 兜住（它 try/catch 后退回原 blob），
 * 所以这里直接抛，不吞异常 —— 吞掉会让人以为转码成功了。
 */
const defaultToWav = (blob: Blob): Promise<ArrayBuffer | null> => toGatewayWavBytes(blob)

/**
 * §954：默认的**分块音频源** —— 只在**原生平台**上生效。
 *
 * ⚠ 非原生平台返回 null ⇒ 退回「取整场音频」那条路 ⇒ **Web / iOS 链的行为一个字没变**。
 * ⚠ 原生平台上它把**盘上已有的分片**流式接成块（不拼整段、不写新文件）
 *   ⇒ 整段重转在 Android 上**第一次真的发生**（§826 查实它此前永不发生）。
 */
const defaultLoadAudioChunks = (meetingId: string): AsyncIterable<AudioChunkSource> | null => {
  if (!Capacitor.isNativePlatform()) return null
  return streamNativeMeetingAudioChunks(meetingId)
}

/**
 * §955：取「原生采集窗口」的快照（**观测失败不得影响主流程**，§818）。
 *
 * ⚠ 取值失败时返回「**原生但不可信**」而不是「没有原生」：
 *   「没有原生」会让护栏退回 **JS 墙钟** ⇒ 那是**不同源**的钟 ⇒ 差的是系统偏移
 *   ⇒ 短录音会**假警报**；
 *   而「不可信」⇒ 护栏判 `no-ground-truth`（**量不到**）⇒ 宁可瞎也不误报
 *   （§803.4：宁可漏报，也不要训练人忽略报警）。
 */
function readNativeCaptureSnapshot(
  provider: FinalizeDeps['nativeCaptureSnapshot'],
): NativeCaptureSnapshot | null {
  if (!provider) return null
  try {
    return provider() ?? null
  } catch (e) {
    console.warn('[meeting-finalize] 取原生采集窗口失败，本场不做时长一致性判定：', e)
    return { used: true, windowMs: null }
  }
}

/**
 * 收尾：全量重转 → 说话人归因 → 人名同音纠正 → LLM 精校 → 入库（笔记/待办/提醒）。
 *
 * 顺序每一环都不能换：
 *  · 全量重转**必须在**精校之前 —— LLM 只能看到已经错了的字符，它并不知道音频里
 *    原本说的是什么，所以「润色」修不了同音字/专名/数字（2026-10-06 的关键修复）。
 *  · 说话人归因必须在精校之前 —— 服务端提示词第 6 条要求「保留每行开头的 [说话人] 标记」。
 *  · 人名同音纠正必须在精校之前 —— 摘要/行动项/纪要都从正确的人名派生，
 *    放之后只能修「已经污染过的产物」。
 *  · 精校必须在入库之前 —— 入库要消费 refine.todos（带 due 的行动项）。
 */
export async function finalizeRecording(
  meetingId: string,
  segments: MeetingSegment[],
  elapsedMs: number,
  deps: FinalizeDeps,
  /**
   * ★§825：同一场录音的采集侧事实，来自 `rt.stop()` 的返回值。
   *
   * ⚠ 必须由调用方从 stop() 的返回值取（与 durationMs 同一条纪律，§49）：
   *   录音态的读数在 stop 之后不可信。
   */
  recording?: { audioStartsAtMs?: number },
): Promise<FinalizeOutcome> {
  const outcome: FinalizeOutcome = {
    refined: false, aborted: false, adopted: false,
    todosCreated: 0, reminderPlanned: 0, reminders: 0, dueUnresolved: 0,
    eventsCreated: 0, eventUnresolved: 0, eventFailed: false, ingestFailed: false,
  }
  const notify = deps.notify
  if (!meetingId) return outcome

  const segs = segments ?? []

  // ★ §803/§804：把「墙钟说有 62 秒」和「音频真的有 62 秒」放在同一处比一次。
  //   此前**没有任何一处**做这件事 —— 整条链路每个时长数字都是 `Date.now()` 的差，
  //   产物真实长度从未被测量 ⇒ §785 那 4.64 秒缺口运行时无从察觉。
  //   这里能比，是因为全量重转**本来就要**把 webm 解成 WAV（`toWav` 那一次 decode
  //   是无条件发生的），而 WAV 的字节数自带动长 ⇒ **零额外解码成本**。
  //   ⚠ 刻意不拿分段 `endMs` 对照 `elapsedMs`：两者同出墙钟，是恒等式（§803.2）。
  let measuredAudioSec: number | null = null
  const toWav: NonNullable<FinalizeDeps['toWav']> = createMeasuringToWav(
    deps.toWav ?? defaultToWav,
    (sec) => { measuredAudioSec = sec },
  )

  const finalText = await refetchFullTranscript(
    meetingId,
    deps.transcribeFull ?? defaultTranscribeFull,
    // ★★★ 不传这一项，整段重转在真机上是死的（§50）。
    //   录音产物是 webm/opus，网关只收 wav/mp3 ⇒ 重转直接失败；
    //   且 `stt.SplitWAV` 对 webm 不可切（实测 splittable=false）
    //   ⇒ 就算发得出去也只回 1 条回执 ⇒ 说话人归因整块不生效。
    //
    // ⚠ §954：`loadAudioChunks` 是**原生链**那条路 —— 那里压根没有「整场音频」可取
    //   （`loadMeetingAudio` 恒 null，§826）⇒ 不给它，整段重转在 Android 上永不发生。
    { toWav, loadAudioChunks: deps.loadAudioChunks ?? defaultLoadAudioChunks },
  )
  // ⚠ §954：走分块音频源时 `toWav` 不会被调用（没有整场音频可转）
  //   ⇒ §805 的时长护栏在原生链上原本恒判 `no-ground-truth`
  //   ⇒ 现在用各块时长之和补上 ⇒ **护栏在原生链上复活**。
  if (typeof finalText.measuredAudioSec === 'number' && Number.isFinite(finalText.measuredAudioSec)) {
    measuredAudioSec = finalText.measuredAudioSec
  }

  // ⚠⚠ §955：护栏的**墙钟**不再恒等于 `elapsedMs`。
  //   原生链上必须用**与盘上音频同源**的原生采集窗口
  //   ⇒ 否则比的是两个**不同相位**的钟（JS 的 `start()` 比原生服务开采**晚**）
  //   ⇒ 差的是**系统偏移**而不是「音频丢了多少」⇒ 短录音会假警报。
  //   ⇒ 决策在纯函数 `resolveDurationGuardInput` 里（§52 纪律：决策必须在能测的地方）。
  //   ⇒ Web / iOS 链（没有原生窗口）⇒ 走 JS 墙钟 ⇒ **行为一字未变**。
  const guardInput = resolveDurationGuardInput({
    elapsedMs,
    nativeCapture: readNativeCaptureSnapshot(deps.nativeCaptureSnapshot),
    measuredAudioSec,
  })
  // 只 warn、不改状态：阈值与是否升级为阻断属产品口径（§805.3）。
  // `no-ground-truth` 不算异常 —— 转码失败那条路由 `refetchFullTranscript` 自己兜。
  const drift = assessDurationDrift(guardInput.wallClockMs, guardInput.audioSec)
  if (!drift.ok) {
    console.warn(
      `[meeting-finalize] 录音时长与落盘音频不符：墙钟 ${guardInput.wallClockMs}ms`
      + `（${describeDurationGuardSource(guardInput.source)}）vs 音频 `
      + `${guardInput.audioSec === null ? '未知' : Math.round(guardInput.audioSec * 1000) + 'ms'}`
      + `（差 ${drift.driftMs}ms > 容差 ${Math.round(drift.toleranceMs)}ms，${drift.reason}）`,
    )
  }
  // ★§818：让「转码失败」本身可见。
  //
  // ⚠ 此前这条链**从头到尾静默**：转码失败被空 catch 块吞掉 ⇒ 退回原 webm 上传
  // ⇒ 网关拒收 ⇒ `reason = 'empty-transcript'` ⇒ 而**唯一生产调用方（本函数）
  // 一个 `reason` 字段都不读** ⇒ 用户只看到「只有分段文本，没有全文精校」，真因从未出现。
  // 时长护栏在这条路上也判 `ok`（`no-ground-truth`）——
  // `assessDurationDrift` 的注释本来就写着「真正的处理是让转码失败本身可见」。
  //
  // ⚠⚠ **决策在纯函数 `finalizeFailureNotice` 里**，不在这里：本函数跑不到（§52），
  //   把判断写在调用点的话，判据只能对着 `console.warn` 做源码匹配 ——
  //   实测把 warn 改成 `void 0 && console.warn` 照样全绿（零牙）。
  //
  // ⚠ 触达面与上面那条一致：**只到开发者 console**（§813）。要不要升到用户可见
  //   （`notify('warning')`）属产品口径，**待属主拍板，本轮不代拍**。
  const notice = finalizeFailureNotice(finalText)
  if (notice) console.warn(`[meeting-finalize] ${notice}`)
  // ★ 决策全部在纯函数里（§52）：采纳什么、用哪些分段、名单是谁、改哪几个同音字。
  //   这一段此前散在本函数体内，而本函数在 node 里跑不到 ⇒ 那些决策
  //   **一行行为门都没有**（§45/§48/§49/§50 四轮 bug 全在这里面）。
  const meeting = await getMeeting(meetingId)
  const plan = planRefine({
    meetingId,
    segments: segs,
    full: { text: finalText.text, applied: finalText.applied, segments: finalText.segments },
    participants: meeting?.participants,
    elapsedMs,
    // §825：音频时间基错位时，整段重转文本**不进**精校（理由见 planRefine）。
    audioStartsAtMs: recording?.audioStartsAtMs ?? 0,
  })
  outcome.adopted = plan.adopted
  const { roster, refineSegments } = plan

  try {
    // ★ meta.participants 必须传：不传的话服务端 `participantLeak` 的名单是空的
    //   ⇒ 恒返回 "" ⇒ `guardRefineResult` 永不触发 ⇒ `refine_rejected` 永不产生
    //   ⇒ 前端的 'refine-failed' 状态与对应文案**在生产里永远不会出现**（§48）。
    //   说话人来源的名字按构造就带在 transcript 的 [名字] 前缀里，
    //   `participantLeak` 的 `!Contains(transcript, w)` 会正确放行。
    const result = await meetingsApi.refine(meetingId, refineSegments, ['en'], {
      title: meeting?.title ?? undefined,
      participants: roster,
      location: meeting?.location ?? undefined,
      // ★ §138.3：主题就在这个作用域里（上面已读 title/location/participants），
      //   此前**只差这一个字段**就没人传。主题里装的是专有名词，
      //   而专有名词正是 ASR 错得最多、也最该被精校纠正的那一类
      //   （实测：mimo 对 / minimax 把「悬界芯片」写成「玄介芯片」）。
      topic: meeting?.topic ?? undefined,
    })
    outcome.refined = true
    // 先落精校结果与状态：**无论后面的入库成不成，用户至少拿得到精校文本**。
    await updateMeeting(meetingId, {
      refinedTranscript: result.refinedTranscript,
      // ⚠ 与 meeting-ingest 同一判定，单一来源在 refine-outcome-notice。
      status: refineStatusFor({ fromFallback: result.fromFallback, rejected: result.rejected }),
    })
    // ★ 这一步之前**根本不存在**，于是用户需求「时间点自动加进计划日程」
    //   在随手记录音上从未生效（§48）。事实链：服务端 `finalizeKxRefine` 返回
    //   `"todos": resp.Todos`（server_meeting_ingest.go:328）→ 前端 normalizeRefine
    //   解析进 result.todos → 而调用点只读了 3 个字段，**带 due 的行动项被丢弃**。
    //   失败必须单独兜：入库抛错不能让已经成功的精校被说成「未生效」。
    try {
      const fresh = await getMeeting(meetingId)
      if (fresh) {
        const ingested = await ingestMeetingArtifacts(fresh, result)
        outcome.todosCreated = ingested.todosCreated
        outcome.reminderPlanned = ingested.reminderPlanned
        outcome.reminders = ingested.reminders
        outcome.dueUnresolved = ingested.dueUnresolved
        outcome.eventsCreated = ingested.eventsCreated
        // ⚠§229.1：这三格不补，`buildIngestNotice` 收到的永远是初值 0/false
        //   ⇒ 「没听懂」「没建成」会被说成「什么也没发生」。
        outcome.eventUnresolved = ingested.eventUnresolved
        outcome.eventFailed = ingested.eventFailed
      }
    } catch {
      outcome.ingestFailed = true
    }
    // ⚠ 降级或被拦下时**不能**再报「精翻完成」。服务端有两条不同的回落路径，
    //   都带 refine_fallback：parseRefineJSON 失败 / guardRefineResult 拦下。
    //   降级本身是对的（宁可给原文也别给空），但必须说实话，且要说**对**原因。
    const notice = buildRefineNotice({
      fromFallback: result.fromFallback,
      rejected: result.rejected,
      // ⚠⚠ §908/D77：原因必须**一路传到底**，否则文案会退回「名单污染」那句 ——
      //   而长会议上真正的原因是「转写太长，模型回显不完」。
      //   不传这一项，`rejectedReason` 就断在 API 层，用户被指向一个错的原因。
      rejectedReason: result.rejectedReason,
      // ⚠⚠ §910/D79：留存判定的原因也必须传到文案层，
      //   否则「模型写成了摘要」会被报成「云端返回无法解析」。
      retentionReason: result.retentionReason,
      adopted: outcome.adopted && finalText.applied,
      failedSegments: finalText.failedSegments,
    })
    // 入库结果如实说：建了待办就报数，没建出来也要说，
    // 别让用户以为「时间点已经进日程了」。
    // ⚠ 级别仍由 notice.kind 决定，**不因 tail 改判**：精校确实成功了，
    //   就不该因为附带一句「行动项未能入库」把成功降级成中性提示。
    // §66.7：文案搬进纯模块（meeting-ingest-notice.ts）——
    //   旧写法「N 项行动已加入待办**与提醒**」正是随手记侧门禁的**负控样本**
    //   （0 条提醒时也在暗示「时间点进了日程」），而它内联在组件里、无法被行为断言。
    const tail = buildIngestNotice({
      todosCreated: outcome.todosCreated,
      reminderPlanned: outcome.reminderPlanned,
      reminders: outcome.reminders,
      dueUnresolved: outcome.dueUnresolved,
      eventsCreated: outcome.eventsCreated,
      eventUnresolved: outcome.eventUnresolved,
      eventFailed: outcome.eventFailed,
      ingestFailed: outcome.ingestFailed,
    })
    notify(notice.kind, notice.text + tail)
  } catch (e) {
    // 「已取消」和「失败」对用户是两件不同的事：前者不该被提示成「稍后重试」，
    // 否则会让人以为录音或会议出了什么问题。
    if (isAbortError(e)) {
      outcome.aborted = true
      notify('info', '已停止精翻')
      return outcome
    }
    // ⚠ 2026-10-06：原文写的是「精翻稍后可在会议详情重试」——
    // 而**会议详情里没有那个入口**。那句话让用户去一个不存在的地方，
    // 是「告诉用户去做产品做不到的事」，比不说更糟。
    // 失败时用户**确实**有东西可用：录音期间逐段落库的转写仍在
    // （每段带说话人标签，§40 修的就是它在收尾时不丢），状态停在 completed。
    notify('warning', '录音已结束，精翻未生效（分段转写已保存）')
  }
  return outcome
}