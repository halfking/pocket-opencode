<template>
  <HeaderActionsPortal>
    <WaveformVisualizer
      v-if="recording"
      :is-recording="true"
      :width="88"
      :height="28"
      color="var(--danger)"
      :show-time="false"
      :show-progress="false"
    />
    <button type="button" class="hdr" :disabled="summarizeDisabled" @click="$emit('summarize')">
      {{ summarizing ? '总结中' : '总结' }}
    </button>
    <button type="button" class="hdr icon" aria-label="更多操作" @click="open = true">
      <span class="material-symbols-outlined">more_vert</span>
    </button>
  </HeaderActionsPortal>

  <BottomSheet :open="open" title="会议操作" @close="open = false">
    <div class="ops">
      <button
        v-for="item in items"
        :key="item.id"
        type="button"
        class="op"
        :class="{ danger: item.danger }"
        :disabled="item.id === 'dispatch-acc' && !canDispatch"
        @click="onPick(item.id)"
      >{{ item.label }}</button>
    </div>
  </BottomSheet>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { BottomSheet, WaveformVisualizer } from '@/components'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'
import { studioMenuItems, type MeetingStudioAction } from './meeting-page-actions'

const props = defineProps<{
  archived: boolean
  recording: boolean
  summarizing: boolean
  summarizeDisabled: boolean
  canDispatch: boolean
}>()

const emit = defineEmits<{
  summarize: []
  action: [id: MeetingStudioAction]
}>()

const open = ref(false)
const items = computed(() => studioMenuItems({ archivedAt: props.archived ? 1 : null }))

function onPick(id: MeetingStudioAction) {
  open.value = false
  emit('action', id)
}
</script>

<style scoped>
.ops { display: flex; flex-direction: column; padding: 0 0 var(--space-3); }
.op {
  width: 100%; min-height: 48px; padding: 0 16px; border: none; border-bottom: 1px solid var(--border-subtle);
  background: transparent; color: var(--text-primary); font-size: 15px; text-align: left;
}
.op.danger { color: var(--danger); }
.op:disabled { opacity: 0.45; }
.hdr {
  min-height: 36px; padding: 0 10px; border: none; background: transparent;
  color: var(--brand-primary); font-weight: 600; font-size: 13px;
}
.hdr.icon { width: 44px; }
.hdr:disabled { opacity: 0.45; }
</style>
