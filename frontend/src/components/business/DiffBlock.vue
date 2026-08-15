<script setup lang="ts">
/**
 * DiffBlock — unified diff 分段审查视图（P2 E5-S3）。
 *
 * 每个 @@ hunk 是独立折叠单元；仅首段默认展开。大 diff 下，折叠段不挂载
 * 行级 DOM，避免 5,000 行输入一次性占满页面。文本使用插值输出，不走 v-html。
 */
import { computed, ref, watch } from 'vue'
import { parseUnifiedDiff } from '../../utils/diffParse'

const props = defineProps<{
  diff: string
}>()

const parsed = computed(() => parseUnifiedDiff(props.diff))
const MAX_INITIAL_LINES = 250
const expanded = ref<Set<number>>(new Set([0]))
const visibleCounts = ref<Map<number, number>>(new Map())

watch(
  () => props.diff,
  () => {
    expanded.value = new Set([0])
    visibleCounts.value = new Map()
  },
)

function toggle(index: number): void {
  const next = new Set(expanded.value)
  if (next.has(index)) next.delete(index)
  else next.add(index)
  expanded.value = next
}

function marker(type: 'context' | 'add' | 'del'): string {
  if (type === 'add') return '+'
  if (type === 'del') return '-'
  return ' '
}

function visibleCount(index: number, total: number): number {
  return visibleCounts.value.get(index) ?? Math.min(MAX_INITIAL_LINES, total)
}

function showMore(index: number, total: number): void {
  const next = new Map(visibleCounts.value)
  next.set(index, Math.min(total, visibleCount(index, total) + MAX_INITIAL_LINES))
  visibleCounts.value = next
}

function fileLabel(meta: string[]): string {
  const line = meta.find((item) => item.startsWith('diff --git '))
  return line ? line.replace(/^diff --git a\//, '').replace(/ b\/.*$/, '') : ''
}
</script>

<template>
  <section v-if="parsed" class="diff-block" aria-label="代码变更">
    <header class="diff-summary">
      <span class="material-symbols-outlined" aria-hidden="true">difference</span>
      <strong>{{ parsed.hunks.length }} 个变更段</strong>
      <span class="diff-add">+{{ parsed.adds }}</span>
      <span class="diff-del">-{{ parsed.dels }}</span>
      <span class="diff-lines">{{ parsed.totalLines }} 行</span>
    </header>

    <pre v-if="parsed.meta.length" class="diff-meta">{{ parsed.meta.filter(Boolean).join('\n') }}</pre>

    <div class="diff-hunks">
      <article v-for="(hunk, index) in parsed.hunks" :key="`${hunk.header}-${index}`" class="diff-hunk">
        <div v-if="hunk.fileMeta.length" class="hunk-file" :title="hunk.fileMeta.join('\n')">
          {{ fileLabel(hunk.fileMeta) || hunk.fileMeta[0] }}
        </div>
        <button
          type="button"
          class="hunk-toggle"
          :aria-expanded="expanded.has(index)"
          @click="toggle(index)"
        >
          <span class="material-symbols-outlined" aria-hidden="true">
            {{ expanded.has(index) ? 'expand_less' : 'expand_more' }}
          </span>
          <code>{{ hunk.header }}</code>
          <span class="hunk-counts">
            <span class="diff-add">+{{ hunk.adds }}</span>
            <span class="diff-del">-{{ hunk.dels }}</span>
          </span>
        </button>

        <pre v-if="expanded.has(index)" class="diff-lines-list"><code><span
          v-for="(line, lineIndex) in hunk.lines.slice(0, visibleCount(index, hunk.lines.length))"
          :key="lineIndex"
          class="diff-line"
          :class="`line-${line.type}`"
        ><span class="line-marker" aria-hidden="true">{{ marker(line.type) }}</span><span>{{ line.text }}</span></span></code></pre>
        <button
          v-if="expanded.has(index) && visibleCount(index, hunk.lines.length) < hunk.lines.length"
          type="button"
          class="show-more"
          @click="showMore(index, hunk.lines.length)"
        >
          继续显示（{{ visibleCount(index, hunk.lines.length) }}/{{ hunk.lines.length }} 行）
        </button>
      </article>
    </div>
  </section>
</template>

<style scoped>
.hunk-file {
  overflow: hidden;
  padding: 6px 10px 0;
  color: var(--text-secondary);
  font: var(--text-sm)/1.4 var(--font-mono);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.show-more {
  display: block;
  width: 100%;
  min-height: 48px;
  padding: 8px 12px;
  border: 0;
  border-top: 1px solid var(--border);
  background: var(--bg-subtle);
  color: var(--brand-primary);
  font-size: var(--text-base);
  cursor: pointer;
}

.diff-block {
  overflow: hidden;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--bg-card);
}

.diff-summary {
  display: flex;
  min-height: 48px;
  align-items: center;
  gap: 8px;
  padding: 6px 10px;
  border-bottom: 1px solid var(--border);
  font-size: var(--text-base);
}

.diff-summary .material-symbols-outlined {
  font-size: 19px;
}

.diff-add {
  color: var(--color-success);
  font-weight: 700;
}

.diff-del {
  color: var(--color-error);
  font-weight: 700;
}

.diff-lines {
  margin-left: auto;
  color: var(--text-secondary);
}

.diff-meta {
  overflow-x: auto;
  margin: 0;
  padding: 8px 10px;
  border-bottom: 1px solid var(--border);
  background: var(--bg-subtle);
  color: var(--text-secondary);
  font: var(--text-base)/1.5 var(--font-mono);
  white-space: pre;
}

.diff-hunks {
  display: grid;
}

.diff-hunk + .diff-hunk {
  border-top: 1px solid var(--border);
}

.hunk-toggle {
  display: grid;
  width: 100%;
  min-height: 48px;
  grid-template-columns: 24px minmax(0, 1fr) auto;
  align-items: center;
  gap: 6px;
  padding: 4px 8px;
  border: 0;
  background: var(--info-soft);
  color: var(--text-primary);
  text-align: left;
  cursor: pointer;
}

.hunk-toggle code {
  overflow: hidden;
  font: var(--text-base)/1.4 var(--font-mono);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.hunk-toggle .material-symbols-outlined {
  font-size: 20px;
}

.hunk-counts {
  display: flex;
  gap: 7px;
  font-size: var(--text-base);
}

.diff-lines-list {
  overflow: auto;
  max-height: 52vh;
  margin: 0;
  font: var(--text-base)/1.55 var(--font-mono);
  white-space: pre;
}

.diff-line {
  display: grid;
  min-width: max-content;
  grid-template-columns: 22px 1fr;
  padding-right: 12px;
}

.line-marker {
  user-select: none;
  text-align: center;
}

.line-add {
  background: var(--success-soft);
  color: var(--text-primary);
}

.line-del {
  background: var(--danger-soft);
  color: var(--text-primary);
}

.line-context {
  color: var(--text-primary);
}

@media (prefers-reduced-motion: reduce) {
  .hunk-toggle,
  .show-more {
    transition: none;
  }
}
</style>
