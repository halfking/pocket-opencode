<script setup lang="ts">
/**
 * SessionDetailDrawer — 会话工作台头部「详情」抽屉（设计方案 v2 §4.3-3）。
 *
 * 收敛旧 features/opencode/SessionDetailView.vue 的能力：
 *   - 代码变更统计（+/- 行数、文件数）+ 消息数（旧页 4 格统计对齐）；
 *   - 导出 markdown（迁移 exportSummary，数据源从 mock session 换成
 *     round.completed 事件累计 / 消息流 diff 推导）。
 *
 * 形态：BottomSheet（复用 components/base/BottomSheet.vue，与 ApprovalBottomSheet
 * 同一基座；safe-area 处理由 BottomSheet 负责，不回退）。
 */
import { computed } from 'vue'
import BottomSheet from '../../components/base/BottomSheet.vue'
import { useToast } from '../../composables/useToast'
import {
  buildSessionMarkdown,
  type RoundCompletedData,
  type SessionStats,
} from './useSessionEvents'

const props = defineProps<{
  visible: boolean
  sessionId: string
  sessionTitle: string
  stats: SessionStats
  /** 轮摘要导出素材：index 升序；data 为 null 表示该轮无 round.completed 事件。 */
  rounds: Array<{ index: number; data: RoundCompletedData | null; fallbackSummary: string }>
}>()

const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void
}>()

const toast = useToast()

const additionsPercentage = computed(() => {
  const total = props.stats.added + props.stats.removed
  if (total === 0) return 0
  return (props.stats.added / total) * 100
})

const deletionsPercentage = computed(() => {
  const total = props.stats.added + props.stats.removed
  if (total === 0) return 0
  return (props.stats.removed / total) * 100
})

function onVisibleChange(v: boolean): void {
  emit('update:visible', v)
}

/** 导出 markdown 并触发下载（迁移自旧 SessionDetailView.exportSummary）。 */
function exportMarkdown(): void {
  const content = buildSessionMarkdown({
    title: props.sessionTitle || props.sessionId,
    sessionId: props.sessionId,
    stats: props.stats,
    rounds: props.rounds,
  })
  try {
    const blob = new Blob([content], { type: 'text/markdown' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `session-${props.sessionId}.md`
    a.click()
    URL.revokeObjectURL(url)
    toast.success('已导出')
  } catch {
    toast.error('导出失败')
  }
}
</script>

<template>
  <BottomSheet
    :model-value="visible"
    title="会话详情"
    height="half"
    role="dialog"
    aria-modal="true"
    aria-label="会话详情"
    @update:model-value="onVisibleChange"
  >
    <!-- 代码变更统计（旧详情页 4 格统计对齐） -->
    <section class="stats-card">
      <h3 class="section-title">📊 代码变更统计</h3>
      <div class="stats-grid">
        <div class="stat-item additions">
          <div class="stat-value">+{{ stats.added }}</div>
          <div class="stat-label">新增行数</div>
        </div>
        <div class="stat-item deletions">
          <div class="stat-value">-{{ stats.removed }}</div>
          <div class="stat-label">删除行数</div>
        </div>
        <div class="stat-item files">
          <div class="stat-value">{{ stats.files }}</div>
          <div class="stat-label">修改文件</div>
        </div>
        <div class="stat-item messages">
          <div class="stat-value">{{ stats.messageCount }}</div>
          <div class="stat-label">消息数</div>
        </div>
      </div>
      <div class="change-bar">
        <div class="change-additions" :style="{ width: additionsPercentage + '%' }"></div>
        <div class="change-deletions" :style="{ width: deletionsPercentage + '%' }"></div>
      </div>
    </section>

    <!-- 轮次摘要（round.completed 事件；无事件的轮显示消息流推导的首行） -->
    <section class="rounds-card">
      <h3 class="section-title">📜 轮次摘要</h3>
      <p v-if="rounds.length === 0" class="empty-text">暂无记录</p>
      <ul v-else class="rounds-list">
        <li v-for="r in rounds" :key="r.index" class="round-row">
          <span class="round-no">轮 {{ r.index }}</span>
          <template v-if="r.data">
            <span class="round-dot" :class="'dot-' + r.data.status" aria-hidden="true"></span>
            <span class="round-changes">
              +{{ r.data.changes.added }}/-{{ r.data.changes.removed }} · {{ r.data.changes.files }} 文件
            </span>
            <span class="round-summary">{{ r.data.summary }}</span>
          </template>
          <span v-else class="round-summary muted">{{ r.fallbackSummary }}</span>
        </li>
      </ul>
    </section>

    <template #footer>
      <button type="button" class="export-btn" @click="exportMarkdown">
        导出 Markdown
      </button>
    </template>
  </BottomSheet>
</template>

<style scoped>
.section-title {
  font-size: var(--text-md);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
  margin: 0 0 var(--space-3);
}
.stats-card {
  margin-bottom: var(--space-4);
}
.stats-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: var(--space-2);
  margin-bottom: var(--space-3);
}
.stat-item {
  text-align: center;
  padding: var(--space-3) var(--space-2);
  border-radius: var(--radius-md);
  background: var(--bg-subtle);
}
.stat-value {
  font-size: var(--text-xl);
  font-weight: 700;
  margin-bottom: 2px;
  font-variant-numeric: tabular-nums;
}
.stat-item.additions .stat-value { color: var(--success); }
.stat-item.deletions .stat-value { color: var(--danger); }
.stat-item.files .stat-value,
.stat-item.messages .stat-value { color: var(--info); }
.stat-label {
  font-size: var(--text-xs);
  color: var(--text-secondary);
}
.change-bar {
  height: 6px;
  background: var(--bg-subtle);
  border-radius: var(--radius-full);
  display: flex;
  overflow: hidden;
}
.change-additions { background: var(--success); transition: width 0.3s; }
.change-deletions { background: var(--danger); transition: width 0.3s; }

.rounds-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}
.round-row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2);
  background: var(--bg-subtle);
  border-radius: var(--radius-md);
  font-size: var(--text-sm);
}
.round-no {
  flex: 0 0 auto;
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
}
.round-dot {
  flex: 0 0 auto;
  width: 8px;
  height: 8px;
  border-radius: 50%;
}
.round-dot.dot-completed { background: var(--success); }
.round-dot.dot-error { background: var(--danger); }
.round-dot.dot-cancelled { background: var(--warning, #f59e0b); }
.round-changes {
  flex: 0 0 auto;
  color: var(--text-secondary);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.round-summary {
  flex: 1 1 auto;
  min-width: 0;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.round-summary.muted { color: var(--text-secondary); }
.empty-text {
  color: var(--text-muted);
  font-size: var(--text-sm);
}

.export-btn {
  width: 100%;
  min-height: 44px; /* 触摸热区 ≥44px */
  padding: var(--space-2) var(--space-3);
  border: none;
  border-radius: var(--radius-md);
  background: var(--brand-primary);
  color: var(--text-inverse);
  font-size: var(--text-base);
  font-weight: var(--font-weight-semibold);
}
.export-btn:active {
  transform: scale(0.98);
}
</style>
