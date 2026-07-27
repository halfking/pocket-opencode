<template>
  <div class="mic-status" :class="`mic-${state}`">
    <span class="mic-dot" aria-hidden="true"></span>
    <span>{{ label }}</span>
    <button v-if="state === 'denied'" class="mic-action" @click="$emit('settings')">去设置</button>
    <button v-else-if="state !== 'granted'" class="mic-action" @click="$emit('retry')">检查权限</button>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { MicState } from '../composables/useMicPermission'

const props = defineProps<{ state: MicState }>()
defineEmits<{ (event: 'settings'): void; (event: 'retry'): void }>()
const label = computed(() => ({
  unknown: '麦克风权限待确认',
  granted: '麦克风已就绪',
  denied: '麦克风权限被拒绝',
  unavailable: '麦克风不可用',
}[props.state]))
</script>

<style scoped>
.mic-status { display: flex; align-items: center; gap: 7px; margin: 0 0 12px; padding: 8px 10px; border-radius: 8px; background: var(--bg-card); color: var(--text-secondary); font-size: 12px; text-align: left; }
.mic-dot { width: 8px; height: 8px; border-radius: 50%; background: var(--text-muted); }
.mic-granted .mic-dot { background: #16a34a; }.mic-denied .mic-dot { background: var(--danger); }.mic-unavailable .mic-dot { background: #f59e0b; }
.mic-action { margin-left: auto; border: 0; background: transparent; color: var(--brand-primary); cursor: pointer; font-size: 12px; }
</style>
