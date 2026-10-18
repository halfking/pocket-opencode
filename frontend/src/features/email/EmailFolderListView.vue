<!-- 邮件目录管理：目录列表（进入/新建/删除）+ 本地迁移操作日志 + 同步到服务器。 -->
<template>
  <div class="folders-page">
    <HeaderActionsPortal>
      <button type="button" aria-label="新建目录" @click="createOpen = true">
        <span class="material-symbols-outlined" aria-hidden="true">create_new_folder</span>
      </button>
    </HeaderActionsPortal>

    <PullToRefresh :on-refresh="reload" class="folders-scroll">
      <!-- 同步到服务器：本地迁移操作 → 邮件服务器（IMAP MOVE / 移入垃圾箱） -->
      <section class="sync-card">
        <div class="sync-row">
          <div class="sync-info">
            <strong>同步到服务器</strong>
            <span v-if="pendingCount > 0" class="badge">{{ pendingCount }} 项待同步</span>
            <span v-else class="muted">本地操作均已同步</span>
          </div>
          <button type="button" class="sync-btn" :disabled="syncing || pendingCount === 0" @click="onSyncAll">
            {{ syncing ? '同步中…' : '全量同步' }}
          </button>
        </div>
        <template v-if="ops.length > 0">
          <div class="ops-head">
            <span>操作日志</span>
            <label v-if="pendingCount > 0" class="pick-all">
              <input v-model="selectAll" type="checkbox" @change="toggleSelectAll" /> 可选同步
            </label>
          </div>
          <div v-for="o in ops" :key="o.id" class="ops-row" :class="o.status">
            <label v-if="o.status === 'pending' || o.status === 'failed'" class="pick">
              <input type="checkbox" :checked="selected.has(o.id)" @change="toggleSelect(o.id)" />
            </label>
            <div class="ops-main">
              <div class="ops-line">
                <span class="ops-action">{{ o.action === 'delete' ? '删除' : `移动 → ${o.targetFolder || '收件箱'}` }}</span>
                <span class="ops-status" :class="o.status">{{ statusLabel(o.status) }}</span>
              </div>
              <div class="ops-subject">{{ o.subject || o.emailId }}</div>
            </div>
            <button
              v-if="(o.status === 'pending' || o.status === 'failed') && !syncing"
              type="button"
              class="ops-sync-one"
              @click="onSyncOne(o.id)"
            >同步</button>
          </div>
        </template>
      </section>

      <section class="folder-section">
        <h2 class="section-title">目录</h2>
        <!--
          §842：有行时的失败不打断已加载内容（沿用 §840/§841 确立的两档契约：
          无行时接管整页、有行时降级为非破坏性提示）。
        -->
        <div v-if="readError && folders.length > 0" class="list-error-banner" role="alert">
          <span>{{ readErrorNotice.hint }}</span>
          <button type="button" class="banner-retry" @click="reload">{{ readErrorNotice.action }}</button>
        </div>
        <!-- §842：服务器同步目录失败但本地列表可用 ⇒ 目录可能是旧的，必须说清。 -->
        <p v-if="syncStale" class="muted sync-stale-note">
          服务器同步没成功，下面是上次同步到的目录。
        </p>
        <button v-for="f in folders" :key="f.id" type="button" class="folder-row" @click="enterFolder(f)">
          <span class="material-symbols-outlined folder-icon" aria-hidden="true">{{ folderIcon(f) }}</span>
          <span class="folder-name">{{ f.displayName }}</span>
          <span class="folder-count">{{ f.emailCount }}</span>
          <span
            v-if="f.source === 'user' || f.special === ''"
            class="material-symbols-outlined folder-del"
            role="button"
            aria-label="删除目录"
            @click.stop="onDeleteFolder(f)"
          >delete</span>
        </button>
        <!--
          §842：读失败**不得**落进下面的真空态。
          `reload()` 原本没有 catch ⇒ `folders` 停在 `[]` ⇒ 显示「还没有自定义目录」
          并提示「右上角新建目录」—— 而原记录完好，用户会被引导去做一件没用的事。
          ⚠ 两条形状要求，缺一不可：
             ① 本分支用 `v-if` 开头（下面是 `v-else-if`）—— 它前面是 `v-for` 按钮，
                `v-else-if` 没有相邻的 `v-if` 兄弟会被 Vue 编译拒绝；
             ② 条件自带 `.length === 0` 守卫 —— 门禁 `list-error-failpath` 从空态标签
                那一行**向下**读条件，且用 `/error/i` 认出「错误分支」，
                所以标志名里的 `Error` 与这条守卫都不能省（§840 已踩过一次）。
        -->
        <EmptyState
          v-if="readError && folders.length === 0"
          icon="⚠️"
          :title="readErrorNotice.title"
          :hint="readErrorNotice.hint"
          :action-label="readErrorNotice.action"
          size="sm"
          variant="inline"
          @action="reload"
        />
        <EmptyState
          v-else-if="folders.length === 0 && !loading"
          icon="📁"
          title="还没有自定义目录"
          hint="右上角新建目录，或用收件箱「智能整理」自动归集系统通知。"
          size="sm"
          variant="inline"
        />
      </section>
    </PullToRefresh>

    <BottomSheet v-model="createOpen" title="新建目录" height="auto">
      <div class="new-row">
        <input v-model="newName" class="new-input" placeholder="目录名（如：账单、订阅）" @keyup.enter="onCreate" />
        <button type="button" class="new-btn" :disabled="!newName.trim() || creating" @click="onCreate">
          {{ creating ? '创建中…' : '创建' }}
        </button>
      </div>
      <p v-if="createError" class="err">{{ createError }}</p>
      <p class="hint">目录会在邮箱服务器上真实创建（IMAP），与服务器网页版看到的一致。</p>
    </BottomSheet>

  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { EmptyState, PullToRefresh, BottomSheet } from '../../components'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'
import { useToast } from '../../composables/useToast'
import { useApiError } from '../../composables/useApiError'
import { useConfirm } from '../../composables/useConfirm'
import {
  countPendingOps, createFolder, flushEmailOps, listLocalFolders,
  listOpsEntries, removeFolder, syncFoldersFromServer,
  type LocalFolder, type LocalOpsEntry,
} from './email-folders-store'
import { setHeaderTitle } from '../../composables/useAppHeaderTitle'
import {
  isLocalDbUnavailable,
  listReadFailureNotice,
  type ListReadFailureNotice,
} from '../../utils/list-load-state'

/** 诊断前缀（§842）。 */
const FOLDER_LOAD_FAIL_TAG = '[email-folders] 读取目录失败：'
const FOLDER_LOAD_WARN_TAG = '[email-folders] 服务器同步目录失败：'

defineOptions({ name: 'EmailFolderListView' })

const router = useRouter()
const toast = useToast()
const { confirm } = useConfirm()
const apiError = useApiError()

const folders = ref<LocalFolder[]>([])
const ops = ref<LocalOpsEntry[]>([])
const pendingCount = ref(0)
const loading = ref(true)
const syncing = ref(false)
const selected = ref(new Set<string>())
const selectAll = ref(false)
const createOpen = ref(false)
const creating = ref(false)
const newName = ref('')
const createError = ref('')
/**
 * §842：reload 的失败说明，**整份 notice 存一个来源**。
 * ⚠ 刻意叫 `readError` 而不是 `readFailure`：门禁 `list-error-failpath` 用
 *   `/error/i` 认出「错误分支」再查它的行数守卫 ⇒ 名字里没有 `error` 一词的话，
 *   这条分支会被判据**整体跳过**，守卫被拿掉也没人喊（§840 已在会议侧踩过一次）。
 * title / hint / action 由 `listReadFailureNotice` 统一产出，模板不再自己拼 ——
 * 否则「无行」和「有行」两处一旦各拼一次就会漂移（§835 的教训）。
 */
const readError = ref<ListReadFailureNotice | null>(null)
/**
 * 模板绑定用：永远非空。
 *
 * ⚠ `vue-tsc` **不会**用 `v-else-if="readError && …"` 收窄后续属性里的 `readError`
 *   ⇒ 直接绑 `readError.title` 会报 TS18047（possibly null）。
 *   失败分支渲染时它必然非 null；没有失败时这份占位根本不会被渲染。
 */
const readErrorNotice = computed<ListReadFailureNotice>(
  () => readError.value ?? listReadFailureNotice('邮件目录'),
)
/** §842：服务器同步目录失败但本地列表可用 —— 目录可能是旧的。 */
const syncStale = ref(false)
const pickerAccountId = ref('')

const selectableIds = computed(() =>
  ops.value.filter((o) => o.status === 'pending' || o.status === 'failed').map((o) => o.id),
)

/**
 * §842：原来这个函数**只有 `finally` 没有 `catch`**，而 `listLocalFolders()`
 * 走 `localDB.query()`（`email-folders-store.ts:31`）⇒ `LocalDB 未初始化` 时
 * 异常直接逃出去：
 *   ① `onMounted` 的 `void reload()` ⇒ **未处理 rejection**；
 *   ② `folders` 停在 `[]`、`loading` 归 false ⇒ 模板 `folders.length === 0 && !loading`
 *      为真 ⇒ 显示「还没有自定义目录」并**引导用户去新建目录**（原记录完好）；
 *   ③ 三个调用方里，新建/删除两条路径会因此给出**与真实结果相反的提示** ——
 *      新建：`toast.success(已创建)` 之后抛 ⇒ 写进**已关闭**的面板，用户看不到；
 *      删除：先 `toast.success(已删除)` 再抛 ⇒ catch 再弹一条 `toast.error(删除失败)`
 *      ⇒ 同一次点击弹出两条互相矛盾的话。
 *
 * ⇒ 自己兜住，`reload()` **不再向外抛** ⇒ ①②③ 一并消失（③ 是自动解掉的，
 *    不是额外打的补丁：调用方的 catch 根本不会再被触发）。
 */
async function reload() {
  loading.value = true
  readError.value = null
  try {
    try {
      await syncFoldersFromServer()
      syncStale.value = false
    } catch (e) {
      // ⚠ 原来这里是一句「吞掉服务器同步失败」的空回调（§838 明令禁止的形状）。
      //   失败零痕迹 ⇒ 用户以为目录是最新的。
      syncStale.value = true
      console.warn(FOLDER_LOAD_WARN_TAG, '服务器同步目录失败，下面是上次同步到的', e)
    }
    folders.value = await listLocalFolders()
    ops.value = await listOpsEntries()
    pendingCount.value = await countPendingOps()
  } catch (e: unknown) {
    readError.value = listReadFailureNotice(
      '邮件目录',
      isLocalDbUnavailable(e) ? { dbUnavailable: true } : {},
    )
    console.error(FOLDER_LOAD_FAIL_TAG, '邮件目录读取失败', e)
  } finally {
    loading.value = false
  }
}

function statusLabel(s: LocalOpsEntry['status']): string {
  switch (s) {
    case 'pending': return '待同步'
    case 'pushed': return '已推送待确认'
    case 'applied': return '已同步'
    case 'failed': return '失败'
  }
}

function folderIcon(f: LocalFolder): string {
  switch (f.special) {
    case 'trash': return 'delete'
    case 'junk': return 'report'
    case 'sent': return 'send'
    case 'drafts': return 'draft'
    default: return 'folder'
  }
}

function enterFolder(f: LocalFolder) {
  router.push({ path: '/email', query: { folder: f.name } })
}

async function onCreate() {
  const name = newName.value.trim()
  if (!name || creating.value) return
  creating.value = true
  createError.value = ''
  try {
    if (!pickerAccountId.value) {
      const { listAccounts } = await import('./emails-store')
      pickerAccountId.value = (await listAccounts())[0]?.id || ''
    }
    if (!pickerAccountId.value) throw new Error('没有可用邮箱账户')
    await createFolder(pickerAccountId.value, name)
    newName.value = ''
    createOpen.value = false
    toast.success(`目录「${name}」已创建`)
    await reload()
  } catch (e: any) {
    createError.value = apiError(e, 'errors.operateFailed')
  } finally {
    creating.value = false
  }
}

async function onDeleteFolder(f: LocalFolder) {
  // §854：原为 window.confirm（同 EmailInboxView 的两处）。项目已确立 ConfirmDialog
  // 为全局唯一确认弹窗，这处是第三处漏网的；它在 Android WebView 里同步阻塞、
  // 会卡死渲染进程（BUG-AQ）。注意这函数本身已有 try/catch + toast，
  // 缺的只有确认弹窗这一步。
  if (!await confirm({
    title: '删除目录',
    message: `删除目录「${f.displayName}」？目录里的邮件会回到收件箱视图，服务器目录本身不受影响。`,
    confirmText: '删除',
    danger: true,
  })) return
  try {
    await removeFolder(f)
    toast.success('目录已删除')
    await reload()
  } catch (e: any) {
    toast.error(apiError(e, 'errors.operateFailed'))
  }
}

function toggleSelect(id: string) {
  const next = new Set(selected.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  selected.value = next
}

function toggleSelectAll() {
  selected.value = selectAll.value ? new Set(selectableIds.value) : new Set()
}

async function runSync(keys?: string[]) {
  syncing.value = true
  try {
    const rep = await flushEmailOps(keys?.length ? { ids: keys } : {})
    if (rep.pushed === 0 && rep.applied === 0 && rep.failed === 0) {
      toast.info('没有待同步的操作')
    } else if (rep.failed > 0 || rep.errors.length > 0) {
      toast.error(`同步完成：成功 ${rep.applied}，失败 ${rep.failed}`)
    } else {
      toast.success(`已同步 ${rep.applied} 项操作到服务器`)
    }
  } catch (e: any) {
    toast.error(apiError(e, 'errors.operateFailed'))
  } finally {
    syncing.value = false
    await reload()
  }
}

/** 全量：本地所有 pending（服务端幂等去重，重放安全）。 */
function onSyncAll() {
  if (!pendingCount.value) return
  void runSync()
}

/** 可选：只同步勾选（或单条）的 pending 操作。 */
function onSyncOne(id: string) {
  void runSync([id])
}

onMounted(() => {
  void reload()
  setHeaderTitle('邮件目录')
})
</script>

<style scoped>
.folders-page { display: flex; flex-direction: column; height: 100%; min-height: 0; }
.folders-scroll { flex: 1; min-height: 0; }
.sync-card { margin: var(--space-3); padding: var(--space-3); background: var(--bg-card); border: 1px solid var(--border); border-radius: var(--radius-md); }
.sync-row { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.sync-info { display: flex; align-items: center; gap: 8px; font-size: var(--text-base); }
.badge { font-size: var(--text-2xs); padding: 1px 8px; border-radius: var(--radius-full); background: var(--warning); color: #fff; }
.muted { font-size: var(--text-sm); color: var(--text-muted); }
/* §842：与 SessionListView / EmailInboxView 的 .list-error-banner 同源，
   三处可对照。本文件是 scoped 样式，不复制一份就没有任何视觉。 */
.sync-stale-note { margin: 0 0 var(--space-2); }
.list-error-banner {
  display: flex; align-items: center; justify-content: space-between;
  gap: var(--space-2);
  background: var(--danger-bg); color: var(--danger);
  padding: var(--space-2) var(--space-3);
  font-size: var(--text-sm);
  border-radius: var(--radius-sm);
  margin-bottom: var(--space-2);
}
.banner-retry {
  flex: 0 0 auto;
  background: transparent; color: inherit;
  border: 1px solid currentColor; border-radius: var(--radius-sm);
  padding: 2px var(--space-2);
  font-size: var(--text-sm); cursor: pointer;
}
.sync-btn { min-height: 34px; padding: 0 14px; border: none; border-radius: var(--radius-sm); background: var(--brand-primary); color: var(--text-inverse); }
.sync-btn:disabled { opacity: .5; }
.ops-head { display: flex; justify-content: space-between; align-items: center; margin-top: var(--space-3); font-size: var(--text-sm); color: var(--text-muted); }
.pick-all { display: flex; align-items: center; gap: 4px; }
.ops-row { display: flex; align-items: center; gap: 8px; padding: 8px 0; border-bottom: 1px solid var(--border); }
.ops-row:last-child { border-bottom: none; }
.pick { display: flex; align-items: center; }
.ops-main { flex: 1; min-width: 0; }
.ops-line { display: flex; justify-content: space-between; gap: 8px; font-size: var(--text-smd); }
.ops-action { color: var(--text-primary); }
.ops-status { font-size: var(--text-2xs); }
.ops-status.pending { color: var(--warning); }
.ops-status.failed { color: var(--danger); }
.ops-status.applied { color: var(--success, var(--brand-primary)); }
.ops-status.pushed { color: var(--text-secondary); }
.ops-subject { font-size: var(--text-sm); color: var(--text-muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ops-sync-one { border: 1px solid var(--border); background: var(--bg-card); color: var(--brand-primary); border-radius: var(--radius-sm); font-size: var(--text-sm); padding: 3px 10px; }
.folder-section { margin: 0 var(--space-3) var(--space-6); }
.section-title { font-size: var(--text-smd); color: var(--text-muted); margin: var(--space-3) 0 var(--space-2); font-weight: 600; }
.folder-row { display: flex; align-items: center; gap: 10px; width: 100%; padding: 12px; background: var(--bg-card); border: 1px solid var(--border); border-radius: var(--radius-md); margin-bottom: 8px; cursor: pointer; text-align: left; }
.folder-icon { color: var(--brand-primary); font-size: 20px; }
.folder-name { flex: 1; min-width: 0; font-size: var(--text-base); color: var(--text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.folder-count { font-size: var(--text-sm); color: var(--text-muted); }
.folder-del { color: var(--text-muted); font-size: var(--text-xl); padding: 4px; }
.new-row { display: flex; gap: 8px; padding: 4px 0 8px; }
.new-input { flex: 1; min-height: 38px; border: 1px solid var(--border); border-radius: var(--radius-sm); padding: 0 10px; background: var(--bg-card); color: var(--text-primary); font-size: var(--text-base); }
.new-btn { min-height: 38px; padding: 0 14px; border: none; border-radius: var(--radius-sm); background: var(--brand-primary); color: var(--text-inverse); }
.new-btn:disabled { opacity: .5; }
.err { color: var(--danger); font-size: var(--text-sm); margin: 4px 0 0; }
.hint { font-size: var(--text-sm); color: var(--text-muted); margin: 8px 0 0; }
</style>
