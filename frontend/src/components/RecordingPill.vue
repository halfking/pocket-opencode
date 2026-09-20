<!--
  RecordingPill — 全局录音指示条(2026-09-20 P0 录音后台化)。

  录音所有权上移到 recordingRuntime 后,会议/笔记录音跨页面存续;用户切离
  录音宿主页(会议详情 / 会话页 / 笔记页)后,由本组件提供:
  - 状态可见性:红点 + 类型 + 时长,任何页面都能看到"还在录";
  - 找回入口:点击回宿主页;
  - 显式停止:停止按钮调用对应 runtime.stop()(会议正式收尾落库;笔记产物
    暂存 pendingResult,回笔记页可补建语音草稿)。

  宿主页自身有完整录音 UI,指示条不重复显示。
-->
<template>
  <div v-if="visible" class="rec-pill" role="status" aria-live="polite">
    <button class="rec-go" type="button" @click="goHost">
      <span class="rec-dot" aria-hidden="true"></span>
      <span class="rec-label">{{ label }}</span>
      <span class="rec-clock">{{ clock }}</span>
    </button>
    <button class="rec-stop" type="button" aria-label="停止录音" @click="onStop">
      <span class="material-symbols-outlined" aria-hidden="true">stop_circle</span>
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useToast } from '../composables/useToast'
import {
  meetingRecorderRuntime, noteRecorderRuntime, anyRecordingActive,
} from '../native/recordingRuntime'
import { formatRecordingClock } from '../features/notes/note-recording'

const route = useRoute()
const router = useRouter()
const toast = useToast()

const meetingActive = computed(() => meetingRecorderRuntime.isRecording.value)
const noteActive = computed(() => noteRecorderRuntime.recording.value)

// 宿主页自身有录音 UI;只在切离宿主页时显示指示条。
const onHostPage = computed(() => {
  const p = route.path
  if (meetingActive.value) return p.startsWith('/meetings/') || p.startsWith('/sessions/')
  if (noteActive.value) return p.startsWith('/notes')
  return false
})
const visible = computed(() => anyRecordingActive() && !onHostPage.value)

const label = computed(() => (meetingActive.value ? '会议录音中' : '笔记录音中'))
const clock = computed(() => {
  if (meetingActive.value) return meetingRecorderRuntime.formatElapsed()
  return formatRecordingClock(noteRecorderRuntime.elapsedMs.value)
})

function goHost() {
  if (meetingActive.value) {
    const id = meetingRecorderRuntime.activeMeetingId.value
    router.push(id ? `/meetings/${id}` : '/meetings')
  } else {
    router.push('/notes')
  }
}

async function onStop() {
  if (meetingActive.value) {
    await meetingRecorderRuntime.stop()
    toast.success('会议录音已结束并保存')
  } else {
    await noteRecorderRuntime.stop()
    toast.success('笔记录音已结束，回笔记页可拾取语音草稿')
  }
}
</script>

<style scoped>
.rec-pill {
  position: fixed;
  right: var(--space-4, 16px);
  bottom: calc(var(--app-safe-bottom, 12px) + 72px);
  z-index: var(--z-fab, 40);
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 4px 6px 4px 12px;
  border-radius: 999px;
  border: 1px solid var(--border);
  background: var(--bg-card);
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.18);
}
.rec-go {
  display: flex;
  align-items: center;
  gap: 8px;
  border: none;
  background: transparent;
  color: var(--text-primary);
  font-size: 13px;
  padding: 6px 4px;
}
.rec-dot {
  width: 8px;
  height: 8px;
  border-radius: 999px;
  background: var(--danger, #e5484d);
  animation: rec-pulse 1.6s ease-in-out infinite;
}
@keyframes rec-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.35; }
}
.rec-clock {
  font-variant-numeric: tabular-nums;
  color: var(--text-secondary);
}
.rec-stop {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border-radius: 999px;
  border: none;
  background: transparent;
  color: var(--danger, #e5484d);
}
.rec-stop .material-symbols-outlined { font-size: 22px; }
</style>
