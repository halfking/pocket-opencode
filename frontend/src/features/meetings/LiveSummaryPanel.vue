<template>
  <div
    class="summary-panel"
    :class="{ 'summary-panel--expanded': expanded }"
    @click="onPanelClick"
  >
    <div class="summary-header">
      <span class="summary-title">📋 实时摘要</span>
      <button
        type="button"
        class="summary-toggle"
        :aria-label="expanded ? '缩小' : '放大'"
        @click.stop="toggleExpand"
      >
        {{ expanded ? '⊖' : '⊕' }}
      </button>
    </div>

    <div v-if="isUpdating" class="summary-loading">更新中…</div>

    <div v-else class="summary-body">
      <p v-if="summary?.summary" class="summary-text">{{ summary.summary }}</p>
      <p v-else class="summary-empty">转写开始后摘要将在此显示</p>

      <ul v-if="summary?.keyPoints?.length" class="summary-list">
        <li v-for="(p, i) in summary.keyPoints" :key="i">{{ p }}</li>
      </ul>

      <div v-if="summary?.actionItems?.length" class="action-items">
        <div class="section-label">✅ 行动项</div>
        <div v-for="(a, i) in summary.actionItems" :key="i" class="action-chip">
          {{ a.text }}
          <span v-if="a.due" class="due">{{ a.due }}</span>
        </div>
      </div>

      <div v-if="recommendations.length" class="recommendations">
        <div class="section-label">💡 相关推荐</div>
        <div
          v-for="rec in recommendations.slice(0, 3)"
          :key="rec.id"
          class="rec-chip"
        >
          <span class="rec-type">{{ typeIcon(rec.type) }}</span>
          {{ rec.title }}
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import type { LiveSummary, RecommendItem } from './meetings-store'

withDefaults(defineProps<{
  summary: LiveSummary | null
  recommendations?: RecommendItem[]
  isUpdating?: boolean
}>(), {
  recommendations: () => [],
  isUpdating: false,
})

const expanded = ref(false)

function toggleExpand() {
  expanded.value = !expanded.value
}

function onPanelClick() {
  if (!expanded.value) expanded.value = true
}

function typeIcon(type: string): string {
  const icons: Record<string, string> = { note: '📝', email: '📨', meeting: '🎙', contact: '👤' }
  return icons[type] ?? '💡'
}
</script>

<style scoped>
.summary-panel {
  position: fixed;
  top: calc(var(--topbar-height, 48px) + var(--space-2));
  right: var(--space-2);
  width: 50vw;
  max-width: 360px;
  height: 50vh;
  max-height: 420px;
  background: rgba(var(--bg-card-rgb, 255, 255, 255), 0.85);
  backdrop-filter: blur(12px);
  -webkit-backdrop-filter: blur(12px);
  border: 1px solid var(--border-subtle, rgba(0,0,0,0.08));
  border-radius: var(--radius-lg, 12px);
  box-shadow: var(--shadow-md, 0 4px 16px rgba(0,0,0,0.12));
  display: flex;
  flex-direction: column;
  overflow: hidden;
  z-index: 100;
  transition: all 0.3s var(--ease-out, ease);
}

.summary-panel--expanded {
  top: 0;
  right: 0;
  width: 100vw;
  max-width: none;
  height: 100vh;
  max-height: none;
  border-radius: 0;
  z-index: 200;
}

.summary-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-3) var(--space-3) var(--space-2);
  border-bottom: 1px solid var(--border-subtle, rgba(0,0,0,0.06));
  flex-shrink: 0;
}

.summary-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--text-primary);
}

.summary-toggle {
  width: 32px;
  height: 32px;
  border: none;
  background: var(--bg-subtle);
  border-radius: var(--radius-sm);
  font-size: 18px;
  cursor: pointer;
  color: var(--text-secondary);
}

.summary-body {
  flex: 1;
  overflow-y: auto;
  padding: var(--space-3);
}

.summary-text {
  font-size: 13px;
  line-height: 1.6;
  color: var(--text-primary);
  margin: 0 0 var(--space-3);
}

.summary-empty {
  font-size: 13px;
  color: var(--text-muted);
  margin: 0;
}

.summary-list {
  margin: 0 0 var(--space-3);
  padding-left: var(--space-4);
  font-size: 12px;
  color: var(--text-secondary);
  line-height: 1.8;
}

.section-label {
  font-size: 11px;
  font-weight: 600;
  color: var(--text-muted);
  text-transform: uppercase;
  margin-bottom: var(--space-2);
}

.action-items, .recommendations {
  margin-top: var(--space-3);
}

.action-chip, .rec-chip {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  padding: 4px 10px;
  margin: 0 4px 4px 0;
  background: var(--bg-subtle);
  border-radius: var(--radius-full, 999px);
  font-size: 12px;
  color: var(--text-secondary);
}

.due {
  color: var(--brand-primary);
  font-size: 11px;
}

.summary-loading {
  padding: var(--space-4);
  font-size: 13px;
  color: var(--text-muted);
  text-align: center;
}
</style>
