<template>
  <aside class="insight" :aria-label="isRecording ? '即时总结' : '会议纪要'">
    <header class="insight-head">
      <span>{{ isRecording ? '即时总结' : '会议纪要' }}</span>
      <span v-if="isUpdating" class="muted">更新中…</span>
    </header>
    <div class="insight-body">
      <!--
        §220：这条必须**在正文之上**，且只在正文确实取自 summary.summary 时才说。

        ① 位置：§109 那条提示落在 references 附近，而正文在第 8 行 ——
           提示若排在正文**下面**，用户从上往下读时先看到「会议纪要 + 一段原文」，
           隔了三个空区块才读到「这不是摘要」。
        ② 条件：summaryText = finalSummary || summary?.summary。
           用户手改过稿子时正文是 finalSummary，**不是转写**，
           这时说「下面显示的是录音原文」就是错的（§30：宁可少说一句）。
        ③ 为什么必须显式声明：这一段退化有**三态**，不是两态 ——
           ①智能体 ②一次性 chat ③连降级链都失败。第三态此前与前两态
           **结构完全一样**（完整、200、四数组全空），
           界面只能按 agent 猜 ⇒ 猜错时正好说出最不可信的那句
           「已回落到一次性摘要」—— 那个一次性的正是失败者。
      -->
      <p v-if="summary && summary.summaryFallback === true && !finalSummary" class="muted-note">
        摘要没生成成功：智能总结与一次性摘要都没拿到结果。下面显示的是录音原文，不是摘要。
      </p>
      <p v-if="summaryText" class="summary">{{ summaryText }}</p>
      <p v-else class="muted">{{ emptyHint }}</p>
      <ul v-if="summary?.keyPoints?.length">
        <li v-for="(p, i) in summary.keyPoints" :key="i">{{ p }}</li>
      </ul>
      <!--
        §124：决策与待确认问题此前**从来没有**被渲染过。

        它们一路都在：提示词 schema 要求输出 → 服务端解析 →
        normalizeSummary → toLiveSummary → live_summary blob → LiveSummary 类型。
        只有界面没有。

        ⇒ 这正是 §41.5 那句「会议纪要三段式 ✅ 已对齐」骗人的地方：
        那一行只验了 **schema**（字段存不存在），没验**界面上看不看得到**。
        与 §119 同一类：产出存在 ≠ 用户能看到。
        决策是讯飞听见三段式（全文概要-决策结论-待办事项）里的中间一柱，
        缺它就只剩两柱。

        位置：决策紧跟关键点（都是「这次会定了什么」），
        待确认问题放在待办**之后** —— 它是未决项，不该和已定的待办混排。
      -->
      <section v-if="decisions.length" class="block">
        <div class="label">决策</div>
        <ul class="plain-list">
          <li v-for="(d, i) in decisions" :key="i">{{ d }}</li>
        </ul>
      </section>
      <section v-if="todos.length" class="block">
        <div class="label">待办</div>
        <div v-for="(a, i) in todos" :key="i" class="todo">
          <span>{{ a.text }}</span>
          <span class="todo-actions">
            <button type="button" @click="$emit('share-todo', a)">转交</button>
            <button type="button" @click="$emit('acc-todo', a)">ACC</button>
          </span>
        </div>
      </section>
      <section v-if="openQuestions.length" class="block">
        <div class="label">待确认问题</div>
        <ul class="plain-list">
          <li v-for="(q, i) in openQuestions" :key="i">{{ q }}</li>
        </ul>
      </section>
      <!--
        智能体检索到的参考资料（2026-10-06）。放在「相关推荐」**之前**：
        references 来自用户自己的笔记，是**有据可查**的；而下面的推荐在
        无 kxmemory 时是 LLM 兜底生成的搜索建议。两者可信度不同，
        摆放顺序不该让用户分不出差别。
      -->
      <!--
        §109：**降级时把「参考资料为什么没有」说出来**。

        `v-if="references.length"` 让这一整块在降级时**静默消失** ——
        而用户原话明确要「在总结同时，给出一些参考的资料与建议」。

        ⚠ 只有 `agent !== true` 才提示：**agent 跑过但没检索到你的笔记**
        是另一回事（那是真的没有），说成降级就是撒谎。
        与 §30 的不对称原则同源：宁可少说一句，不可说错一句。
      -->
      <!--
        ⚠ §220：**必须排除 summaryFallback 那一种**（提示本体在正文上方）。
        「已回落到一次性摘要」这句只在「一次性真的出了摘要」时准确；
        连降级链都失败时那个一次性的就是失败者，这句话会变成误导。
      -->
      <p
        v-if="summary && !references.length && summary.agent !== true && summary.summaryFallback !== true"
        class="muted-note"
      >
        本轮没有检索参考资料：智能总结路径未完成，已回落到一次性摘要。
      </p>
      <section v-if="references.length" class="block">
        <div class="label">参考资料 · 你写过的笔记</div>
        <button
          v-for="(ref, i) in references"
          :key="`ref-${ref.noteId}-${i}`"
          type="button"
          class="rec"
          @click="$emit('open-related', { type: 'note', id: ref.noteId, title: ref.title, snippet: ref.why, score: 0 })"
        >
          <span>📝 {{ ref.title }}</span>
          <span v-if="ref.why" class="muted">{{ ref.why }}</span>
        </button>
      </section>
      <!--
        §223：与 LiveSummaryPanel 同一句话、同一条件。
        这一块此前整块静默消失 ⇒ 用户需求里「给出参考资料与建议」那条
        在失败时被无声放弃，没有任何可观察的信号。

        ⛔ §563 勘误：条件里三个 `recommendFallback &&` 的第一个就已经卡死了。
          `recommendFallback` 只表示「模型有没有好好说话」，
          **不表示「有没有走主源」**（server_meeting.go:669）。
          ⭐ 而下面 :118 的小标题是「相关 · 笔记 / 知识库 / 网络」——
            在默认部署形态（kxmemory 未配）下，本块里的条目**全部**由模型现编，
            每条 `type:"web"`、url 是一条 Bing **搜索链接**（从没联网检索过），
            且 `parseRecommendJSONWithFallback` 只会产出 `type:"web"`
            ⇒ 标题里「笔记」「知识库」两种来源在该形态下**结构上不可能出现**。
          ⇒ 用户看到的是「网络」条目 + 搜索链接，而没有任何一句
            「这是模型的建议，不是检索到的资料」。
          ⛔ 补一句提示涉及产品文案口径，登记不代拍。
      -->
      <p v-if="recommendFallback && !references.length && !recommendations.length" class="muted-note">
        本轮没有拿到参考资料与建议（可以稍后再试一次）。
      </p>
      <section v-if="recommendations.length" class="block">
        <div class="label">相关 · 笔记 / 知识库 / 网络</div>
        <button
          v-for="rec in recommendations"
          :key="`${rec.type}:${rec.id}`"
          type="button"
          class="rec"
          @click="$emit('open-related', rec)"
        >
          <span>{{ typeIcon(rec.type) }} {{ rec.title }}</span>
          <span class="muted">{{ typeLabel(rec.type) }}</span>
        </button>
      </section>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { ActionItem, LiveSummary, RecommendItem } from './meetings-store'
import { draftsFromActionItems, type MeetingTodoDraft } from './meeting-todos'

const props = withDefaults(defineProps<{
  summary: LiveSummary | null
  finalSummary?: string | null
  recommendations?: RecommendItem[]
  /** §223：true = 最近一轮没拿到推荐（不是「没有建议」）。 */
  recommendFallback?: boolean
  isUpdating?: boolean
  isRecording?: boolean
}>(), {
  recommendations: () => [],
  isUpdating: false,
  isRecording: false,
  finalSummary: '',
})

defineEmits<{
  'share-todo': [draft: MeetingTodoDraft]
  'acc-todo': [draft: MeetingTodoDraft]
  'open-related': [item: RecommendItem]
}>()

const summaryText = computed(() => props.finalSummary || props.summary?.summary || '')
const todos = computed(() => draftsFromActionItems(props.summary?.actionItems as ActionItem[]))
/** 参考资料只在 agent 路径存在；没有时是 undefined 而不是空数组。 */
const references = computed(() => props.summary?.references ?? [])
// §124：这两个字段一路都在（提示词 schema → normalizeSummary → toLiveSummary
// → live_summary blob → LiveSummary 类型），只是此前没有任何界面读它们。
// 判据 live-summary-fields-rendered.test.ts 会盯着这件事：
// 「类型里声明了」不等于「用户看得到」，只验 schema 会漏掉这一整类。
const decisions = computed(() => props.summary?.decisions ?? [])
const openQuestions = computed(() => props.summary?.openQuestions ?? [])
const emptyHint = computed(() => (
  props.isRecording ? '转写开始后，右侧会滚动更新摘要，并从笔记、知识库和网络检索相关信息' : '点击顶栏「总结」生成当前纪要'
))

function typeIcon(type: string): string {
  const icons: Record<string, string> = {
    note: '📝', email: '📨', meeting: '🎙', contact: '👤', web: '🌐', knowledge: '📚',
  }
  return icons[type] ?? '💡'
}

function typeLabel(type: string): string {
  const labels: Record<string, string> = {
    note: '笔记', email: '邮件', meeting: '会议', contact: '联系人', web: '网络', knowledge: '知识库',
  }
  return labels[type] ?? type
}
</script>

<style scoped>
.insight {
  min-width: 0; min-height: 0; display: flex; flex-direction: column;
  background: var(--bg-card); border-left: 1px solid var(--border-subtle);
}
.insight-head {
  display: flex; justify-content: space-between; align-items: center;
  padding: 10px 12px; font-size: var(--text-smd); font-weight: 600; flex-shrink: 0;
}
.insight-body { flex: 1; overflow-y: auto; padding: 0 12px 12px; }
.summary { margin: 0 0 10px; font-size: var(--text-smd); line-height: 1.6; color: var(--text-primary); }
.muted { color: var(--text-muted); font-size: var(--text-sm); }
/* §109：降级提示。用 muted 不用警示色 —— 它不是失败，是「这次没走到那一步」。 */
.muted-note { margin: 0 0 var(--space-2); color: var(--text-muted); font-size: var(--text-2xs); line-height: 1.5; }
ul { margin: 0 0 10px; padding-left: 16px; font-size: var(--text-sm); color: var(--text-secondary); line-height: 1.7; }
.block { margin-top: 12px; }
.label { font-size: var(--text-2xs); color: var(--text-muted); margin-bottom: 6px; }
/* §124：决策 / 待确认问题用与关键点一致的朴素列表，不复用 .todo
   （那套带「转交 / ACC」按钮，只对行动项有意义）。 */
.plain-list { margin: 0; padding-left: 16px; font-size: var(--text-sm); color: var(--text-secondary); line-height: 1.7; }
.todo, .rec {
  display: flex; justify-content: space-between; gap: 8px; width: 100%;
  padding: 8px 0; border: none; border-bottom: 1px solid var(--border-subtle);
  background: transparent; color: var(--text-primary); font-size: var(--text-sm); text-align: left;
}
.todo-actions { display: flex; gap: 6px; flex-shrink: 0; }
.todo-actions button {
  border: 1px solid var(--border); border-radius: 999px; background: var(--bg-subtle);
  font-size: var(--text-2xs); padding: 2px 8px; color: var(--brand-primary);
}
</style>
