<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { rssApi, type RSSItem } from '../../api/rss'

const route = useRoute()
const router = useRouter()
const item = ref<RSSItem | null>(null)
const loading = ref(true)
const errorMsg = ref('')
const shareOpen = ref(false)
const shareCaption = ref('')
const shareResult = ref<{ copyText?: string; deepLink?: string; hint?: string } | null>(null)
const shareBusy = ref(false)

async function load() {
  loading.value = true
  errorMsg.value = ''
  try {
    const id = String(route.params.id)
    item.value = await rssApi.getItem(id)
    if (item.value.status === 'unread') {
      try {
        item.value = await rssApi.markRead(id)
      } catch { /* ignore */ }
    }
  } catch (e: any) {
    errorMsg.value = e?.message ?? String(e)
  } finally {
    loading.value = false
  }
}

onMounted(load)

function openShare() {
  if (!item.value) return
  shareCaption.value = item.value.summary?.slice(0, 120) || item.value.title
  shareResult.value = null
  shareOpen.value = true
}

async function doShare(dest: 'wechat' | 'weibo' | 'clipboard' | 'download') {
  if (!item.value) return
  shareBusy.value = true
  shareResult.value = null
  try {
    const r = await rssApi.share(item.value.id, dest, shareCaption.value)
    shareResult.value = r
    if (dest === 'clipboard' && r.copyText) {
      try {
        await navigator.clipboard.writeText(r.copyText)
      } catch { /* 浏览器/Capacitor 不支持时静默 */ }
    }
    if (dest === 'weibo' && r.deepLink) {
      window.open(r.deepLink, '_blank')
    }
  } catch (e: any) {
    errorMsg.value = `分享失败：${e?.message ?? e}`
  } finally {
    shareBusy.value = false
  }
}

async function toggleStar() {
  if (!item.value) return
  const newStar = item.value.status !== 'starred'
  try {
    item.value = await rssApi.setStarred(item.value.id, newStar)
  } catch (e: any) {
    errorMsg.value = e?.message ?? String(e)
  }
}

const shareCardSrc = computed(() => item.value ? rssApi.shareCardUrl(item.value.id) : '')
function back() {
  if (window.history.length > 1) router.back()
  else router.push({ name: 'rss' })
}
</script>

<template>
  <div class="rss-detail">
    <header class="bar">
      <button class="icon" @click="back" aria-label="返回">
        <span class="material-symbols-outlined">arrow_back</span>
      </button>
      <h3 class="title-clamp">{{ item?.title || '加载中…' }}</h3>
    </header>

    <div v-if="loading" class="empty">加载中…</div>
    <div v-else-if="errorMsg" class="error">{{ errorMsg }}</div>
    <article v-else-if="item" class="article">
      <div class="meta-row">
        <span v-if="item.author">{{ item.author }}</span>
        <span v-if="item.publishedAt">{{ new Date(item.publishedAt).toLocaleString() }}</span>
        <a :href="item.url" target="_blank" rel="noopener">原文 ↗</a>
      </div>
      <div v-if="item.summary" class="summary">{{ item.summary }}</div>
      <div v-if="item.content" class="content" v-html="item.content"></div>
    </article>

    <footer v-if="item" class="actions-bar">
      <button class="btn" type="button" @click="toggleStar">
        <span class="material-symbols-outlined">{{ item.status === 'starred' ? 'star' : 'star_border' }}</span>
        {{ item.status === 'starred' ? '已收藏' : '收藏' }}
      </button>
      <button class="btn btn-primary" type="button" @click="openShare">
        <span class="material-symbols-outlined">share</span>
        一键分享
      </button>
    </footer>

    <!-- 分享 modal -->
    <div v-if="shareOpen" class="modal-mask" @click.self="shareOpen = false">
      <div class="modal">
        <header><h4>分享到…</h4><button class="icon" @click="shareOpen = false">×</button></header>
        <div class="caption-input">
          <label>分享文案</label>
          <textarea v-model="shareCaption" rows="3"></textarea>
        </div>
        <div class="share-grid">
          <div class="preview-card">
            <img :src="shareCardSrc" alt="share card" />
          </div>
          <div class="share-buttons">
            <button class="btn" :disabled="shareBusy" @click="doShare('wechat')">微信朋友圈</button>
            <button class="btn" :disabled="shareBusy" @click="doShare('weibo')">微博</button>
            <button class="btn" :disabled="shareBusy" @click="doShare('clipboard')">复制到剪贴板</button>
            <button class="btn" :disabled="shareBusy" @click="doShare('download')">下载卡片图</button>
          </div>
        </div>
        <div v-if="shareResult" class="result">
          <div v-if="shareResult.deepLink">
            <strong>微博深链接：</strong>
            <a :href="shareResult.deepLink" target="_blank">{{ shareResult.deepLink }}</a>
          </div>
          <div v-if="shareResult.copyText">
            <strong>复制内容：</strong>
            <pre>{{ shareResult.copyText }}</pre>
          </div>
          <div v-if="shareResult.hint" class="hint">{{ shareResult.hint }}</div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.rss-detail { padding: 16px; max-width: 720px; margin: 0 auto; }
.bar { display: flex; align-items: center; gap: 8px; margin-bottom: 12px; }
.title-clamp { flex: 1; font-size: 18px; margin: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.empty { padding: 32px; text-align: center; color: var(--text-muted); }
.error { padding: 8px 12px; background: var(--err-bg); color: var(--err-fg); border-radius: 6px; margin-bottom: 8px; }
.article { padding: 12px 0; }
.meta-row { font-size: 12px; color: var(--text-muted); display: flex; gap: 12px; margin-bottom: 12px; }
.meta-row a { color: var(--accent); }
.summary { font-size: 15px; line-height: 1.6; margin-bottom: 16px; }
.content { font-size: 14px; line-height: 1.6; }
.actions-bar { position: sticky; bottom: 0; display: flex; gap: 8px; padding: 12px; background: var(--bg); border-top: 1px solid var(--border); }
.btn { display: inline-flex; align-items: center; gap: 4px; padding: 8px 16px; border: 1px solid var(--border); background: var(--bg-elevated); border-radius: 6px; cursor: pointer; }
.btn-primary { background: var(--accent); color: white; border-color: var(--accent); }
.icon { padding: 4px 8px; border: none; background: transparent; cursor: pointer; font-size: 20px; }
.modal-mask { position: fixed; inset: 0; background: rgba(0,0,0,0.4); display: flex; align-items: center; justify-content: center; z-index: 1000; }
.modal { background: var(--bg-elevated); border-radius: 8px; padding: 16px; width: 90%; max-width: 520px; }
.modal header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px; }
.caption-input label { display: block; font-size: 12px; color: var(--text-muted); margin-bottom: 4px; }
.caption-input textarea { width: 100%; box-sizing: border-box; padding: 6px; border: 1px solid var(--border); border-radius: 6px; }
.share-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; margin-top: 12px; }
.preview-card img { width: 100%; height: auto; border-radius: 6px; }
.share-buttons { display: flex; flex-direction: column; gap: 8px; }
.share-buttons .btn { justify-content: center; }
.result { margin-top: 12px; padding-top: 12px; border-top: 1px solid var(--border); font-size: 13px; }
.result pre { white-space: pre-wrap; word-break: break-all; padding: 6px; background: var(--bg); border-radius: 4px; }
.hint { color: var(--text-muted); font-style: italic; margin-top: 4px; }
</style>