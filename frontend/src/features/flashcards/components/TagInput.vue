<template>
  <div class="tag-input" :class="{ focused }">
    <span
      v-for="tag in modelValue"
      :key="tag"
      class="chip"
    >
      <span class="chip-text">{{ tag }}</span>
      <button
        type="button"
        class="chip-x"
        :aria-label="`remove ${tag}`"
        @click="removeTag(tag)"
      >
        <span class="material-symbols-outlined" aria-hidden="true">close</span>
      </button>
    </span>
    <input
      v-model="draft"
      type="text"
      class="input"
      :placeholder="placeholder"
      @focus="focused = true"
      @blur="onBlur"
      @keydown.enter.prevent="commit"
      @keydown="onKeydown"
      @paste="onPaste"
    />
  </div>
</template>

<script setup lang="ts">
/**
 * TagInput —— Anki 风格的标签 chip 输入。
 *
 * 用法：v-model="tags: string[]"
 *   - Enter / 逗号 / 空格 → 把 draft 提交为新 tag
 *   - 粘贴 "tag1, tag2 tag3" → 自动切分
 *   - Backspace 且 draft 为空 → 删除最后一个 tag（Anki 行为）
 *   - tag 已存在则忽略（去重）
 *   - tag 自动 trim；空字符串忽略
 */
import { ref } from 'vue'

const props = withDefaults(
  defineProps<{
    modelValue: string[]
    placeholder?: string
  }>(),
  {
    placeholder: '输入标签后回车',
  },
)
const emit = defineEmits<{
  (e: 'update:modelValue', v: string[]): void
}>()

const draft = ref('')
const focused = ref(false)

function commit() {
  const raw = draft.value
  if (!raw) return
  const parts = raw.split(/[,\s]+/).map((s) => s.trim()).filter(Boolean)
  const next = [...props.modelValue]
  let changed = false
  for (const t of parts) {
    if (!next.includes(t)) {
      next.push(t)
      changed = true
    }
  }
  if (changed) emit('update:modelValue', next)
  draft.value = ''
}

function removeTag(tag: string) {
  emit(
    'update:modelValue',
    props.modelValue.filter((t) => t !== tag),
  )
}

function onBlur() {
  focused.value = false
  commit()
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Backspace' && draft.value === '' && props.modelValue.length > 0) {
    e.preventDefault()
    const next = [...props.modelValue]
    next.pop()
    emit('update:modelValue', next)
  }
}

function onPaste(e: ClipboardEvent) {
  const text = e.clipboardData?.getData('text') ?? ''
  if (/[,\s]/.test(text)) {
    e.preventDefault()
    draft.value = draft.value + text
    commit()
  }
}
</script>

<style scoped>
.tag-input {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1);
  width: 100%;
  min-height: 44px;
  padding: 6px 8px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--bg-card);
  transition: border-color var(--duration-fast) var(--ease-out);
}

.tag-input.focused {
  border-color: var(--brand-primary);
}

.chip {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  padding: 2px 4px 2px 10px;
  background: var(--brand-bg, rgba(76, 141, 255, 0.12));
  color: var(--brand-primary, #4c8dff);
  border-radius: 999px;
  font-size: 12px;
  font-weight: var(--font-weight-medium);
  line-height: 1.4;
}

.chip-text {
  max-width: 180px;
  text-overflow: ellipsis;
  overflow: hidden;
  white-space: nowrap;
}

.chip-x {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  background: transparent;
  border: none;
  border-radius: 50%;
  color: var(--brand-primary);
  cursor: pointer;
  padding: 0;
}

.chip-x .material-symbols-outlined {
  font-size: 14px;
}

.chip-x:hover {
  background: rgba(76, 141, 255, 0.18);
}

.input {
  flex: 1;
  min-width: 80px;
  border: none;
  outline: none;
  background: transparent;
  color: var(--text-primary);
  font: inherit;
  font-size: 14px;
  padding: 4px 6px;
}
</style>