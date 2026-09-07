<script setup lang="ts">
export type SessionLane = 'current' | 'historical' | 'local'

export interface TaskSessionRow {
  id: string
  title: string
  lane: SessionLane
  agentKind?: string
  agentSessionId?: string
  gwSessionId?: string
  instanceId?: string
  role?: string
  startedAt?: string
  endedAt?: string
  tokensIn: number
  tokensOut: number
  tokensCache: number | null
}

defineProps<{
  current: TaskSessionRow[]
  historical: TaskSessionRow[]
  localOnly: TaskSessionRow[]
  usage: { input: number; output: number; cache: number | null }
  loading?: boolean
}>()

const emit = defineEmits<{
  open: [row: TaskSessionRow]
  attach: []
}>()

function tokenLine(row: TaskSessionRow): string {
  const cache = row.tokensCache == null ? '?' : String(row.tokensCache)
  return `in ${row.tokensIn} · cache ${cache} · out ${row.tokensOut}`
}
</script>

<template>
  <div class="task-session-panel">
    <div class="usage" v-if="usage.input || usage.output">
      用量 in {{ usage.input }} / out {{ usage.output }}
      <span v-if="usage.cache != null"> / cache {{ usage.cache }}</span>
    </div>

    <section v-for="group in [
      { key: 'current', title: '当前', rows: current },
      { key: 'historical', title: '历史', rows: historical },
      { key: 'local', title: '本地附加', rows: localOnly },
    ]" :key="group.key" class="lane">
      <h3>{{ group.title }} <span class="badge">{{ group.rows.length }}</span></h3>
      <button v-if="group.key === 'current'" class="link-btn" type="button" @click="emit('attach')">+ 附加</button>
      <p v-if="!group.rows.length" class="empty">暂无</p>
      <button
        v-for="row in group.rows"
        :key="row.id"
        class="session-row"
        type="button"
        @click="emit('open', row)"
      >
        <div class="title">{{ row.title }}</div>
        <div class="meta">
          <span v-if="row.agentKind">{{ row.agentKind }}</span>
          <span>{{ tokenLine(row) }}</span>
        </div>
      </button>
    </section>
  </div>
</template>

<style scoped>
.task-session-panel { display: flex; flex-direction: column; gap: 12px; }
.usage { font-size: 12px; color: var(--text-secondary, #666); }
.lane h3 { margin: 0 0 8px; font-size: 14px; display: inline-block; }
.badge { font-weight: 400; opacity: 0.7; }
.link-btn { float: right; background: none; border: 0; color: var(--accent, #3b82f6); }
.empty { font-size: 13px; color: var(--text-secondary, #888); margin: 0; }
.session-row {
  display: block; width: 100%; text-align: left;
  padding: 10px 0; border: 0; border-bottom: 1px solid var(--border, #eee);
  background: transparent; color: inherit;
}
.title { font-size: 14px; }
.meta { font-size: 12px; color: var(--text-secondary, #888); display: flex; gap: 8px; }
</style>
