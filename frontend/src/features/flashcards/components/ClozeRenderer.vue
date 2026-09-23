<template>
  <span class="cloze">
    <template v-for="(seg, i) in segments" :key="i">
      <span v-if="seg.type === 'text'">{{ seg.text }}</span>
      <span
        v-else
        class="occlusion"
        :class="{ revealed, hinted: seg.hint && !revealed }"
        :title="seg.hint || undefined"
      >{{ revealed ? seg.answer : '⋯' }}</span>
    </template>
  </span>
</template>

<script setup lang="ts">
/**
 * ClozeRenderer —— 把 cloze 文本拆成段，逐段渲染。
 *
 * 行为：
 *   - revealed=false（默认）→ 挖空显示 ⋯ 提示「这里有答案」
 *   - revealed=true         → 显示原文答案
 *   - hint 存在且未揭示     → title 属性给悬浮/长按提示（Anki 行为）
 *
 * 安全：所有答案走 v-if 直接渲染文本节点，不用 v-html，避免 XSS。
 */
import { computed } from 'vue'
import { renderCloze } from '../utils/cloze'

const props = defineProps<{
  text: string
  revealed: boolean
}>()

const segments = computed(() => renderCloze(props.text).segments)
</script>

<style scoped>
.cloze {
  display: inline;
  line-height: 1.6;
  font-size: 17px;
  text-align: left;
  word-wrap: break-word;
}

.occlusion {
  display: inline-block;
  min-width: 1.5em;
  padding: 0 6px;
  margin: 0 1px;
  border-radius: 4px;
  background: var(--bg-subtle);
  color: var(--brand-primary);
  font-weight: var(--font-weight-semibold);
  text-align: center;
  transition: background 0.2s ease;
}

.occlusion.hinted {
  border: 1px dashed var(--brand-primary);
  background: transparent;
}

.occlusion.revealed {
  background: var(--success-bg, rgba(34, 197, 94, 0.12));
  color: var(--success, #16a34a);
}
</style>