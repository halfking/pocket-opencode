<script setup lang="ts">
import { ref, computed, onMounted,watch,onBeforeUnmount } from 'vue'
import { useRouter } from 'vue-router'
import { rssApi, type RSSStarterFeed, type RSSCandidate } from '../../api/rss'
import { useI18n } from 'vue-i18n'
import { toUserMessage } from '../../api/error-message'
import { useAuthStore } from '../../stores/auth'
import {captureRssPortalOperation} from './rss-sync-store'

const router = useRouter()
const step = ref<'catalog' | 'discover'>('catalog')
const inputUrl = ref('')
const starters = ref<RSSStarterFeed[]>([])
const allCategories = ref<string[]>([])
const activeCategory = ref<string>('')
const importResult = ref<string>('')
const candidates = ref<RSSCandidate[]>([])
const errorMsg = ref('')
const busy = ref(false)
const interval = ref('30m')
const enabled = ref(true)
const { t } = useI18n()
const auth=useAuthStore();let generation=0
const current=(stamp:number)=>stamp===generation&&auth.activationStatus==='ready'
watch(()=>[auth.activationVersion,auth.userId,auth.workspaceId,auth.activationStatus],()=>{generation++;step.value='catalog';inputUrl.value='';candidates.value=[];starters.value=[];allCategories.value=[];activeCategory.value='';importResult.value=errorMsg.value='';busy.value=false;if(auth.activationStatus==='ready')void loadCatalog()},{flush:'sync'})
onBeforeUnmount(()=>{generation++})

const label = (cat: string) => starters.value.find(f => f.category === cat)?.categoryLabel || cat

const visibleStarters = computed(() => {
  if (!activeCategory.value) return starters.value
  return starters.value.filter(f => f.category === activeCategory.value)
})

// 目录按分类分组渲染；「全部」时逐组展示并带组头。
const grouped = computed(() => {
  const byCat = new Map<string, RSSStarterFeed[]>()
  for (const f of visibleStarters.value) {
    const list = byCat.get(f.category) || []
    list.push(f)
    byCat.set(f.category, list)
  }
  return [...byCat.entries()].map(([cat, feeds]) => ({ cat, label: feeds[0]?.categoryLabel || cat, feeds }))
})

async function loadCatalog() {
  const stamp=generation
  try {
    const operation=captureRssPortalOperation(),res = await rssApi.listStarter(undefined,{assertCurrent:operation.assertCurrent})
    operation.assertCurrent();if(!current(stamp))return
    starters.value = res.feeds ?? []
    allCategories.value = res.categories ?? []
  } catch (e: any) {
    if(current(stamp)&&e?.name!=='AbortError')errorMsg.value = `目录加载失败：${e?.message ?? e}`
  }
}

async function start() {
  const stamp=generation
  const url = inputUrl.value.trim()
  if (!url) return
  busy.value = true
  errorMsg.value = ''
  try {
    const operation=captureRssPortalOperation(),found=await rssApi.discover(url,{assertCurrent:operation.assertCurrent})
    operation.assertCurrent();if(!current(stamp))return
    candidates.value = found
    if (candidates.value.length === 0) {
      candidates.value = [{ url, title: '' }]
    }
    step.value = 'discover'
  } catch (e: any) {
    if(current(stamp)&&e?.name!=='AbortError')errorMsg.value = `发现失败：${e?.message ?? e}`
  } finally {
    if(current(stamp))busy.value = false
  }
}

async function useCatalogFeed(s: RSSStarterFeed) {
  if(!starters.value.includes(s))return
  inputUrl.value = s.url
  await start()
}

/** 一键订阅整个分类（幂等：已订阅的自动跳过）。 */
async function importCategory(cat: string) {
  if(!allCategories.value.includes(cat))return
  const stamp=generation
  busy.value = true
  errorMsg.value = ''
  importResult.value = ''
  try {
    const operation=captureRssPortalOperation(),res = await rssApi.importStarter({ categories: [cat], enabled: true },{assertCurrent:operation.assertCurrent})
    operation.assertCurrent();if(!current(stamp))return
    importResult.value = `已订阅 ${res.created} 个源` + (res.skipped > 0 ? `（${res.skipped} 个此前已订阅）` : '')
  } catch (e: any) {
    if(current(stamp)&&e?.name!=='AbortError')errorMsg.value = toUserMessage(e, t, t('errors.operateFailed'))
  } finally {
    if(current(stamp))busy.value = false
  }
}

async function pickCandidate(c: RSSCandidate) {
  const stamp=generation
  busy.value = true
  errorMsg.value = ''
  try {
    if(!candidates.value.includes(c))throw new DOMException('Former RSS discovery result','AbortError')
    const operation=captureRssPortalOperation()
    await rssApi.addSource({
      url: c.url,
      title: c.title || '',
      enabled: enabled.value,
      fetchInterval: interval.value,
    },{assertCurrent:operation.assertCurrent})
    operation.assertCurrent();if(!current(stamp))return
    router.replace({ name: 'rss' })
  } catch (e: any) {
    if(current(stamp)&&e?.name!=='AbortError')errorMsg.value = `保存失败：${e?.message ?? e}`
  } finally {
    if(current(stamp))busy.value = false
  }
}

async function addDirect() {
  const stamp=generation
  // 没找到候选但用户直接给了 URL
  busy.value = true
  errorMsg.value = ''
  try {
    const operation=captureRssPortalOperation()
    await rssApi.addSource({ url: inputUrl.value.trim(), enabled: enabled.value, fetchInterval: interval.value },{assertCurrent:operation.assertCurrent})
    operation.assertCurrent();if(!current(stamp))return
    router.replace({ name: 'rss' })
  } catch (e: any) {
    if(current(stamp)&&e?.name!=='AbortError')errorMsg.value = toUserMessage(e, t, t('errors.operateFailed'))
  } finally {
    if(current(stamp))busy.value = false
  }
}

onMounted(loadCatalog)

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
    <div v-if="importResult" class="ok">{{ importResult }}</div>

    <!-- Step 1: 分类目录 + 手动 URL -->
    <section v-if="step === 'catalog'" class="step">
      <label>粘贴网站或 feed URL</label>
      <input id="rss-add-url" v-model="inputUrl" placeholder="https://example.com 或 https://example.com/feed" @keydown.enter="start" />
      <button class="btn btn-primary" :disabled="!inputUrl.trim() || busy" @click="start">
        {{ busy ? '发现中…' : '发现候选' }}
      </button>

      <div class="divider">或从内置目录选择（后台自动拉取最新内容）</div>

      <div class="chips" role="tablist">
        <button
          class="chip"
          :class="{ active: activeCategory === '' }"
          role="tab"
          :aria-selected="activeCategory === ''"
          @click="activeCategory = ''"
        >全部</button>
        <button
          v-for="cat in allCategories"
          :key="cat"
          class="chip"
          :class="{ active: activeCategory === cat }"
          role="tab"
          :aria-selected="activeCategory === cat"
          @click="activeCategory = cat"
        >{{ label(cat) }}</button>
      </div>

      <section v-for="group in grouped" :key="group.cat" class="cat-group">
        <header class="cat-head">
          <h4>{{ group.label }} <span class="count">{{ group.feeds.length }}</span></h4>
          <button class="btn btn-mini" :disabled="busy" @click="importCategory(group.cat)">一键订阅本类</button>
        </header>
        <ul class="seeds">
          <li v-for="s in group.feeds" :key="s.url" @click="useCatalogFeed(s)">
            <div class="seed-title">{{ s.title }}</div>
            <div v-if="s.note" class="seed-note">{{ s.note }}</div>
            <div class="seed-url">{{ s.url }}</div>
            <span class="badge">{{ s.language }}</span>
          </li>
        </ul>
      </section>
      <div v-if="starters.length === 0 && !errorMsg" class="empty">目录为空</div>
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
        <input id="rss-add-url" v-model="inputUrl" />
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
.step label { display: block; margin: 8px 0 4px; color: var(--text-muted); font-size: var(--text-smd); }
.step input[type="text"], .step input:not([type]) { width: 100%; padding: 8px; border: 1px solid var(--border); border-radius: 6px; box-sizing: border-box; }
.btn { display: inline-flex; align-items: center; gap: 4px; padding: 8px 16px; border: 1px solid var(--border); background: var(--bg-elevated); border-radius: 6px; cursor: pointer; margin-top: 8px; }
.btn-primary { background: var(--brand-primary); color: white; border-color: var(--brand-primary); }
.btn-mini { padding: 4px 10px; font-size: var(--text-sm); margin-top: 0; }
.icon { padding: 4px 8px; border: none; background: transparent; cursor: pointer; font-size: 20px; }
.error { padding: 8px 12px; background: var(--err-bg); color: var(--err-fg); border-radius: 6px; margin-bottom: 8px; }
.ok { padding: 8px 12px; background: var(--bg-hover); color: var(--text-secondary); border-radius: 6px; margin-bottom: 8px; }
.divider { text-align: center; margin: 16px 0; color: var(--text-muted); font-size: var(--text-sm); position: relative; }
.divider::before, .divider::after { content: ''; display: inline-block; width: 30%; vertical-align: middle; border-top: 1px solid var(--border); margin: 0 8px; }
.chips { display: flex; gap: 6px; flex-wrap: wrap; margin-bottom: 12px; }
.chip { padding: 4px 12px; border-radius: 999px; border: 1px solid var(--border); background: var(--bg-elevated); cursor: pointer; font-size: var(--text-sm); }
.chip.active { background: var(--brand-primary); color: white; border-color: var(--brand-primary); }
.cat-group { margin-bottom: 16px; }
.cat-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 6px; }
.cat-head h4 { margin: 0; font-size: var(--text-md); }
.count { color: var(--text-muted); font-size: var(--text-sm); font-weight: normal; }
.seeds, .candidates { list-style: none; padding: 0; }
.seeds li, .candidates li { padding: 12px; border: 1px solid var(--border); border-radius: 6px; margin-bottom: 8px; cursor: pointer; }
.seeds li:hover, .candidates li:hover { background: var(--bg-hover); }
.seed-title { font-weight: 600; }
.seed-note { font-size: var(--text-sm); color: var(--text-muted); margin-top: 2px; }
.seed-url { font-size: var(--text-sm); color: var(--text-muted); word-break: break-all; }
.badge { display: inline-block; padding: 2px 6px; background: var(--bg-hover); color: var(--text-secondary); border-radius: 10px; font-size: var(--text-2xs); margin-right: 4px; }
.candidates li { display: flex; gap: 8px; align-items: center; }
.cand-url { font-family: var(--font-mono); font-size: var(--text-smd); word-break: break-all; }
.cand-title { font-size: var(--text-sm); color: var(--text-muted); }
.manual { display: flex; gap: 8px; align-items: center; }
.manual input { flex: 1; }
.options { display: flex; gap: 16px; margin-top: 16px; }
.options label { display: flex; align-items: center; gap: 6px; }
.options input { width: 80px; }
.empty { padding: 24px; text-align: center; color: var(--text-muted); }
</style>
