<!--
  列表页录音 FAB：点击切换开始/停止（不再长按）。

  busy = 录音已停止但兜底转写仍在跑（phase === 'stopping'）。那段时间最长 10 分钟，
  若不显式表达，界面表现为：点停止后 FAB 立刻变回「开始录音」外观、
  实时文本消失、再点没反应——也就是用户报的「点击停止无效」。
-->
<template>
  <div class="recorder-fab">
    <button
      class="fab"
      :class="{ recording, busy }"
      type="button"
      :aria-label="busy ? '正在转写录音，请稍候' : (recording ? '停止录音' : '开始录音')"
      :aria-pressed="recording"
      :aria-busy="busy"
      :disabled="busy"
      @click="$emit('toggle')"
    >
      <span class="fab-icon" aria-hidden="true">
        <span v-if="busy" class="fab-spinner" />
        <template v-else>{{ recording ? '⏹' : '🎤' }}</template>
      </span>
      <!-- 与 TasksView 的「快速提问」同一处 Android 桥接缺陷：按钮带
           aria-pressed ⇒ 被映射成 android.widget.ToggleButton，而该桥接
           丢掉纯 aria-label、只保留内容子节点的文字 ⇒ 名字变空串。
           2026-10-03 vivo V2436A / Android 16 实测。详见
           src/__tests__/aria-pressed-needs-text-node.test.mjs -->
      <span class="sr-only">{{ busy ? '正在转写录音，请稍候' : (recording ? '停止录音' : '开始录音') }}</span>
    </button>
    <div v-if="recording" class="pulse" aria-hidden="true" />
  </div>
</template>

<script setup lang="ts">
defineProps<{ recording: boolean; busy?: boolean }>()
defineEmits<{ toggle: [] }>()
</script>

<style scoped>
.recorder-fab {
  position: fixed;
  right: var(--space-5);
  bottom: calc(var(--bottom-chrome-height) + var(--space-4));
  z-index: var(--z-fab);
}
.fab {
  width: 60px;
  height: 60px;
  border-radius: 50%;
  border: none;
  background: var(--brand-gradient);
  color: var(--text-inverse);
  font-size: 24px;
  box-shadow: var(--shadow-lg);
  cursor: pointer;
}
.fab.recording { transform: scale(1.1); background: var(--danger); }
/* 收尾转写中：不可点 + 明显不同外观。上一版没有这个态，点停止后 FAB 立刻
   变回「开始录音」，再点又毫无反应，用户只能当成按钮坏了。 */
.fab.busy {
  background: var(--text-secondary);
  cursor: progress;
  opacity: 0.75;
}
.fab-spinner {
  display: block;
  width: 22px;
  height: 22px;
  border-radius: 50%;
  border: 2px solid currentColor;
  border-top-color: transparent;
  animation: fab-spin 0.9s linear infinite;
}
@keyframes fab-spin {
  to { transform: rotate(360deg); }
}
.pulse {
  position: absolute;
  inset: 0;
  border-radius: 50%;
  border: 2px solid var(--danger);
  animation: pulse var(--duration-slow) infinite;
  /* 动画层仅做视觉装饰,不挡 FAB 命中 — 否则点击录音按钮会被 pulse 拦截,
     用户感知为"按了没反应"。 */
  pointer-events: none;
  z-index: -1;
}
@keyframes pulse {
  0% { transform: scale(1); opacity: 0.8; }
  100% { transform: scale(1.8); opacity: 0; }
}
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}
</style>
