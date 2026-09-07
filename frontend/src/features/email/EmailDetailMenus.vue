<template>
  <BottomSheet v-model="langOpen" title="切换语言" height="auto" aria-label="切换邮件语言">
    <button
      v-for="opt in EMAIL_LANGS"
      :key="opt.code"
      type="button"
      class="sheet-item"
      :class="{ on: lang === opt.code }"
      @click="emit('chooseLang', opt.code)"
    >{{ opt.label }}</button>
  </BottomSheet>

  <BottomSheet v-model="moreOpen" title="邮件操作" height="auto" aria-label="更多邮件操作">
    <button type="button" class="sheet-item" @click="emit('forward')">转发</button>
    <button type="button" class="sheet-item" :disabled="converting" @click="emit('todo')">转 Todo</button>
    <button type="button" class="sheet-item" @click="emit('star')">
      {{ starred ? '取消星标' : '加星' }}
    </button>
    <button type="button" class="sheet-item" @click="emit('read')">
      {{ isRead ? '标为未读' : '标为已读' }}
    </button>
  </BottomSheet>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { BottomSheet } from '../../components'
import { EMAIL_LANGS, type EmailLang } from './translate-email'

const props = defineProps<{
  lang: EmailLang
  langOpen: boolean
  moreOpen: boolean
  starred: boolean
  isRead: boolean
  converting: boolean
}>()

const emit = defineEmits<{
  'update:langOpen': [v: boolean]
  'update:moreOpen': [v: boolean]
  chooseLang: [lang: EmailLang]
  forward: []
  todo: []
  star: []
  read: []
}>()

const langOpen = computed({
  get: () => props.langOpen,
  set: (v) => emit('update:langOpen', v),
})
const moreOpen = computed({
  get: () => props.moreOpen,
  set: (v) => emit('update:moreOpen', v),
})
</script>

<style scoped>
.sheet-item {
  display: block;
  width: 100%;
  text-align: left;
  padding: 12px 4px;
  border: none;
  background: transparent;
  color: var(--text-primary);
  font-size: 15px;
  cursor: pointer;
}
.sheet-item.on { color: var(--brand-primary); font-weight: 600; }
</style>
