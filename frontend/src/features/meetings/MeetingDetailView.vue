<template>
  <div class="studio">
    <div v-if="loading" class="state"><Skeleton :count="3" /></div>
    <EmptyState
      v-else-if="!meeting"
      icon="⚠️"
      title="会议不存在"
      action-label="返回列表"
      @action="router.push('/meetings')"
    />
    <template v-else>
      <!--
        §129：`summarize-disabled` **刻意不**包含「行动项源未就绪」这一条。
        摘要来自转写正文（displaySegments 非空即可），源未就绪只影响行动项，
        而 §129 的修法是让那一支**如实说明**没建成待办。把按钮禁掉会连带
        剥夺「先要一段总结」这个正当用法。
        ⚠ 注释必须放在标签**外面**：写在属性之间会被 Vue 模板编译器当成一个
        属性名（实测报 TS2345：多出 `'<!--': boolean` 这种键）。
      -->
      <MeetingStudioMenu
        :archived="!!meeting.archivedAt"
        :recording="micOn"
        :summarizing="summarizing"
        :summarize-disabled="summarizing || !displaySegments.length"
        :can-dispatch="canDispatch"
        @summarize="onSummarize"
        @action="onMenuAction"
      />

      <p v-if="statusLine" class="status" role="status">{{ statusLine }}</p>
      <p v-if="sttError" class="err" role="alert">{{ sttError }}</p>

      <div class="split">
        <section class="transcript">
          <!--
            ★ §107：精校结果**以前根本不显示**。
              `LocalMeeting.refinedTranscript` 一直有值（meetings-store.ts:257），
              但本页从头到尾没读过它 —— 精校跑完、结果存了，用户打开这场会议看不到。
              判定逻辑抽到 refined-transcript-view.ts（纯函数，可单测），
              本段只负责渲染。
          -->
          <div v-if="refinedView.badge" class="refined-bar" role="status">
            <span class="refined-badge" :class="{ failed: !refinedView.hasRefined }">
              {{ refinedView.badge }}
            </span>
            <button
              v-if="refinedView.hasRefined"
              type="button"
              class="refined-toggle"
              @click="toggleRefined"
            >{{ refinedMode === 'refined' ? '看原文' : '看精校版' }}</button>
          </div>
          <div v-if="refinedMode === 'refined'" class="refined-body">{{ meeting?.refinedTranscript }}</div>
          <TranscriptSegmentList
            v-else
            :segments="displaySegments"
            :is-recording="micOn"
            :interim-text="interimCaption"
            :editable="true"
            @append="onAppendUtterance"
            @edit="onEditSegment"
          />
        </section>
        <div class="meeting-learning">
          <!--
            P2：会议待办进 PG tasks（origin_kind=meeting）。
            之前这条链路是断的 —— 会议待办只写进移动端本地 SQLite 的
            local_todos 表，PC 端和其他设备都看不到，负责人也退化成描述里
            的「负责人：xxx」字符串。服务端按 (meeting, action text) 幂等，
            所以重复点不会产生两份。
          -->
          <AddToLearningButton
            source-kind="meeting"
            :source-id="meetingId"
            as-task
            task-type="meeting"
          />
        </div>
        <MeetingInsightPanel
          :summary="liveSummary || meeting.liveSummary"
          :final-summary="meeting.summary"
          :recommendations="mergedRecs"
          :recommend-fallback="recommendFallback"
          :is-updating="isUpdating || summarizing"
          :is-recording="micOn"
          @share-todo="onShareTodo"
          @acc-todo="onAccTodo"
          @open-related="onOpenRelated"
        />
      </div>

      <MeetingMicDock :recording="micOn" :busy="micBusy" @toggle="onMicToggle" />
      <button
        v-if="micOn && speakers.length"
        type="button"
        class="speakers-btn"
        @click="showSpeakers = true"
      >👤 说话人</button>
      <button
        type="button"
        class="speakers-btn"
        @click="showVoiceprints = true"
      >🎙️ 声纹库</button>
      <VoiceprintSheet
        :open="showVoiceprints"
        @close="showVoiceprints = false"
      />
      <SpeakerLabelSheet
        :open="showSpeakers"
        :speakers="speakers"
        @close="showSpeakers = false"
        @label="onSpeakerLabel"
      />
      <MeetingSettingsSheet
        :open="showSettings"
        :title="meeting.title"
        :topic="meeting.topic"
        :location="meeting.location"
        :participants="meeting.participants"
        :tags="meeting.tags"
        :summary-skill="meeting.summarySkill"
        :started-at="meeting.startedAt"
        :transcript-hint="displaySegments[0]?.text"
        @close="showSettings = false"
        @save="onMetaSave"
      />
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { EmptyState, Skeleton } from '@/components'
import { useMeetingRecorder } from '../../composables/useMeetingRecorder'
import { useLiveSummary } from '../../composables/useLiveSummary'
import { useToast } from '../../composables/useToast'
import { finalizeRecording } from './meeting-recording-finalize'
import {
  getMeetingWithSegments, updateMeeting, updateSegmentText, saveSegment, updateTranscript,
  type LocalMeeting, type MeetingSegment, type RecommendItem,
} from './meetings-store'
import { applySegmentTextEdit } from './segment-edit'
import { buildUtteranceSegment } from './meeting-utterance'
import { summarizeMeeting } from './meetings-ai'
import { buildSummaryPrompt } from './meeting-skills'
import { captureDeviceLocation, formatCapturedTitle } from './meeting-meta'
import { mergeRecommendations, relatedQueryFromTranscript } from './meeting-related'
import { searchRelatedContext } from './meeting-related-search'
import { createMeetingTodos, handoffTodoToAcc, shareTodoWithPerson } from './meeting-todo-persist'
import { buildMeetingReminderNote, buildNoActionItemsNote } from './meeting-reminder-note.ts'
import { dedupeSegments, renderTranscript } from './meeting-dedup.ts'
import { resolveRefinedView, type RefinedViewMode } from './refined-transcript-view.ts'
import { LEFTOVER_RECORDING_HINT } from './meeting-finalize-persist-notice'
import AddToLearningButton from '../study/AddToLearningButton.vue'
import type { MeetingTodoDraft } from './meeting-todos'
import type { MeetingStudioAction } from './meeting-page-actions'
import { useMeetingStudio } from './use-meeting-studio'
import { markListDirty } from '../../composables/list-scene-store'
import TranscriptSegmentList from './TranscriptSegmentList.vue'
import MeetingInsightPanel from './MeetingInsightPanel.vue'
import MeetingMicDock from './MeetingMicDock.vue'
import MeetingSettingsSheet from './MeetingSettingsSheet.vue'
import MeetingStudioMenu from './MeetingStudioMenu.vue'
import SpeakerLabelSheet from './SpeakerLabelSheet.vue'
import VoiceprintSheet from './VoiceprintSheet.vue'
import { useApiError } from '../../composables/useApiError'
import { useAuthStore } from '../../stores/auth'

const apiError = useApiError()
const route = useRoute()
const router = useRouter()
const toast = useToast()
const auth = useAuthStore()
const meetingId = computed(() => String(route.params.id || ''))
const loading = ref(true)
const meeting = ref<LocalMeeting | null>(null)
const storedSegments = ref<MeetingSegment[]>([])
const showSettings = ref(false)
const showSpeakers = ref(false)
const showVoiceprints = ref(false)
const summarizing = ref(false)
const micBusy = ref(false)
const wantMic = ref(false)
const noteRecs = ref<RecommendItem[]>([])
/**
 * ⚠ §411：持久化的**智能体推荐**必须有自己的槽位，不能塞进 `noteRecs`。
 *
 * 缺陷现场：`noteRecs` 同时被两个来源写 ——
 *   ① `noteRecs.value = m.recommendations`（库里的智能体产出）
 *   ② `refreshRelated()` 里的 `noteRecs.value = await searchRelatedContext(...)`
 *      （本地 LIKE + 维基百科），**无条件整体覆盖**。
 * 而 `onMounted` 里 ① 在前、② 在后，中间只隔 4 行 ⇒ ① 是**死代码**。
 *
 * 为什么「打开已录完的会议」必然丢：`recommendations.value`（实时那一份）
 * 只有两处会被填 —— `onMicToggle` 的 seedSegments（:293，仅录音时）与
 * `refresh(true)`（:282，仅停止录音时）；`useLiveSummary` 的 watch 盯的是
 * runtime 的 `segments`，而 `load()` 只填 `storedSegments`。
 * ⇒ 不录音就打开 ⇒ 智能体那一份恒为 `[]` ⇒ 面板上一条都没有。
 * ⭐ 实测：模拟该场景，存活 **0 条**。
 *
 * 为什么不给 `refreshRelated()` 改成「合并」：它由
 * `watch(displaySegments.length)` 每 2.5s 触发一次，改成合并会让上一轮的
 * 检索结果**永久累积**下去。⇒ 两个通道各归其主，只在渲染口合并。
 */
const agentRecs = ref<RecommendItem[]>([])
let relatedTimer: ReturnType<typeof setTimeout> | null = null

const {
  isRecording, segments, sttError, interimCaption, speakers, start, stop, appendText, formatElapsed, labelSpeaker,
  seedSegments, editSegmentInMemory,
} = useMeetingRecorder(meetingId)
const { liveSummary, recommendations, recommendFallback, isUpdating, refresh } = useLiveSummary(meetingId, segments, {
  meta: computed(() => ({
    title: meeting.value?.title ?? undefined,
    participants: meeting.value?.participants,
    location: meeting.value?.location ?? undefined,
  })),
})

const micOn = computed(() => wantMic.value || isRecording.value)
// ★ 在源头去重（2026-10-06）：整页所有消费者（渲染、重新总结、待办转交）
//   都从这里取数据，重复文本会顺着流进 LLM 摘要与待办。在 computed 里
//   做一次即可，computed 本身带缓存，不会每帧重算。
const displaySegments = computed(() => dedupeSegments(
  segments.value.length ? segments.value : storedSegments.value,
))
// §411：智能体那一路（实时 + 库里那份）作为 primary，检索那一路作为 extra ——
//   与改动前的优先级一致，只是多接上了库里那份。三个来源两两去重按 type:id。
const mergedRecs = computed(() =>
  mergeRecommendations(mergeRecommendations(recommendations.value, agentRecs.value), noteRecs.value))
// ★ §107：精校版/原文的展示判定。纯函数见 refined-transcript-view.ts。
const refinedView = computed(() => resolveRefinedView({
  refinedTranscript: meeting.value?.refinedTranscript ?? null,
  status: meeting.value?.status ?? null,
}))
// 用户手动切回原文后，不要被下一次 load() 顶回精校版 —— 但**换会议时必须复位**。
const refinedModeOverride = ref<RefinedViewMode | null>(null)
const refinedMode = computed<RefinedViewMode>(() => {
  if (refinedModeOverride.value) return refinedModeOverride.value
  return refinedView.value.mode
})
function toggleRefined() {
  refinedModeOverride.value = refinedMode.value === 'refined' ? 'original' : 'refined'
}
const statusLine = computed(() => {
  if (micOn.value) return `录音中 ${formatElapsed()}`
  if (meeting.value?.location) return meeting.value.location
  return ''
})

async function load() {
  loading.value = true
  const data = await getMeetingWithSegments(meetingId.value)
  meeting.value = data?.meeting ?? null
  storedSegments.value = data?.segments ?? []
  loading.value = false
  // ⚠ 换会议/首次加载时复位手动切换，否则「在 A 会议切到原文」
  //   会让打开 B 会议时也停在原文 —— 那是**上一场会议的残留状态**。
  refinedModeOverride.value = null
  // ★ §411：智能体的持久化推荐在这里取，而不是在 `onMounted` 里取。
  //   放这里有两个原因：① `load()` 在挂载与「停止录音」后都会跑，库里的值
  //   始终是最新的；② 原先那行在 `onMounted` 里、`refreshRelated()` 之前，
  //   **必然**被后面那次整体覆盖（它是死代码）。
  //   换会议时整份替换 ⇒ 不会把上一场会议的推荐带过来。
  agentRecs.value = data?.meeting?.recommendations ?? []
}

const { canDispatch, onStudioAction } = useMeetingStudio(meeting, load)

async function onMenuAction(id: MeetingStudioAction) {
  if (await onStudioAction(id) === 'classify') showSettings.value = true
}

async function captureContext(m: LocalMeeting) {
  const loc = m.location || await captureDeviceLocation()
  const title = m.title || formatCapturedTitle({
    startedAt: m.startedAt, location: loc, topic: m.topic,
  })
  if (loc !== m.location || title !== m.title) {
    await updateMeeting(m.id, { location: loc, title })
    meeting.value = { ...m, location: loc, title }
  }
}

async function onMicToggle() {
  if (micBusy.value) return
  micBusy.value = true
  if (micOn.value) {
    wantMic.value = false
    try {
      // ★§49：这条路径以前**录完什么都不会发生** —— 没有精校、没有行动项、
      //   没有日程提醒。而它是会议列表「开始录音」的主入口
      //   （MeetingListView push `?record=1` → onMounted 自动开录）。
      //   与随手记路径共用同一份收尾编排，避免「修好一条、另一条照旧」。
      const stopped = await stop()
      // 先 load()：storedSegments 来自持久层，包含 stop() 期间才落库的在途分段。
      // ⚠ 绝不能用 recorder 的 segments —— activeMeetingId 在 stop() 里被置空，
      //   那个 computed 恒为 []（见 meeting-recording-finalize.ts 的头注释）。
      await load()
      await finalizeRecording(
        meetingId.value,
        storedSegments.value,
        stopped?.durationMs ?? meeting.value?.durationMs ?? 0,
        {
          notify: (kind, text) => {
            if (kind === 'success') toast.success(text)
            else if (kind === 'warning') toast.warning(text)
            else toast.info(text)
          },
        },
        // ★§825：与 durationMs 同源，都只能来自 stop() 的返回值。
        // 中途切过设备时这里非 0 ⇒ 收尾不再把整段重转按绝对时间挂回说话人。
        { audioStartsAtMs: stopped?.audioStartsAtMs ?? 0 },
      )
      await refresh(true); await load()
    }
    finally { micBusy.value = false }
    return
  }
  wantMic.value = true
  try {
    if (storedSegments.value.length) {
      // 恢复录音:把 localDB 的既有分段预置进 runtime,续录后的
      // transcript 才含完整历史(segments 是 runtime 的圈定只读视图,
      // 不能再直接 splice)。
      seedSegments(storedSegments.value)
    }
    const ok = await start({ resume: storedSegments.value.length > 0 })
    if (!ok) wantMic.value = false
  } finally { micBusy.value = false }
}

async function onSummarize() {
  const segs = displaySegments.value
  if (!meeting.value || !segs.length) return
  summarizing.value = true
  try {
    // ⚠ §427：原先这里**手搓**了一份 transcript 拼接
    //   （`segs.map((s) => `[${s.speakerLabel || '说话人'}] ${s.text}`).join('\n')`），
    //   而全仓其余 5 个出口走的是 `renderTranscript`。
    //   ⇒ 同一个量在两处各算一遍，说话人兜底就是这么漂掉的
    //     （`renderTranscript` 那侧没有兜底，会拼出 `[null]`）。
    //   ⇒ 这里改为直接复用真身；实测对**已去重**的分段两条拼法逐字一致。
    const transcript = renderTranscript(segs)
    const summary = await summarizeMeeting(transcript, {
      skillPrompt: buildSummaryPrompt(meeting.value.summarySkill),
    })
    await updateMeeting(meetingId.value, { summary })
    // ★ §129：源必须与面板渲染的**同一个**。
    //   面板绑的是 `liveSummary || meeting.liveSummary`（先内存、后持久层），
    //   而这里此前只读内存 ref —— 重新打开一场已经跑过实时总结的会议时它是
    //   null，于是建 0 条待办 + 一句「已生成当前总结」。
    //   同仓另两个读者（meeting-page-actions.ts:22 / meeting-ingest.ts:63）
    //   读的正是持久层那份 ⇒ 三处不一致时，页面是唯一错的那个。
    const source = liveSummary.value ?? meeting.value.liveSummary ?? null
    const items = source?.actionItems ?? []
    let reminderNote = ''
    if (items.length) {
      // ⚠ §66.7：unresolved 必须**一起**解构并传下去 ——
      //   只传前两个，文案函数拿不到第三格，「期限没听清」就永远说不出口。
      //
      // ⚠ §410：`count` 同理必须传下去。它是「本次真正新写入」的条数，
      //   而这个按钮**可以重复点**（`alreadyCreatedTodo` 的幂等）——
      //   录完音的 finalizeRecording 已经把同一批行动项写进了 local_todos，
      //   用户再点「总结」时每一条都命中查重、count 恒为 0。
      //   不传它 ⇒ 0/0/0 被读成「行动项没有可解析的期限，未建提醒」，
      //   而事实上待办在库里、提醒也早已建成 ⇒ 产品把自己的状态说反了。
      //
      // ⚠ 顺序**刻意**保持在 reminders/reminderPlanned/unresolved 之后：
      //   既有门 `reminder-outcome-honesty.test.ts:426` 的正则写死了
      //   `{reminders, reminderPlanned, unresolved}` 紧跟左花括号。
      //   把 count 插在前面会让那道门失效 —— 那道门守的是「unresolved 真的
      //   被解构并传下去」，我的改法没有削弱它半分，却会让它误报。
      //   ⇒ 适配既有判据，而不是放宽既有判据。
      const { reminders, reminderPlanned, unresolved, count: created } = await createMeetingTodos(
        meetingId.value, items, meeting.value.noteId, meeting.value.title ?? '',
      )
      // 文案在纯模块里（meeting-reminder-note.ts），判据能直接断言返回值，
      // 不必去组件里扫「那句提示长什么样」——扫不出真假，注释里也能写同一句。
      reminderNote = buildMeetingReminderNote(reminders, reminderPlanned, unresolved, created)
    } else {
      // ★ §129：以前这两条分支都没有任何提示 ⇒「没试过」与「试了但没建成」同形。
      //   一条待办都没建却只说「已生成当前总结」，用户会以为行动项已经进去了。
      //
      // ⚠ §416：第二个实参是**录音是否还在进行**。不传（缺省 false）时这句会说
      //   「再点「总结」也补不出来」—— 而正在录音时那句话是假的：分段还在长，
      //   实时总结迟早会来，那时再点一次是真能拿到行动项的。
      //   ⇒ 这个承诺必须随录音状态分岔，否则必有一半是在骗用户。
      reminderNote = buildNoActionItemsNote(source !== null, micOn.value)
    }
    markListDirty('meetings')
    await load()
    toast.success(`已生成当前总结${reminderNote}`)
  } catch (e) {
    toast.error(e instanceof Error ? e.message : apiError(e, 'errors.operateFailed'))
  } finally { summarizing.value = false }
}

async function onAppendUtterance(text: string) {
  const saved = await appendText(text)
  if (!saved) {
    // ⚠ §131.2（顺带修的既有缺陷）：`rt.appendText` 第一行就是
    //   `if (!meetingId) return null`，而 `activeMeetingId` 在 `stop()` 里被置空
    //   （recordingRuntime.ts:634）⇒ **录完之后**再点这个按钮是静默无反应，
    //   可输入框上写的提示是无条件可用。这一支以前是直接 `if (!saved) return`。
    //   补上落库分支：与「更正」共用 store 这条 SSOT 路径。
    const last = storedSegments.value[storedSegments.value.length - 1]
    const draft = buildUtteranceSegment({
      meetingId: meetingId.value,
      text,
      startMs: last?.endMs ?? meeting.value?.durationMs ?? 0,
    })
    if (!draft) return
    const id = await saveSegment(draft)
    storedSegments.value = [...storedSegments.value, { id, ...draft }]
    await updateTranscript(meetingId.value, renderTranscript(storedSegments.value))
    await refreshRelated()
    return
  }
  // ★ 这里原来写的是 `storedSegments.value = [...displaySegments.value]`。
  //   `displaySegments` 是**去重后**的（`dedupeSegments` 会整段丢弃零净增的行 ——
  //   `if (step.added) out.push(...)`），把去重结果写回 storedSegments 等于
  //   把「原始段」这一份真相换成了「展示用子集」：下一次 `seedSegments`
  //   （续录恢复）就会拿这份子集当完整历史，丢掉的段在续录后的 transcript 里
  //   永远回不来。落库已由 appendText 做过，这里只同步内存里的**原始**列表。
  storedSegments.value = [...segments.value]
  await refreshRelated()
}

/**
 * §131：更正一段转写的正文（交互式校对）。
 *
 * ⚠ 落库**永远**走 store（`updateSegmentText`）—— runtime 只在录音中持有这一段，
 *   不是 SSOT；把落库挂到 runtime 后面会让「录完之后改」这条主路径直接失效。
 *   录音中才额外同步它的内存副本，否则 `displaySegments` 优先读 `segments.value`，
 *   刚写进 DB 的正文会被内存旧文本立刻盖回去（用户看到「没改成」）。
 */
async function onEditSegment(payload: { id: string; text: string }) {
  // 判空 / 判未变 / 判命中都在纯函数里，页面只负责把「改哪一条」接到两写者上。
  const res = applySegmentTextEdit(displaySegments.value, payload.id, payload.text)
  if (!res.changed) {
    // 空文本要说一句：用户清空输入框点了保存，沉默会被当成「保存了」。
    if (res.reason === 'empty-text') toast.warning('正文不能为空')
    else if (res.reason === 'not-found') toast.warning('这段转写已不在列表里')
    return
  }
  const edited = res.segment as MeetingSegment
  try {
    await updateSegmentText(payload.id, edited.text)
    if (!(await editSegmentInMemory(payload.id, edited.text))) {
      // 没在录：runtime 里没有这一段，改本地那份**原始**列表。
      // ⚠ 不能把 `res.segments` 直接赋值过来 —— 那是去重后的子集（见上）。
      storedSegments.value = storedSegments.value.map(
        (s) => (s.id === payload.id ? edited : s),
      )
    }
    await refreshRelated()
  } catch (e) {
    toast.error(e instanceof Error ? e.message : apiError(e, 'errors.operateFailed'))
  }
}

async function onSpeakerLabel(profileId: string, displayName: string) {
  await labelSpeaker(profileId, displayName)
}

async function onMetaSave(data: {
  title: string; topic: string; location: string; participants: string[]; tags: string[]; summarySkill: string
}) {
  await updateMeeting(meetingId.value, data)
  markListDirty('meetings')
  await load()
}

async function onShareTodo(draft: MeetingTodoDraft) {
  try {
    await shareTodoWithPerson(draft, meeting.value?.title || '')
    toast.success('已生成转交内容')
  } catch (e) {
    toast.error(e instanceof Error ? e.message : apiError(e, 'errors.operateFailed'))
  }
}

async function onAccTodo(draft: MeetingTodoDraft) {
  try {
    const task = await handoffTodoToAcc(draft, meeting.value?.title || '')
    toast.success('已转交 ACC')
    router.push(`/settings/scheduled-tasks/${task.id}`)
  } catch (e) {
    toast.error(e instanceof Error ? e.message : apiError(e, 'errors.operateFailed'))
  }
}

function onOpenRelated(item: RecommendItem) {
  if (item.url) {
    window.open(item.url, '_blank', 'noopener')
    return
  }
  if (item.type === 'note') router.push(`/notes/${item.id}`)
  if (item.type === 'meeting' || item.type === 'knowledge') {
    router.push({ name: 'meeting-detail', params: { id: item.id } })
  }
}

async function refreshRelated() {
  const q = relatedQueryFromTranscript(displaySegments.value.map((s) => s.text))
  // 笔记按 workspace_id 分区，不传会落到 'default'、关联笔记永远为空
  noteRecs.value = await searchRelatedContext(q, {
    excludeMeetingId: meetingId.value,
    workspaceId: auth.workspaceId || 'default',
  })
}

onMounted(async () => {
  await load()
  const m = meeting.value
  if (!m) return
  // ⚠ §411：原来这里有一行 `noteRecs.value = m.recommendations`，已被删除。
  //   它是**死代码** —— 下面 `await refreshRelated()` 会整体覆盖 `noteRecs`
  //   （无条件赋值），而中间只隔两行。智能体的持久化推荐已改由 `load()`
  //   写进 `agentRecs` 这个独立槽位，渲染口 `mergedRecs` 三路合并。
  await captureContext(m)
  // ⚠⚠ §834/D2：原来这里写的是
  //   `const auto = route.query.record === '1' || m.status === 'recording'`
  //   ⇒ 状态卡在 recording 的会议**一打开就自动开麦**。而同一个仓库里另外两处
  //   对同一状态给出的是**相反契约**：
  //     · meetings-live.ts::getRecordingBySession 注释：「杀进程后提示恢复，
  //       **不自动重开采集**」；
  //     · useSessionLiveRecord 真这么做：查到遗留行 ⇒ toast.warning 一句提示。
  //   ⇒ 自动开麦那处是违背仓库自身声明的孤例，且后果最重：麦克风授权弹窗 +
  //     **把新录音混进同一场会议**（seedSegments + start({resume:true})），
  //     用户完全没打算录。
  // ⚠ 删掉它**不影响**任何合法启动：三个「开始录音」入口
  //   （MeetingListView / MeetingRecordView / router-mobile）都在 createMeeting
  //   之后立刻带 `?record=1` 跳过来；进程内续录走 micOn（= wantMic || isRecording），
  //   麦克风还开着时那条分支本就不触发。
  const auto = route.query.record === '1'
  if (auto) await onMicToggle()
  else if (m.status === 'recording') {
    // ⚠ 只提示、不开麦，且**逐字复用**会话侧那句承诺（同一个常量 ⇒ 两处不再各说各话）。
    toast.warning(LEFTOVER_RECORDING_HINT)
  }
  await refreshRelated()
})

watch(() => displaySegments.value.length, (count) => {
  if (count === 0) return
  if (relatedTimer) clearTimeout(relatedTimer)
  relatedTimer = setTimeout(() => { void refreshRelated() }, 2500)
})

onUnmounted(() => {
  if (relatedTimer) clearTimeout(relatedTimer)
})

</script>

<style scoped>
.studio { display: flex; flex-direction: column; height: 100%; min-height: 0; }
.state { padding: var(--space-3); }
.status, .err { margin: 0; padding: 8px 12px; font-size: var(--text-sm); flex-shrink: 0; }
.status { color: var(--text-secondary); background: var(--bg-card); }
.err { color: var(--danger); }
.split {
  flex: 1; min-height: 0; display: grid;
  grid-template-columns: minmax(0, 7fr) minmax(140px, 3fr);
}
.transcript { min-height: 0; overflow: hidden; display: flex; flex-direction: column; }
/* §107：精校版展示区。徽章与列表徽章同一套语义（refine-failed 用警示色，
   中性灰读起来像「正常完成」—— 见 MeetingListView.vue 的同名注释）。 */
.refined-bar {
  display: flex; align-items: center; gap: var(--space-2);
  padding: 6px var(--space-3); flex-shrink: 0;
  background: var(--bg-subtle); font-size: var(--text-sm);
}
.refined-badge { color: var(--success, var(--text-secondary)); font-weight: 600; }
.refined-badge.failed { color: var(--warning, var(--text-muted)); font-weight: 500; }
.refined-toggle {
  margin-left: auto; border: 1px solid var(--border); border-radius: var(--radius-full);
  background: var(--bg-card); color: var(--text-secondary);
  padding: 3px 10px; font-size: var(--text-2xs);
}
.refined-body {
  flex: 1; min-height: 0; overflow-y: auto;
  padding: var(--space-3);
  white-space: pre-wrap; line-height: 1.7;
  color: var(--text-primary); font-size: var(--text-smd);
}
.speakers-btn {
  /* 2026-10-06 修正：原 bottom = 14px < --bottom-chrome-height 56px，而本按钮
     自身只有 40px 高 ⇒ **整颗按钮 100% 落在底栏区域内**，再加上
     z-index: --z-fab(60) < --z-bottom-nav(70)，它被底栏完全盖住、点不到。 */
  position: fixed; left: var(--space-4); bottom: calc(var(--bottom-chrome-height) + var(--space-4));
  height: 40px; padding: 0 12px; border-radius: 999px; border: 1px solid var(--border);
  background: var(--bg-card); z-index: var(--z-fab); font-size: var(--text-smd);
}
@media (max-width: 720px) {
  .split { grid-template-columns: 1fr; grid-template-rows: minmax(0, 7fr) minmax(140px, 3fr); }
  .insight-border { border-left: none; }
}
</style>
