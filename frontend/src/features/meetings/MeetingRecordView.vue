<template>
  <div class="record-view">
    <!-- 顶部栏由壳层统一渲染（标题"开始会议" + 全局返回键）。
         录音状态 / 计时作为内容区顶部的状态条展示；停止操作注入壳层顶栏右侧。 -->
    <HeaderActionsPortal>
      <button
        v-if="isRecording"
        type="button"
        class="record-stop-btn"
        @click="onStop"
      >
        停止
      </button>
    </HeaderActionsPortal>

    <!-- 录音状态条（非标题栏）：录音中红点 + 计时 -->
    <div class="rec-status" role="status" aria-live="polite">
      <span v-if="isRecording" class="rec-dot" aria-hidden="true" />
      {{ isRecording ? '录音中' : '准备录音' }}
      <span class="elapsed">{{ formatElapsed() }}</span>
    </div>

    <!-- 波形 -->
    <div class="waveform-wrap">
      <WaveformVisualizer
        :is-recording="isRecording"
        :width="waveWidth"
        :height="64"
        color="var(--brand-primary)"
        :show-time="false"
        :show-progress="false"
      />
    </div>

    <!-- 主区域：实时转写 -->
    <div class="transcript-area">
      <TranscriptSegmentList
        :segments="segments"
        :is-recording="isRecording"
      />
    </div>

    <!-- 实时摘要浮层 -->
    <LiveSummaryPanel
      :summary="liveSummary"
      :recommendations="recommendations"
      :is-updating="isUpdating"
    />

    <!-- 即时提醒 toast -->
    <MeetingAlertToast :alerts="alerts" @dismiss="dismissAlert" />

    <!-- 离线提示 -->
    <div v-if="!online" class="offline-banner">
      离线模式：转写继续，摘要将在联网后更新
    </div>

    <!-- 转写处理中指示 -->
    <div v-if="processingCount > 0" class="processing-bar">
      转写中…（{{ processingCount }} 段）
    </div>

    <!-- 底栏 -->
    <footer class="record-footer">
      <button type="button" class="footer-btn" @click="showMeta = true">📝 信息</button>
      <button
        v-if="isRecording && speakers.length > 0"
        type="button"
        class="footer-btn"
        @click="showSpeakers = true"
      >
        👤 说话人
      </button>
      <button
        v-if="!isRecording && !started"
        type="button"
        class="footer-btn primary"
        @click="onStart"
      >
        🎙 开始录音
      </button>
      <button
        v-else-if="isRecording"
        type="button"
        class="footer-btn danger"
        @click="onStop"
      >
        ⏹ 结束
      </button>
    </footer>

    <MeetingMetaSheet
      :open="showMeta"
      :title="meta.title"
      :location="meta.location"
      :participants="meta.participants"
      :started-at="meta.startedAt"
      @close="showMeta = false"
      @save="onMetaSave"
    />

    <SpeakerLabelSheet
      :open="showSpeakers"
      :speakers="speakers"
      @close="showSpeakers = false"
      @label="onSpeakerLabel"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { WaveformVisualizer } from '@/components'
import { useMeetingRecorder } from '../../composables/useMeetingRecorder'
import { useLiveSummary } from '../../composables/useLiveSummary'
import { useMeetingAlerts } from '../../composables/useMeetingAlerts'
import { useConfirm } from '../../composables/useConfirm'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'
import { getMeeting, updateMeeting } from './meetings-store'
import TranscriptSegmentList from './TranscriptSegmentList.vue'
import LiveSummaryPanel from './LiveSummaryPanel.vue'
import MeetingMetaSheet from './MeetingMetaSheet.vue'
import MeetingAlertToast from './MeetingAlertToast.vue'
import SpeakerLabelSheet from './SpeakerLabelSheet.vue'

const route = useRoute()
const router = useRouter()
const { confirm } = useConfirm()
const meetingId = route.params.id as string

const meta = reactive({
  title: null as string | null,
  location: null as string | null,
  participants: [] as string[],
  startedAt: Date.now(),
})

const {
  alerts, dismissAlert, onSummaryUpdated, requestNotificationPermission, reset: resetAlerts,
} = useMeetingAlerts()

const {
  isRecording, segments, speakers, processingCount, start, stop, formatElapsed, labelSpeaker,
} = useMeetingRecorder(meetingId)
const {
  liveSummary, recommendations, isUpdating, refresh,
} = useLiveSummary(meetingId, segments, {
  onUpdated: onSummaryUpdated,
  meta: computed(() => ({
    title: meta.title ?? undefined,
    participants: meta.participants,
    location: meta.location ?? undefined,
  })),
})

const started = ref(false)
const showMeta = ref(false)
const showSpeakers = ref(false)
const online = ref(navigator.onLine)
const waveWidth = ref(window.innerWidth - 32)

function onOnline() { online.value = true }
function onOffline() { online.value = false }

async function onStart() {
  const ok = await start()
  if (ok) started.value = true
}

async function onStop() {
  await stop()
  await refresh(true)
  router.replace({ name: 'meeting-detail', params: { id: meetingId } })
}

async function onMetaSave(data: { title: string; location: string; participants: string[] }) {
  meta.title = data.title || null
  meta.location = data.location || null
  meta.participants = data.participants
  await updateMeeting(meetingId, {
    title: data.title || null,
    location: data.location || null,
    participants: data.participants,
  })
}

async function onSpeakerLabel(profileId: string, displayName: string) {
  await labelSpeaker(profileId, displayName)
}

onMounted(async () => {
  window.addEventListener('online', onOnline)
  window.addEventListener('offline', onOffline)
  await requestNotificationPermission()
  resetAlerts()
  const m = await getMeeting(meetingId)
  if (m) {
    meta.title = m.title
    meta.location = m.location
    meta.participants = m.participants
    meta.startedAt = m.startedAt
    if (m.status === 'recording') {
      await onStart()
    }
  } else {
    router.replace('/meetings')
  }
})

onUnmounted(() => {
  window.removeEventListener('online', onOnline)
  window.removeEventListener('offline', onOffline)
})
</script>

<style scoped>
.record-view {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  background: var(--bg-base);
  position: relative;
}

/* 录音状态条（原 record-header 中的视觉元素；录音语义迁移到内容区顶部） */
.rec-status {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  height: 40px;
  padding: 0 var(--space-3);
  background: var(--bg-card);
  border-bottom: 1px solid var(--border);
  font-size: var(--text-sm);
  color: var(--text-primary);
  flex-shrink: 0;
}

.rec-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--danger);
  animation: rec-pulse 1.4s ease-in-out infinite;
}

@keyframes rec-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.35; }
}

.elapsed {
  margin-left: auto;
  font-variant-numeric: tabular-nums;
  color: var(--text-secondary);
}

/* Portal 注入到壳层标题栏的停止按钮（壳层 :deep 规则已给 44px 热区） */
.record-stop-btn {
  color: var(--danger);
  font-weight: var(--font-weight-semibold);
}

.waveform-wrap {
  padding: var(--space-2) var(--space-3);
  flex-shrink: 0;
}

.transcript-area {
  flex: 1 1 auto;
  min-height: 0;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  /* 为右侧摘要面板留出空间 */
  padding-right: min(50vw, 360px);
}

.offline-banner {
  position: fixed;
  bottom: calc(var(--app-safe-bottom) + var(--space-3));
  left: var(--space-3);
  right: var(--space-3);
  padding: 8px 12px;
  background: rgba(251, 191, 36, 0.9);
  color: #78350f;
  border-radius: var(--radius-md);
  font-size: 12px;
  text-align: center;
  z-index: var(--z-fab);
}

.processing-bar {
  position: fixed;
  bottom: calc(var(--app-safe-bottom) + var(--space-3));
  left: 50%;
  transform: translateX(-50%);
  padding: 4px 12px;
  background: var(--bg-card);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-full);
  font-size: 12px;
  color: var(--text-muted);
  z-index: var(--z-fab);
  box-shadow: var(--shadow-sm);
}

.record-footer {
  display: flex;
  gap: var(--space-2);
  padding: var(--space-3);
  border-top: 1px solid var(--border-subtle);
  background: var(--bg-card);
  flex-shrink: 0;
}

.footer-btn {
  flex: 1;
  padding: 12px;
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-base);
  font-size: 14px;
  cursor: pointer;
  color: var(--text-primary);
}

.footer-btn.primary {
  background: var(--brand-primary);
  color: #fff;
  border-color: transparent;
}

.footer-btn.danger {
  background: #ef4444;
  color: #fff;
  border-color: transparent;
}
</style>
