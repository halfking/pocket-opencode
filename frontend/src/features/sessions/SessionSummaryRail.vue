<script setup lang="ts">
/**
 * SessionSummaryRail — 会话请求列表右缘的总结色条（2026-09-08 会话详情改版）。
 *
 * 收起态：贴窗口右缘、与内容区等高的浅黄「纸面」窄条（便签质感），
 * 竖排「总结」+ 图标；点击向左滑出总结面板：
 *   - 会话统计（轮数 / 消息数 / ±行数）；
 *   - 各轮摘要（round.completed 事件结论，降级为消息流推导首行），
 *     点击任意一轮跳转到该轮（emit seek）并收起面板。
 */
import { ref } from 'vue'
import type { RoundCompletedData, SessionStats } from './useSessionEvents'

const props = defineProps<{
  stats: SessionStats
  /** 轮摘要：data 为 null 表示该轮无 round.completed 事件（与详情抽屉同源）。 */
  rounds: Array<{ index: number; data: RoundCompletedData | null; fallbackSummary: string }>
}>()

const emit = defineEmits<{ (e: 'seek', index: number): void }>()

const open = ref(false)

function summaryOf(r: { data: RoundCompletedData | null; fallbackSummary: string }): string {
  return (r.data ? r.data.summary : r.fallbackSummary) || '暂无摘要'
}

function dotClass(r: { data: RoundCompletedData | null }): string {
  if (!r.data) return 'dot-none'
  if (r.data.status === 'completed') return 'dot-completed'
  if (r.data.status === 'error') return 'dot-error'
  return 'dot-cancelled'
}

function jump(index: number): void {
  open.value = false
  emit('seek', index)
}
</script>

<template>
  <div class="summary-root">
    <!-- 展开面板（渲染在前，条收起后面板从右滑出） -->
    <Transition name="summary-slide">
      <aside v-if="open" class="summary-panel" aria-label="会话总结">
        <header class="panel-head">
          <span class="material-symbols-outlined head-icon" aria-hidden="true">summarize</span>
          <h3 class="panel-title">会话总结</h3>
          <button type="button" class="panel-close" aria-label="收起会话总结" @click="open = false">
            <span class="material-symbols-outlined" aria-hidden="true">close</span>
          </button>
        </header>

        <div class="panel-stats">
          <div class="pstat">
            <b>{{ rounds.length }}</b><span>轮次</span>
          </div>
          <div class="pstat">
            <b>{{ stats.messageCount }}</b><span>消息</span>
          </div>
          <div class="pstat add">
            <b>+{{ stats.added }}</b><span>新增行</span>
          </div>
          <div class="pstat del">
            <b>-{{ stats.removed }}</b><span>删除行</span>
          </div>
        </div>

        <div class="panel-list">
          <button
            v-for="r in rounds"
            :key="r.index"
            type="button"
            class="sum-row"
            :aria-label="`跳到第 ${r.index} 轮`"
            @click="jump(r.index)"
          >
            <span class="sum-no">轮 {{ r.index }}</span>
            <span class="sum-dot" :class="dotClass(r)" aria-hidden="true"></span>
            <span class="sum-text">{{ summaryOf(r) }}</span>
            <span v-if="r.data" class="sum-changes">
              +{{ r.data.changes.added }}/-{{ r.data.changes.removed }}
            </span>
          </button>
          <p v-if="rounds.length === 0" class="sum-empty">暂无总结，开始对话后自动生成。</p>
        </div>
      </aside>
    </Transition>

    <!-- 收起态：浅黄纸面色条 -->
    <button
      v-if="!open"
      type="button"
      class="summary-rail"
      aria-label="展开会话总结"
      :aria-expanded="false"
      @click="open = true"
    >
      <span class="material-symbols-outlined rail-icon" aria-hidden="true">summarize</span>
      <span class="rail-text">总结</span>
    </button>
  </div>
</template>

<style scoped>
.summary-root {
  position: relative;
  flex: 0 0 auto;
  align-self: stretch;
  display: flex;
}

/* ── 收起态：纸面色条（便签质感，明暗主题下都保持纸面观感） ── */
.summary-rail {
  flex: 0 0 auto;
  align-self: stretch;
  width: 22px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: flex-start;
  gap: 6px;
  padding: var(--space-2) 0;
  border: none;
  border-left: 1px solid rgba(180, 155, 60, 0.35);
  background: linear-gradient(180deg, #fbf3d9 0%, #f7edd0 100%);
  color: #8a6d1f;
  cursor: pointer;
}
.summary-rail:active {
  background: linear-gradient(180deg, #f5eadc 0%, #f0e2bc 100%);
}
.rail-icon {
  font-size: 15px;
}
.rail-text {
  writing-mode: vertical-rl;
  font-size: 11px;
  font-weight: var(--font-weight-semibold);
  letter-spacing: 3px;
}

/* ── 展开态：右缘滑出面板（同款纸面） ── */
.summary-panel {
  position: absolute;
  top: 0;
  right: 0;
  bottom: 0;
  z-index: var(--z-popover, 30);
  width: min(320px, 82vw);
  display: flex;
  flex-direction: column;
  background: linear-gradient(180deg, #fdf8e7 0%, #faf3d9 100%);
  border-left: 1px solid rgba(180, 155, 60, 0.35);
  box-shadow: -8px 0 24px rgba(60, 48, 10, 0.12);
}

.summary-slide-enter-active,
.summary-slide-leave-active {
  transition: transform var(--duration-base, 0.2s) var(--ease-out);
}
.summary-slide-enter-from,
.summary-slide-leave-to {
  transform: translateX(100%);
}

.panel-head {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border-bottom: 1px solid rgba(180, 155, 60, 0.28);
  color: #6d5514;
}
.head-icon {
  font-size: 18px;
}
.panel-title {
  flex: 1 1 auto;
  margin: 0;
  font-size: var(--text-md);
  font-weight: var(--font-weight-semibold);
  color: #55430f;
}
.panel-close {
  flex: 0 0 auto;
  width: 44px;
  height: 44px;
  margin-right: -8px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: none;
  background: transparent;
  color: #8a6d1f;
  border-radius: var(--radius-full);
  cursor: pointer;
}
.panel-close:active {
  background: rgba(180, 155, 60, 0.18);
}

.panel-stats {
  flex: 0 0 auto;
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: var(--space-2);
  padding: var(--space-3);
}
.pstat {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 2px;
  padding: var(--space-2) 0;
  border-radius: var(--radius-md);
  background: rgba(255, 255, 255, 0.55);
  border: 1px solid rgba(180, 155, 60, 0.22);
}
.pstat b {
  font-size: var(--text-md);
  font-weight: 700;
  color: #55430f;
  font-variant-numeric: tabular-nums;
}
.pstat.add b {
  color: var(--success);
}
.pstat.del b {
  color: var(--danger);
}
.pstat span {
  font-size: var(--text-xs);
  color: #8a6d1f;
}

.panel-list {
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto;
  -webkit-overflow-scrolling: touch;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: 0 var(--space-3) var(--space-3);
}
.sum-row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  min-height: 44px;
  padding: var(--space-2);
  text-align: left;
  border: 1px solid rgba(180, 155, 60, 0.22);
  border-radius: var(--radius-md);
  background: rgba(255, 255, 255, 0.55);
  cursor: pointer;
}
.sum-row:active {
  background: rgba(255, 255, 255, 0.85);
}
.sum-no {
  flex: 0 0 auto;
  font-size: var(--text-xs);
  font-weight: var(--font-weight-semibold);
  color: #6d5514;
}
.sum-dot {
  flex: 0 0 auto;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--text-muted);
}
.sum-dot.dot-completed {
  background: var(--success);
}
.sum-dot.dot-error {
  background: var(--danger);
}
.sum-dot.dot-cancelled {
  background: var(--warning, #f59e0b);
}
.sum-text {
  flex: 1 1 auto;
  min-width: 0;
  font-size: var(--text-sm);
  line-height: 1.4;
  color: #4a3c10;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.sum-changes {
  flex: 0 0 auto;
  font-size: var(--text-xs);
  color: #8a6d1f;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.sum-empty {
  margin: 0;
  padding: var(--space-4);
  text-align: center;
  font-size: var(--text-sm);
  color: #8a6d1f;
}
</style>
