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
      <ProgressRing
        :value="ringValue"
        :size="22"
        :stroke="2"
        :duration="0"
        :linecap="'round'"
        :color="'var(--danger, #e5484d)'"
        class="rec-ring"
        aria-hidden="true"
      />
      <span class="rec-dot" aria-hidden="true"></span>
      <span class="rec-label">{{ label }}</span>
      <span class="rec-clock">{{ clock }}</span>
      <AnimatedNumber
        class="rec-secs"
        :value="secondsOnly"
        unit="s"
        :duration="380"
      />
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
import ProgressRing from './base/ProgressRing.vue'
import AnimatedNumber from './base/AnimatedNumber.vue'
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

/** 录音已用秒（仅秒整数部分）。喂给 ProgressRing 的进度与 AnimatedNumber 的连续动画。 */
const secondsOnly = computed(() => {
  if (meetingActive.value) return Math.floor((meetingRecorderRuntime.elapsedMs.value ?? 0) / 1000)
  return Math.floor((noteRecorderRuntime.elapsedMs.value ?? 0) / 1000)
})

/**
 * 环状进度：60s 一循环的视觉心跳。任何录音很少超过十几分钟，本地循环足够
 * 标示「还在录」；mm:ss 文本独立承担精确读数职责。
 */
const ringValue = computed(() => {
  const secs = secondsOnly.value
  return ((secs % 60) / 60) * 100
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
  // ⚠⚠ §834/D1：此处此前**没有** try/catch，而 `toast.success('…已结束并保存')`
  //   是**无条件**的 ⇒ 两类失败都让用户面对「点了完全没反应」：
  //     ① stop() 抛错（收尾写库失败）⇒ 连成功提示都不会出现；
  //     ② stop() 正常返回但 finalizePersistFailed 为真 ⇒ **成功提示照弹**，
  //        而库里那条会议根本没被写成 completed ⇒ 那句话在撒谎。
  //   ⇒ 失败必须变成一句人话，且**成功提示不得再无条件宣告已保存**。
  try {
    if (meetingActive.value) {
      await meetingRecorderRuntime.stop()
      // ⚠ §834/D1：写库失败时 runtime **自己**已经 toast 过一次（它是唯一知道
      //   细节的那一方），这里只负责**不再宣告成功** —— 那句「已结束并保存」
      //   在库里没写成 completed 时是撒谎。
      if (!meetingRecorderRuntime.lastFinalizePersistFailed) {
        toast.success('会议录音已结束并保存')
      }
    } else {
      await noteRecorderRuntime.stop()
      toast.success('笔记录音已结束，回笔记页可拾取语音草稿')
    }
  } catch (e) {
    // 兜底：笔记侧的 stop() 里已逐处包过（§832/§833），这里仍留着，
    // 因为「UI 边界必须自己说真话」不能依赖下游永远不抛。
    console.error('[recording-pill] 停止录音失败：', e)
    toast.error('停止录音时出错，请检查后重试（已录的内容仍在本地）')
  }
}
</script>

<style scoped>
.rec-pill {
  position: fixed;
  right: var(--space-4, 16px);
  /* 2026-10-06：原 calc(var(--app-safe-bottom,12px) + 72px) 里的 72 是手工
     复刻「底栏 56 + 间隙」⇒ 与 token 脱钩。改用 chrome token（实测位移 2px）。 */
  bottom: calc(var(--bottom-chrome-height) + var(--space-4));
  z-index: var(--z-fab);
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
  font-size: var(--text-smd);
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
  margin-right: 6px;
}

.rec-ring {
  flex-shrink: 0;
  /* 占位优先保证 mm:ss 文字不会因 ring 切换位置跳动 */
  width: 22px;
  height: 22px;
}

.rec-secs {
  font-size: var(--text-2xs);
  opacity: 0.7;
  margin-left: 6px;
  color: var(--danger, #e5484d);
  font-variant-numeric: tabular-nums;
  /* micro-pulse 持续视觉心跳 —— 数字每秒变一次像心跳 */
  min-width: 32px;
  text-align: right;
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
