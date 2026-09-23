<template>
  <section class="page">
    <header class="head">
      <button type="button" class="back-btn" aria-label="back" @click="goBack">
        <span class="material-symbols-outlined">arrow_back</span>
      </button>
      <h1>{{ t('flashcards.io.title') }}</h1>
    </header>

    <main class="content">
      <!-- 导出 -->
      <section class="card">
        <h2>
          <span class="material-symbols-outlined" aria-hidden="true">file_upload</span>
          {{ t('flashcards.io.exportTitle') }}
        </h2>
        <p class="muted">{{ t('flashcards.io.exportDesc') }}</p>
        <dl class="stats">
          <div><dt>{{ t('flashcards.io.notes') }}</dt><dd>{{ counts.notes }}</dd></div>
          <div><dt>{{ t('flashcards.io.cards') }}</dt><dd>{{ counts.cards }}</dd></div>
          <div><dt>{{ t('flashcards.io.decks') }}</dt><dd>{{ counts.decks }}</dd></div>
        </dl>
        <button class="primary" type="button" :disabled="busy || counts.notes === 0" @click="onExport">
          <span class="material-symbols-outlined" aria-hidden="true">download</span>
          <span>{{ t('flashcards.io.exportBtn') }}</span>
        </button>
      </section>

      <!-- 导入 -->
      <section class="card">
        <h2>
          <span class="material-symbols-outlined" aria-hidden="true">file_download</span>
          {{ t('flashcards.io.importTitle') }}
        </h2>
        <p class="muted">{{ t('flashcards.io.importDesc') }}</p>
        <input
          ref="fileInputEl"
          type="file"
          accept="application/json"
          class="hidden-input"
          @change="onFileChange"
        />
        <button class="primary" type="button" :disabled="busy" @click="triggerFilePicker">
          <span class="material-symbols-outlined" aria-hidden="true">upload</span>
          <span>{{ t('flashcards.io.importBtn') }}</span>
        </button>
      </section>

      <p v-if="message" class="message" :class="messageKind" role="status">{{ message }}</p>
    </main>
  </section>
</template>

<script setup lang="ts">
/**
 * FlashcardImportExportView —— 卡片 JSON 导入 / 导出（Phase 6）。
 *
 * 路由：`/flashcards/io`
 *
 * 设计：
 *   - 导出：全量 notes + cards + deckConfigs + reviewLogs；通过 Share 面板分发。
 *   - 导入：从 File Picker 读 JSON，校验后合并到 store（不删除已有）。
 *
 * Phase 6.1 增量：选 deck 导入 / 单 deck 导出 / 冲突合并策略。
 */
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useFlashcardsStore } from '../../stores/flashcards'
import { buildExportBundle, exportJson, importJsonFromFile } from './utils/flashcardIo'

defineOptions({ name: 'FlashcardImportExportView' })

const { t } = useI18n()
const router = useRouter()
const store = useFlashcardsStore()

const fileInputEl = ref<HTMLInputElement | null>(null)
const busy = ref(false)
const message = ref('')
const messageKind = ref<'ok' | 'err'>('ok')

const counts = computed(() => ({
  notes: store.notes.length,
  cards: store.cards.length,
  decks: store.deckConfigs.length,
}))

async function onExport() {
  busy.value = true
  message.value = ''
  try {
    const bundle = buildExportBundle({
      notes: store.notes,
      cards: store.cards,
      deckConfigs: store.deckConfigs,
      reviewLogs: store.reviewLogs,
    })
    await exportJson({ bundle })
    messageKind.value = 'ok'
    message.value = t('flashcards.io.exportOk', { count: counts.value.notes })
  } catch (e: any) {
    messageKind.value = 'err'
    message.value = e?.message ?? t('flashcards.io.exportFailed')
  } finally {
    busy.value = false
  }
}

function triggerFilePicker() {
  message.value = ''
  fileInputEl.value?.click()
}

async function onFileChange(e: Event) {
  const target = e.target as HTMLInputElement
  const file = target.files?.[0]
  if (!file) return
  busy.value = true
  message.value = ''
  try {
    const bundle = await importJsonFromFile(file)
    /* 合并策略（Phase 6 简化）：按 id 去重覆盖。
     * - 已有 id → 用导入的覆盖（用户主动操作）
     * - 新 id → 追加
     */
    const noteMap = new Map(store.notes.map((n) => [n.id, n]))
    for (const n of bundle.notes) noteMap.set(n.id, n)
    const cardMap = new Map(store.cards.map((c) => [c.id, c]))
    for (const c of bundle.cards) cardMap.set(c.id, c)
    const deckMap = new Map(store.deckConfigs.map((d) => [d.deckId, d]))
    for (const d of bundle.deckConfigs) deckMap.set(d.deckId, d)
    store.replaceAllNotes([...noteMap.values()])
    store.replaceAllCards([...cardMap.values()])
    store.replaceAllDeckConfigs([...deckMap.values()])
    if (bundle.reviewLogs && bundle.reviewLogs.length > 0) {
      const logMap = new Map(store.reviewLogs.map((l) => [l.id, l]))
      for (const l of bundle.reviewLogs) logMap.set(l.id, l)
      store.replaceAllReviewLogs([...logMap.values()])
    }
    messageKind.value = 'ok'
    message.value = t('flashcards.io.importOk', {
      notes: bundle.notes.length,
      cards: bundle.cards.length,
      decks: bundle.deckConfigs.length,
    })
  } catch (e: any) {
    messageKind.value = 'err'
    message.value = e?.message ?? t('flashcards.io.importFailed')
  } finally {
    busy.value = false
    target.value = '' // 允许重复选择同一文件
  }
}

function goBack() {
  if (window.history.length > 1 && window.history.state?.back) router.back()
  else router.push('/flashcards')
}

onMounted(() => {
  store.loadFromCache()
})
</script>

<style scoped>
.page { min-height: 100%; background: var(--bg-base); display: flex; flex-direction: column; }

.head {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-4);
}
.head h1 { flex: 1; margin: 0; font-size: 18px; color: var(--text-primary); }
.back-btn {
  border: 0;
  background: transparent;
  color: var(--text-primary);
  padding: 6px;
  border-radius: 999px;
  cursor: pointer;
}

.content {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  padding: 0 var(--space-4) 100px;
}

.card {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  padding: var(--space-3);
}

.card h2 {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  margin: 0;
  font-size: 14px;
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
}

.card h2 .material-symbols-outlined {
  font-size: 18px;
  color: var(--brand-primary);
}

.muted {
  margin: 0;
  font-size: 12px;
  color: var(--text-secondary);
  line-height: 1.4;
}

.stats {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: var(--space-2);
  margin: 0;
  padding: var(--space-2);
  background: var(--bg-subtle);
  border-radius: var(--radius-sm);
}

.stats > div {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 2px;
}

.stats dt {
  font-size: 11px;
  color: var(--text-tertiary);
  text-transform: uppercase;
  letter-spacing: 0.4px;
}

.stats dd {
  margin: 0;
  font-size: 18px;
  font-weight: var(--font-weight-bold);
  color: var(--text-primary);
}

.primary {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-1);
  padding: 10px 16px;
  background: var(--brand-gradient);
  color: var(--text-inverse, #fff);
  border: 0;
  border-radius: var(--radius-md);
  font: inherit;
  font-size: 14px;
  font-weight: var(--font-weight-semibold);
  cursor: pointer;
}

.primary:disabled { opacity: 0.5; cursor: not-allowed; }
.primary .material-symbols-outlined { font-size: 18px; }

.hidden-input { display: none; }

.message {
  margin: 0;
  padding: var(--space-3);
  border-radius: var(--radius-sm);
  font-size: 13px;
}
.message.ok { background: var(--success-bg); color: var(--success); }
.message.err { background: var(--danger-bg); color: var(--danger); }
</style>