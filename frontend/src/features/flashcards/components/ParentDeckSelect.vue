<template>
  <button
    type="button"
    class="parent-select"
    :aria-label="t('flashcards.edit.parentDeck')"
    @click="open = true"
  >
    <span class="parent-icon" aria-hidden="true">
      <span class="material-symbols-outlined">folder</span>
    </span>
    <span class="parent-info">
      <span class="parent-label">{{ t('flashcards.edit.parentDeck') }}</span>
      <span class="parent-path">{{ displayPath }}</span>
    </span>
    <span class="material-symbols-outlined chev" aria-hidden="true">chevron_right</span>
  </button>

  <BottomSheet v-if="open" :model-value="open" placement="bottom" @update:model-value="(v) => (open = v)">
    <div class="picker">
      <h3 class="picker-title">{{ t('flashcards.edit.parentDeck') }}</h3>
      <ul class="picker-list">
        <li>
          <button
            type="button"
            class="picker-row top"
            :class="{ active: !modelValue }"
            @click="select('')"
          >
            <span class="picker-name">— {{ t('flashcards.edit.parentDeckNone') }} —</span>
          </button>
        </li>
        <li v-for="deck in treeOptions" :key="deck.deckId">
          <button
            type="button"
            class="picker-row"
            :class="{ active: modelValue === deck.deckId }"
            :disabled="deck.deckId === excludeId"
            @click="select(deck.deckId)"
          >
            <span class="picker-indent" :style="{ width: deck.depth * 16 + 'px' }" />
            <span class="picker-name">{{ deck.name }}</span>
            <span v-if="deck.deckId === excludeId" class="picker-note">{{ t('flashcards.edit.parentDeckSelf') }}</span>
          </button>
        </li>
      </ul>
    </div>
  </BottomSheet>
</template>

<script setup lang="ts">
/**
 * ParentDeckSelect —— 选父牌组的轻量级「按钮 + 底部 sheet 树形」。
 *
 * 树深度限制：3 层（避免无限嵌套导致 UI 烂掉）。
 *
 * 用法：v-model="parentDeckId: string | null"
 */
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import BottomSheet from '../../../components/base/BottomSheet.vue'
import type { FlashcardDeckConfig } from '../../../types/flashcards'

const props = defineProps<{
  modelValue: string | null | undefined
  /** 排除某个 deck（通常是被编辑的 deck 自己，避免自指环）。 */
  excludeId?: string
  decks: FlashcardDeckConfig[]
}>()
const emit = defineEmits<{
  (e: 'update:modelValue', v: string | null): void
}>()

const { t } = useI18n()
const open = ref(false)

interface TreeOpt { deckId: string; name: string; depth: number }

/**
 * 把扁平 decks 列表 → 树形渲染路径。
 * 简化算法：
 *   1. 找到所有 parentDeckId === null/'' 的根
 *   2. DFS 展开 children，indent 按深度加 1（最大 3 层）
 *   3. 不存在的 parentDeckId（孤儿卡）按顶级兜底
 */
const treeOptions = computed<TreeOpt[]>(() => {
  const decks = props.decks ?? []
  if (decks.length === 0) return []
  const byParent = new Map<string, FlashcardDeckConfig[]>()
  for (const d of decks) {
    const k = (d.parentDeckId ?? '').toString()
    const arr = byParent.get(k) ?? []
    arr.push(d)
    byParent.set(k, arr)
  }
  const out: TreeOpt[] = []
  const MAX_DEPTH = 3
  const walk = (parentId: string, depth: number) => {
    if (depth > MAX_DEPTH) return
    const children = (byParent.get(parentId) ?? []).slice().sort((a, b) => a.name.localeCompare(b.name))
    for (const c of children) {
      out.push({ deckId: c.deckId, name: c.name, depth: depth - 1 })
      walk(c.deckId, depth + 1)
    }
  }
  walk('', 1)
  return out
})

const displayPath = computed(() => {
  if (!props.modelValue) return t('flashcards.edit.parentDeckNone')
  const found = treeOptions.value.find((d) => d.deckId === props.modelValue)
  if (!found) return t('flashcards.edit.parentDeckNone')
  return found.name
})

function select(id: string) {
  emit('update:modelValue', id || null)
  open.value = false
}
</script>

<style scoped>
.parent-select {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  width: 100%;
  padding: var(--space-2) var(--space-3);
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  color: var(--text-primary);
  text-align: left;
  cursor: pointer;
  min-height: 44px;
}

.parent-icon {
  width: 28px;
  height: 28px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-sm);
  background: var(--brand-bg, rgba(76, 141, 255, 0.12));
  color: var(--brand-primary);
  flex-shrink: 0;
}

.parent-icon .material-symbols-outlined {
  font-size: 18px;
}

.parent-info {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
}

.parent-label {
  font-size: 11px;
  color: var(--text-tertiary, var(--text-muted));
  text-transform: uppercase;
  letter-spacing: 0.4px;
}

.parent-path {
  font-size: 14px;
  font-weight: var(--font-weight-medium);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.chev {
  font-size: 18px;
  color: var(--text-tertiary, var(--text-muted));
}

.picker {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3);
}

.picker-title {
  margin: 0;
  font-size: 15px;
  font-weight: var(--font-weight-semibold);
}

.picker-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  background: var(--bg-card);
  border-radius: var(--radius-sm);
  overflow: hidden;
}

.picker-row {
  display: flex;
  align-items: center;
  width: 100%;
  min-height: 44px;
  padding: var(--space-2) var(--space-3);
  background: transparent;
  border: none;
  border-bottom: 1px solid var(--border);
  color: var(--text-primary);
  text-align: left;
  cursor: pointer;
}

.picker-list li:last-child .picker-row { border-bottom: none; }

.picker-row.active {
  background: var(--brand-bg, rgba(76, 141, 255, 0.12));
  color: var(--brand-primary);
  font-weight: var(--font-weight-semibold);
}

.picker-row:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.picker-indent {
  display: inline-block;
  flex-shrink: 0;
}

.picker-name {
  flex: 1;
}

.picker-note {
  margin-left: var(--space-2);
  font-size: 11px;
  color: var(--text-tertiary);
}
</style>