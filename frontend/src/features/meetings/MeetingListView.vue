<template>
  <div class="meetings-page">
    <DbLockedState
      v-if="dbNotReady"
      hint="会议记录需要本地加密数据库"
      @relogin="router.push('/login')"
    />

    <template v-else>
      <HeaderActionsPortal>
        <button
          v-for="f in filters"
          :key="f.id"
          type="button"
          class="hdr-filter"
          :class="{ active: filter === f.id }"
          :aria-pressed="filter === f.id"
          @click="onFilter(f.id)"
        >{{ f.label }}</button>
      </HeaderActionsPortal>

      <PullToRefresh :on-refresh="load" class="list-scroll">
        <div v-if="loading" class="state"><Skeleton :count="4" /></div>
        <EmptyState
          v-else-if="meetings.length === 0"
          icon="🎙️"
          :title="filter === 'archived' ? '没有已归档会议' : '还没有会议记录'"
          hint="点击右下角麦克风开始第一次会议"
          size="sm"
          variant="inline"
        />
        <div v-else class="meeting-list">
          <SwipeableListItem
            v-for="m in meetings"
            :key="m.id"
            :left-actions="filter === 'archived'
              ? [{ id: 'restore', icon: '↩', label: '恢复', type: 'success', onAction: () => onRestore(m.id) }]
              : [{ id: 'archive', icon: '📦', label: '归档', type: 'primary', onAction: () => onArchive(m.id) }]"
            :right-actions="[{ id: 'delete', icon: '🗑', label: '删除', type: 'danger', onAction: () => onDelete(m.id) }]"
            @activate="openMeeting(m)"
          >
            <div class="meeting-card" role="link" tabindex="0" @click="openMeeting(m)" @keydown.enter="openMeeting(m)">
              <div class="card-header">
                <h3 class="card-title">{{ m.title || '未命名会议' }}</h3>
                <span class="status-badge" :class="m.status">{{ statusText(m.status) }}</span>
              </div>
              <p v-if="m.topic && m.topic !== m.title" class="card-topic">{{ m.topic }}</p>
              <p v-else-if="m.summary" class="card-summary">{{ m.summary.slice(0, 80) }}</p>
              <div class="card-meta">
                <span>{{ formatMeetingWhen(m.startedAt) }}</span>
                <span v-if="m.durationMs">{{ formatDuration(m.durationMs) }}</span>
                <span v-if="formatLocationLine(m.location)">{{ formatLocationLine(m.location) }}</span>
                <span v-if="formatParticipants(m.participants)">👤 {{ formatParticipants(m.participants) }}</span>
              </div>
            </div>
          </SwipeableListItem>
          <div v-if="meetings.length > 0" ref="sentinelRef" class="more">
            <span v-if="loadingMore">加载中…</span>
            <!-- 失败：显式重试。旧实现失败后停在半截且没有任何重试入口。 -->
            <button v-else-if="listStatus === 'failed'" type="button" class="more-retry" @click="retry">
              加载失败，点击重试
            </button>
            <!-- 显式入口：既是哨兵不可用（needsManualLoad）时的降级路径，
                 也让「上拉加载更多」在无手势设备上仍然可达。 -->
            <button v-else-if="hasMore" type="button" class="more-retry" @click="loadMore">
              {{ needsManualLoad ? '继续加载' : '加载更多' }}
            </button>
            <span v-else>没有更多了</span>
          </div>
        </div>
      </PullToRefresh>

      <button
        class="fab"
        :class="{ recording: starting }"
        type="button"
        :aria-label="starting ? '正在进入录音' : '开始会议录音'"
        :disabled="starting"
        @click="startNewMeeting"
      >🎙</button>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useContinuousList } from '../../composables/useContinuousList'
import { DEFAULT_LIST_PAGE_SIZE, pageHasMore } from '../../native/list-sync/page'
import {
  Skeleton, EmptyState, DbLockedState, PullToRefresh, SwipeableListItem,
} from '@/components'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'
import {
  listMeetings, createMeeting, deleteMeeting, archiveMeeting, unarchiveMeeting, updateMeeting,
  type LocalMeeting,
} from './meetings-store'
import { deleteMeetingAudio } from '../../native/meeting-audio'
import {
  formatDuration, formatLocationLine, formatMeetingWhen, formatParticipants, statusText,
  MEETING_LIST_FILTERS, type MeetingListFilter,
} from './meeting-list'
import { captureDeviceLocation, formatCapturedTitle } from './meeting-meta'
import { useToast } from '../../composables/useToast'
import {
  describeRecordActionFailure,
  RECORD_ACTION_FAIL_TAG,
  RECORD_MEDIA_CLEANUP_FAIL_TAG,
} from '../../utils/action-failure-notice'
import { useListScene } from '../../composables/use-list-scene'

defineOptions({ name: 'MeetingListView' })

const router = useRouter()
const dbNotReady = ref(false)
// §838：列表侧的用户动作（归档/恢复/删除）此前无任何失败反馈。
const toast = useToast()
const starting = ref(false)
const filter = ref<MeetingListFilter>('active')
const filters = MEETING_LIST_FILTERS

/**
 * 列表数据源：2026-10-04 从 `useListSentinel` 迁到 Hyper 的
 * `useContinuousList`（首个迁移页）。
 *
 * 迁移换来的东西（旧的 `loadMore` 缺这些）：
 *  - **代次**：切换筛选后上一页的慢响应不再回写（`resetQuery()` 提升代次）。
 *  - **失败态**：读失败保留已加载行并显示显式重试，而不是静默停在半截。
 *  - **耗尽态**：`status==='exhausted'` 后断开 observer，不再空转请求。
 *  - **KeepAlive**：停用时 pause（断开 observer），回页时 resume 并重测 sentinel。
 *  - **去重**：按稳定 `m.id` 去重并同页原位替换。
 *
 * 保留的行为：首屏仍由 `load()` 语义承担（immediate），`dbNotReady` 仍由
 * fetchPage 内捕获 —— LocalDB 未初始化是这个页面特有的错误，不是通用失败。
 */
const {
  rows: meetings,
  status: listStatus,
  hasMore,
  needsManualLoad,
  sentinelRef,
  resetQuery,
  retry,
  loadMore,
} = useContinuousList<LocalMeeting>({
  pageSize: DEFAULT_LIST_PAGE_SIZE,
  fetchPage: async ({ page }) => {
    const res = await listMeetings(DEFAULT_LIST_PAGE_SIZE, {
      archived: filter.value === 'archived',
      // 后端是 offset 型，控制器是 page 型（从 1 起）。
      offset: (page - 1) * DEFAULT_LIST_PAGE_SIZE,
    })
    return {
      rows: res,
      // 本地库不提供 total，用「本页是否满」推导，与原 pageHasMore 语义一致。
      total: Number.MAX_SAFE_INTEGER,
      hasMore: pageHasMore(res.length),
    }
  },
})

const loading = computed(() => listStatus.value === 'idle' && meetings.value.length === 0 && !dbNotReady.value)
const loadingMore = computed(() => listStatus.value === 'loadingNext' || listStatus.value === 'refreshing')

async function load() {
  dbNotReady.value = false
  try {
    resetQuery()
  } catch (e: unknown) {
    const msg = e instanceof Error ? e.message : String(e)
    if (msg.includes('LocalDB 未初始化')) dbNotReady.value = true
  }
}

async function onFilter(next: MeetingListFilter) {
  filter.value = next
  // ⚠️ 换筛选必须走 resetQuery()（提升代次 + 清游标 + 回到首页），
  // 不能沿用旧游标继续追加，否则会看到上一个筛选的残留行。
  await load()
}

async function startNewMeeting() {
  if (starting.value) return
  starting.value = true
  try {
    const startedAt = Date.now()
    const m = await createMeeting({
      startedAt,
      title: formatCapturedTitle({ startedAt }),
    })
    void captureDeviceLocation().then(async (location) => {
      if (!location) return
      await updateMeeting(m.id, {
        location,
        title: formatCapturedTitle({ startedAt, location }),
      })
    })
    await router.push({ name: 'meeting-detail', params: { id: m.id }, query: { record: '1' } })
  } finally {
    starting.value = false
  }
}

function openMeeting(m: LocalMeeting) {
  router.push({ name: 'meeting-detail', params: { id: m.id } })
}

// ⚠⚠ §838：这五个处理器此前都没有 try/catch。列表变更刻意排在 await **之后**
//   （不能假装成功，这点是对的），但**零反馈** —— 用户点了归档，列表没变，
//   界面上一个字都没有。
async function onArchive(id: string) {
  try {
    await archiveMeeting(id)
  } catch (e) {
    console.error(RECORD_ACTION_FAIL_TAG, 'archive', e)
    toast.error(describeRecordActionFailure('archive', { error: e }))
    return
  }
  meetings.value = meetings.value.filter((m) => m.id !== id)
}

async function onRestore(id: string) {
  try {
    await unarchiveMeeting(id)
  } catch (e) {
    console.error(RECORD_ACTION_FAIL_TAG, 'restore', e)
    toast.error(describeRecordActionFailure('restore', { error: e }))
    return
  }
  meetings.value = meetings.value.filter((m) => m.id !== id)
}

async function onDelete(id: string) {
  // ⚠ 音频清理此前是 `catch { /* ok */ }` —— 而 deleteMeetingAudio 写的是
  //   IndexedDB（openDB + 事务），失败时音频 blob **永久残留**且零痕迹
  //   ⇒ 那是静默的存储泄漏，注释里的「ok」与事实不符（§838）。
  //   ⇒ 现在失败要说出来：主体已删除不必重试，但残留会占空间。
  let mediaCleaned = true
  try {
    await deleteMeeting(id)
  } catch (e) {
    console.error(RECORD_ACTION_FAIL_TAG, 'delete', e)
    toast.error(describeRecordActionFailure('delete', { error: e }))
    return
  }
  try {
    await deleteMeetingAudio(id)
  } catch (e) {
    mediaCleaned = false
    console.error(RECORD_MEDIA_CLEANUP_FAIL_TAG, e)
    toast.error(describeRecordActionFailure('delete', { error: e, mediaCleaned: false }))
  }
  meetings.value = meetings.value.filter((m) => m.id !== id)
}



onMounted(load)
/* KeepAlive 现场保持：active/archived 筛选保留；详情页改动过会议才刷新 */
useListScene('meetings', load)
</script>

<style scoped>
.meetings-page { position: relative; height: 100%; min-height: 0; display: flex; flex-direction: column; }
.hdr-filter { font-size: var(--text-smd); color: var(--text-secondary); }
.hdr-filter.active { color: var(--brand-primary); font-weight: 700; }
.list-scroll { flex: 1 1 auto; min-height: 0; }
.meeting-list { display: flex; flex-direction: column; gap: var(--space-2); padding: var(--space-3); }
.meeting-card { padding: var(--space-3); background: var(--bg-card); cursor: pointer; }
.card-header { display: flex; align-items: center; justify-content: space-between; gap: var(--space-2); }
.card-title { margin: 0; font-size: var(--text-md); font-weight: 600; color: var(--text-primary); }
.status-badge { font-size: var(--text-2xs); padding: 2px 8px; border-radius: var(--radius-full); background: var(--bg-subtle); color: var(--text-muted); flex-shrink: 0; }
.status-badge.recording { background: var(--danger-bg); color: var(--danger); }
/* 精校降级/被拦下：用警示色而不是中性灰 —— 中性灰读起来像「正常完成」。 */
.status-badge.refine-failed { background: var(--warning-bg, var(--bg-subtle)); color: var(--warning, var(--text-muted)); }
.card-topic, .card-summary { margin: var(--space-2) 0 0; font-size: var(--text-smd); color: var(--text-secondary); line-height: 1.5; }
.card-meta { display: flex; flex-wrap: wrap; gap: var(--space-3); margin-top: var(--space-2); font-size: var(--text-sm); color: var(--text-muted); }
.fab {
  position: fixed; right: var(--space-4); bottom: calc(var(--bottom-chrome-height) + var(--space-4));
  width: 56px; height: 56px; border-radius: 50%; border: none;
  background: var(--brand-gradient, linear-gradient(135deg, #667eea, #764ba2));
  color: var(--text-inverse); font-size: 24px; box-shadow: var(--shadow-lg, 0 4px 20px rgba(0,0,0,0.2));
  z-index: var(--z-fab);
}
.fab.recording { background: var(--danger); }
.fab:disabled { opacity: 0.7; }
.more-retry {
  display: block;
  width: 100%;
  padding: 10px 12px;
  background: transparent;
  color: var(--text-dim, #888);
  border: none;
  /* 用 token：13px 恰好等于 --text-smd，写死像素会被 font-size-token-equal 门禁判红 */
  font-size: var(--text-smd);
  cursor: pointer;
  /* 触控目标下限：主操作 ≥44px 是 compact 红线 */
  min-height: 44px;
}
.more-retry:active { opacity: 0.6; }
.more { padding: 16px 0 24px; text-align: center; font-size: var(--text-sm); color: var(--text-muted); }
</style>
