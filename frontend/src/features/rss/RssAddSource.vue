<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { rssApi, type RSSSeed, type RSSCandidate } from '../api/rss'

const router = useRouter()
const step = ref<'input' | 'discover' | 'confirm'>('input')
const inputUrl = ref('')
const seeds = ref<RSSSeed[]>([])
const candidates = ref<RSSCandidate[]>([])
const errorMsg = ref('')
const busy = ref(false)
const interval = ref('30m')
const enabled = ref(true)

async function start() {
  const url = inputUrl.value.trim()
  if (!url) return
  busy.value = true
  errorMsg.value = ''
  try {
    candidates.value = await rssApi.discover(url)
    if (candidates.value.length === 0) {
      // 没找到候选，假定用户直接给了 feed URL
      candidates.value = [{ url, title: '' }]
    }
    step.value = 'discover'
  } catch (e: any) {
    errorMsg.value = `发现失败：${e?.message ?? e}`
  } finally {
    busy.value = false
  }
}

async function useSeed(s: RSSSeed) {
  inputUrl.value = s.url
  await start()
}

async function pickCandidate(c: RSSCandidate) {
  busy.value = true
  errorMsg.value = ''
  try {
    await rssApi.addSource({
      url: c.url,
      title: c.title || '',
      enabled: enabled.value,
      fetchInterval: interval.value,
    })
    router.replace({ name: 'rss' })
  } catch (e: any) {
    errorMsg.value = `保存失败：${e?.message ?? e}`
  } finally {
    busy.value = false
  }
}

async function addDirect() {
  // 没找到候选但用户直接给了 URL
  busy.value = true
  errorMsg.value = ''
  try {
    await rssApi.addSource({ url: inputUrl.value.trim(), enabled: enabled.value, fetchInterval: interval.value })
    router.replace({ name: 'rss' })
  } catch (e: any) {
    errorMsg.value = e?.message ?? String(e)
  } finally {
    busy.value = false
  }
}

onMounted(async () => {
  try { seeds.value = await rssApi.listSeeds() } catch { /* 静默失败 */ }
})

function back() {
  if (window.history.length > 1) router.back()
  else router.replace({ name: 'rss' })
}
</script>

<template>
  <div class="rss-add">
    <header class="bar">
      <button class="icon" @click="back" aria-label="返回">
        <span class="material-symbols-outlined">arrow_back</span>
      </button>
      <h3>添加订阅源</h3>
    </header>

    <div v-if="errorMsg" class="error">{{ errorMsg }}</div>

    <!-- Step 1: paste URL or pick a seed -->
    <section v-if="step === 'input'" class="step">
      <label>粘贴网站或 feed URL</label>
      <input v-model="inputUrl" placeholder="https://example.com 或 https://example.com/feed" @keydown.enter="start" />
      <button class="btn btn-primary" :disabled="!inputUrl.trim() || busy" @click="start">
        {{ busy ? '发现中…' : '发现候选' }}
      </button>

      <div class="divider">或者从内置种子开始</div>
      <ul class="seeds">
        <li v-for="s in seeds" :key="s.url" @click="useSeed(s)">
          <div class="seed-title">{{ s.title }}</div>
          <div class="seed-url">{{ s.url }}</div>
          <span class="badge">{{ s.language }}</span>
          <span class="badge subtle">{{ s.category }}</span>
        </li>
      </ul>
    </section>

    <!-- Step 2: pick candidate + options -->
    <section v-else class="step">
      <p>系统找到 {{ candidates.length }} 个候选 feed，挑一个订阅：</p>
      <ul class="candidates">
        <li v-for="c in candidates" :key="c.url" @click="pickCandidate(c)">
          <span class="material-symbols-outlined">rss_feed</span>
          <div class="cand-info">
            <div class="cand-url">{{ c.url }}</div>
            <div v-if="c.title" class="cand-title">{{ c.title }}</div>
          </div>
        </li>
      </ul>

      <div class="divider">或者手动指定 URL</div>
      <div class="manual">
        <input v-model="inputUrl" />
        <button class="btn" :disabled="busy" @click="addDirect">添加</button>
      </div>

      <div class="options">
        <label><input type="checkbox" v-model="enabled" /> 启用</label>
        <label>拉取间隔 <input v-model="interval" placeholder="30m" /></label>
      </div>
    </section>
  </div>
</template>

<style scoped>
.rss-add { padding: 16px; max-width: 720px; margin: 0 auto; }
.bar { display: flex; align-items: center; gap: 8px; margin-bottom: 12px; }
.bar h3 { margin: 0; flex: 1; }
.step label { display: block; margin: 8px 0 4px; color: var(--text-muted); font-size: 13px; }
.step input[type="text"], .step input:not([type]) { width: 100%; padding: 8px; border: 1px solid var(--border); border-radius: 6px; box-sizing: border-box; }
.btn { display: inline-flex; align-items: center; gap: 4px; padding: 8px 16px; border: 1px solid var(--border); background: var(--bg-elevated); border-radius: 6px; cursor: pointer; margin-top: 8px; }
.btn-primary { background: var(--accent); color: white; border-color: var(--accent); }
.icon { padding: 4px 8px; border: none; background: transparent; cursor: pointer; font-size: 20px; }
.error { padding: 8px 12px; background: var(--err-bg); color: var(--err-fg); border-radius: 6px; margin-bottom: 8px; }
.divider { text-align: center; margin: 16px 0; color: var(--text-muted); font-size: 12px; position: relative; }
.divider::before, .divider::after { content: ''; display: inline-block; width: 30%; vertical-align: middle; border-top: 1px solid var(--border); margin: 0 8px; }
.seeds, .candidates { list-style: none; padding: 0; }
.seeds li, .candidates li { padding: 12px; border: 1px solid var(--border); border-radius: 6px; margin-bottom: 8px; cursor: pointer; }
.seeds li:hover, .candidates li:hover { background: var(--bg-hover); }
.seed-title { font-weight: 600; }
.seed-url { font-size: 12px; color: var(--text-muted); word-break: break-all; }
.badge { display: inline-block; padding: 2px 6px; background: var(--accent); color: white; border-radius: 10px; font-size: 11px; margin-right: 4px; }
.badge.subtle { background: var(--bg-hover); color: var(--text-secondary); }
.candidates li { display: flex; gap: 8px; align-items: center; }
.cand-url { font-family: monospace; font-size: 13px; word-break: break-all; }
.cand-title { font-size: 12px; color: var(--text-muted); }
.manual { display: flex; gap: 8px; align-items: center; }
.manual input { flex: 1; }
.options { display: flex; gap: 16px; margin-top: 16px; }
.options label { display: flex; align-items: center; gap: 6px; }
.options input { width: 80px; }
</style>