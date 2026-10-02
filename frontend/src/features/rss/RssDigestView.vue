<script setup lang="ts">
/**
 * RssDigestView — 每天一份的「全部信息摘要」。
 *
 * 三件事必须在这里做完，不能只给一个列表页：
 *  - 看：按 IT / 财经 / 时事分组，可切历史日期。
 *  - 收：页面本身就是每天推送那条系统通知的落点（点通知直接进这里）。
 *  - 分享：一键拉起系统分享面板，文本与卡片图两种形态（微博/朋友圈都在面板里）。
 */
import { ref, onMounted, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { rssApi, type RSSDigest, type RSSDigestListItem } from '../../api/rss'
import { toUserMessage } from '../../api/error-message'
import { getPocketNative } from '../../native/pocket-native'
import { shareDigestText, renderDigestCardDataURL, dataURLToBase64 } from './digest-share'

const router = useRouter()
const { t } = useI18n()

const digest = ref<RSSDigest | null>(null)
const history = ref<RSSDigestListItem[]>([])
const loading = ref(true)
const regenerating = ref(false)
const sharing = ref(false)
const errorMsg = ref('')
const noticeMsg = ref('')
const selectedDate = ref('')

const today = computed(() => digest.value?.date ?? '')
const hasItems = computed(() => (digest.value?.itemCount ?? 0) > 0)

async function load(date?: string) {
  loading.value = true
  errorMsg.value = ''
  try {
    digest.value = await rssApi.getDigest(date)
    selectedDate.value = digest.value.date
    history.value = await rssApi.listDigests(14)
  } catch (e: any) {
    errorMsg.value = toUserMessage(e, t, t('errors.loadRssFailed'))
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void load()
})

async function regenerate() {
  regenerating.value = true
  errorMsg.value = ''
  try {
    digest.value = await rssApi.runDigest(selectedDate.value || undefined)
    noticeMsg.value = `已重新生成 ${digest.value.date} 的摘要（${digest.value.itemCount} 条）`
    history.value = await rssApi.listDigests(14)
  } catch (e: any) {
    errorMsg.value = toUserMessage(e, t, t('errors.operateFailed'))
  } finally {
    regenerating.value = false
  }
}

/** 分享文本：微博/朋友圈正文，复制与系统面板都用它。 */
async function shareText() {
  if (!digest.value) return
  const text = shareDigestText(digest.value)
  sharing.value = true
  errorMsg.value = ''
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
      noticeMsg.value = '摘要文本已复制，可直接粘贴到微博/朋友圈'
    }
    const native = getPocketNative()
    if (native.platform !== 'web') {
      await native.share.share({ title: digest.value.headline, text, dialogTitle: '分享今日摘要' })
    }
  } catch (e: any) {
    errorMsg.value = `分享失败：${e?.message ?? e}`
  } finally {
    sharing.value = false
  }
}

/**
 * 分享卡片图。
 *
 * 路径：canvas 画 1080×1350 → dataURL →（原生）写入 Filesystem cache →
 * Share.share({ files }) 拉起系统面板。Web 端没有 files 能力，退回分享文本。
 */
async function shareCard() {
  if (!digest.value) return
  sharing.value = true
  errorMsg.value = ''
  try {
    const dataURL = renderDigestCardDataURL(digest.value)
    const native = getPocketNative()
    if (native.platform === 'web') {
      noticeMsg.value = '当前环境不支持分享图片，已改为分享文本'
      await shareText()
      return
    }
    const name = `digest-${digest.value.date}.png`
    // writeFile 只负责落盘（返回 void），分享需要的是 file:// URI，所以再取一次。
    await native.filesystem.writeFile(name, dataURLToBase64(dataURL), 'cache')
    const { uri } = await native.filesystem.getUri(name, 'cache')
    await native.share.share({
      title: digest.value.headline,
      text: shareDigestText(digest.value, 5),
      dialogTitle: '分享今日摘要卡片',
      // Android 分享图片必须走 files；url 在 Android 侧不会作为附件传下去。
      files: [uri],
    })
  } catch (e: any) {
    errorMsg.value = `分享卡片失败：${e?.message ?? e}`
  } finally {
    sharing.value = false
  }
}

function openItem(id: string) {
  router.push({ name: 'rss-item', params: { id } })
}
function back() {
  if (window.history.length > 1) router.back()
  else router.replace({ name: 'rss' })
}
function openSources() {
  router.push({ name: 'rss' })
}
</script>

<template>
  <div class="digest-page">
    <header class="bar">
      <button class="icon" @click="back" aria-label="返回">
        <span class="material-symbols-outlined">arrow_back</span>
      </button>
      <h3>每日摘要</h3>
      <button class="icon" @click="openSources" title="订阅列表">
        <span class="material-symbols-outlined">rss_feed</span>
      </button>
    </header>

    <div v-if="errorMsg" class="error">{{ errorMsg }}</div>
    <div v-if="noticeMsg" class="notice">{{ noticeMsg }}</div>

    <div v-if="loading" class="empty">加载中…</div>

    <template v-else-if="digest">
      <section class="headline-card">
        <div class="date">{{ digest.date }}</div>
        <h4>{{ digest.headline }}</h4>
        <div class="stats">
          <span>{{ digest.itemCount }} 条</span>
          <span>·</span>
          <span>{{ digest.sourceCount }} 个来源</span>
        </div>
        <div class="share-row">
          <button class="btn primary" :disabled="sharing || !hasItems" @click="shareText">
            <span class="material-symbols-outlined">share</span>
            分享到微博/朋友圈
          </button>
          <button class="btn" :disabled="sharing || !hasItems" @click="shareCard">
            <span class="material-symbols-outlined">image</span>
            分享卡片图
          </button>
          <button class="btn" :disabled="regenerating" @click="regenerate">
            <span class="material-symbols-outlined">refresh</span>
            重新生成
          </button>
        </div>
      </section>

      <div v-if="!hasItems" class="empty">
        今天还没有内容。先去
        <button class="link" @click="openSources">订阅源</button>
        导入推荐源，等后台拉取一轮就有了。
      </div>

      <section v-for="sec in digest.sections" :key="sec.category" class="section">
        <h5>{{ sec.label }} <span class="count">{{ sec.items.length }}</span></h5>
        <ul>
          <li v-for="it in sec.items" :key="it.id" @click="openItem(it.id)">
            <div class="item-title">{{ it.title || '(无标题)' }}</div>
            <div class="item-meta">
              <span>{{ it.sourceTitle }}</span>
              <span v-if="it.publishedAt">{{ new Date(it.publishedAt).toLocaleString() }}</span>
            </div>
            <div v-if="it.summary" class="item-summary">{{ it.summary }}</div>
            <a v-if="it.url" class="item-link" :href="it.url" target="_blank" rel="noreferrer" @click.stop>
              {{ it.url }}
            </a>
          </li>
        </ul>
      </section>

      <section v-if="history.length > 1" class="history">
        <h5>历史摘要</h5>
        <ul>
          <li v-for="d in history" :key="d.date" :class="{ current: d.date === today }" @click="load(d.date)">
            <span class="h-date">{{ d.date }}</span>
            <span class="h-count">{{ d.itemCount }} 条</span>
          </li>
        </ul>
      </section>
    </template>
  </div>
</template>

<style scoped>
.digest-page { padding: 16px; max-width: 720px; margin: 0 auto; }
.bar { display: flex; align-items: center; gap: 8px; margin-bottom: 12px; }
.bar h3 { margin: 0; flex: 1; }
.icon { padding: 4px 8px; border: none; background: transparent; cursor: pointer; font-size: 20px; }
.headline-card { border: 1px solid var(--border); border-radius: 10px; padding: 16px; background: var(--bg-elevated); }
.headline-card .date { color: var(--text-muted); font-size: var(--text-sm); }
.headline-card h4 { margin: 6px 0; font-size: var(--text-xl); }
.stats { color: var(--text-muted); font-size: var(--text-sm); display: flex; gap: 6px; }
.share-row { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 12px; }
.btn { display: inline-flex; align-items: center; gap: 4px; padding: 8px 12px; border: 1px solid var(--border); background: var(--bg-elevated); border-radius: 6px; cursor: pointer; }
.btn.primary { background: var(--brand-primary); color: white; border-color: var(--brand-primary); }
.btn:disabled { opacity: 0.5; cursor: not-allowed; }
.section { margin-top: 20px; }
.section h5 { margin: 0 0 8px; font-size: var(--text-lg); }
.section .count { color: var(--text-muted); font-size: var(--text-sm); }
.section ul { list-style: none; padding: 0; margin: 0; }
.section li { padding: 10px 0; border-bottom: 1px solid var(--border); cursor: pointer; }
.item-title { font-weight: 600; }
.item-meta { display: flex; gap: 8px; font-size: var(--text-sm); color: var(--text-muted); margin-top: 2px; }
.item-summary { font-size: var(--text-smd); color: var(--text-secondary); margin-top: 4px; }
.item-link { display: inline-block; font-size: var(--text-sm); color: var(--brand-primary); word-break: break-all; margin-top: 4px; }
.history { margin-top: 24px; }
.history ul { list-style: none; padding: 0; }
.history li { display: flex; justify-content: space-between; padding: 8px 0; border-bottom: 1px solid var(--border); cursor: pointer; }
.history li.current { color: var(--brand-primary); font-weight: 600; }
.empty { padding: 32px; text-align: center; color: var(--text-muted); }
.error { padding: 8px 12px; background: var(--err-bg); color: var(--err-fg); border-radius: 6px; margin-bottom: 8px; }
.notice { padding: 8px 12px; background: var(--bg-hover); border-radius: 6px; margin-bottom: 8px; font-size: var(--text-sm); }
.link { background: none; border: none; color: var(--brand-primary); cursor: pointer; text-decoration: underline; padding: 0; }
</style>
