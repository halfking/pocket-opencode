<template>
  <!--
    StorageSyncSection — 设置页「存储与同步」分组（设计 03 §8 / 04 §4）。

    - 说明条：云端 = 统一存 RedClaw 后台；仅本地 = 只留本机、同步跳过。
    - 六个模块行：图标容器 + 名称 + 说明 + 「云端 | 仅本地」分段选择器。
      全部圆角矩形（设计约束：禁圆形按钮 / 圆点单选）。
    - 切「仅本地」时弹确认；确认框内含「清除云端副本」次级操作占位
      （POST /api/v1/portal/cloud/purge），失败 toast。
    - 「上次同步」只读报告区：渲染 syncPolicyStore.lastSyncReport，
      无数据显示「尚未同步」。
    - 策略读写在 syncPolicyStore；远端不可达时 store 自动降级
      本地缓存/默认全 cloud，本区块永远可渲染、可操作（不阻塞 UI）。
  -->
  <div class="storage-sync-section" data-testid="storage-sync-section">
    <!-- 说明条 -->
    <div class="storage-intro">
      <span class="material-symbols-outlined" aria-hidden="true">privacy_tip</span>
      <span>{{ t('settings.storageIntro') }}</span>
    </div>

    <!-- 按模块设置数据去向 -->
    <div
      v-for="row in moduleRows"
      :key="row.module"
      class="storage-module"
      :data-testid="`storage-module-row-${row.module}`"
    >
      <div class="module-icon" :class="`module-icon-${row.module}`">
        <span class="material-symbols-outlined" aria-hidden="true">{{ row.icon }}</span>
      </div>
      <div class="module-content">
        <div class="module-name">{{ t(row.nameKey) }}</div>
        <div class="module-desc">{{ t(row.descKey) }}</div>
      </div>
      <div class="segment" role="radiogroup" :aria-label="t(row.nameKey)">
        <button
          v-for="mode in (['cloud', 'local'] as SyncMode[])"
          :key="mode"
          type="button"
          role="radio"
          :aria-checked="policy.modules[row.module] === mode"
          :class="['segment-item', `segment-${mode}`, { active: policy.modules[row.module] === mode }]"
          :disabled="!policy.allowOverride"
          :data-testid="`storage-mode-${row.module}-${mode}`"
          @click="onSegmentTap(row.module, mode)"
        >
          {{ mode === 'cloud' ? t('settings.storageModeCloud') : t('settings.storageModeLocal') }}
        </button>
      </div>
    </div>

    <!-- 上次同步（只读报告） -->
    <div class="sync-report">
      <div class="report-head">
        <span class="report-title">{{ t('settings.storageLastSyncTitle') }}</span>
        <span class="report-time">{{ reportTimeLabel }}</span>
      </div>
      <template v-if="policy.lastSyncReport.length > 0">
        <div
          v-for="row in reportRows"
          :key="row.module"
          class="report-row"
          :data-testid="`storage-report-${row.module}`"
        >
          <span :class="['report-dot', row.entry ? row.entry.status : 'none']"></span>
          <span class="report-module">{{ moduleLabel(row.module) }}</span>
          <span v-if="!row.entry" class="report-none">—</span>
          <span v-else-if="row.entry.status === 'skipped'" class="report-tag skipped">
            {{ t('settings.storageStatusSkipped') }}
          </span>
          <span v-else class="report-tag synced">
            {{
              row.entry.count !== undefined
                ? t('settings.storageSyncedCount', { count: row.entry.count })
                : t('settings.storageStatusSynced')
            }}
          </span>
        </div>
      </template>
      <div v-else class="report-empty">{{ t('settings.storageNeverSynced') }}</div>
    </div>

    <div v-if="policy.loadError" class="storage-load-error">
      {{ t('settings.storageLoadFailed') }}
    </div>

    <!-- 切「仅本地」确认框（含清除云端副本次级操作占位） -->
    <div v-if="confirmFor" class="confirm-mask" @click.self="confirmFor = null">
      <div class="confirm-card" role="alertdialog" :aria-label="t('settings.storageConfirmTitle')">
        <h3>{{ t('settings.storageConfirmTitle') }}</h3>
        <p>{{ t('settings.storageConfirmMessage', { module: moduleLabel(confirmFor) }) }}</p>
        <div class="confirm-actions">
          <button type="button" class="confirm-btn secondary" @click="confirmFor = null">
            {{ t('common.cancel') }}
          </button>
          <button
            type="button"
            class="confirm-btn secondary danger"
            :disabled="purging"
            :data-testid="`storage-purge-${confirmFor}`"
            @click="confirmPurgeAndSwitch"
          >
            {{ purging ? t('settings.storagePurging') : t('settings.storageConfirmPurgeAndSwitch') }}
          </button>
          <button
            type="button"
            class="confirm-btn primary"
            data-testid="storage-confirm-switch"
            @click="confirmSwitchOnly"
          >
            {{ t('settings.storageConfirmSwitch') }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from '../../composables/useToast'
import { purgeCloudCopy } from '../../api/syncPolicy'
import type { SyncMode, SyncModule } from '../../api/syncPolicyContract.ts'
import { useSyncPolicyStore } from '../../stores/syncPolicyStore'
import { ICON, type IconName } from '../../constants/icons'

const { t } = useI18n()
const toast = useToast()
const policy = useSyncPolicyStore()

interface ModuleRow {
  module: SyncModule
  icon: IconName
  nameKey: string
  descKey: string
}

/** 六模块行（顺序 = 设计稿：AI 对话/邮件/RSS/笔记·PKM/会议记录/任务）。 */
const moduleRows: ModuleRow[] = [
  { module: 'ai-chat', icon: ICON.storageSyncChat, nameKey: 'settings.storageModuleAiChat', descKey: 'settings.storageModuleAiChatDesc' },
  { module: 'email', icon: ICON.storageSyncEmail, nameKey: 'settings.storageModuleEmail', descKey: 'settings.storageModuleEmailDesc' },
  { module: 'rss', icon: ICON.storageSyncRss, nameKey: 'settings.storageModuleRss', descKey: 'settings.storageModuleRssDesc' },
  { module: 'notes', icon: ICON.storageSyncNotes, nameKey: 'settings.storageModuleNotes', descKey: 'settings.storageModuleNotesDesc' },
  { module: 'meetings', icon: ICON.storageSyncMeetings, nameKey: 'settings.storageModuleMeetings', descKey: 'settings.storageModuleMeetingsDesc' },
  { module: 'tasks', icon: ICON.storageSyncTasks, nameKey: 'settings.storageModuleTasks', descKey: 'settings.storageModuleTasksDesc' },
]

const rowByModule = new Map(moduleRows.map((r) => [r.module, r]))

/** 待确认切换为 local 的模块（非空 = 确认框打开）。 */
const confirmFor = ref<SyncModule | null>(null)
const purging = ref(false)

onMounted(() => {
  // 远端失败由 store 内部降级（缓存/默认全 cloud），这里不 catch 不阻塞。
  if (!policy.loaded) void policy.load()
})

/** 报告行：固定六模块顺序；缺失条目的行显示「—」（不谎称已同步/已跳过）。 */
const reportRows = computed(() =>
  moduleRows.map((r) => ({
    module: r.module,
    entry: policy.lastSyncReport.find((e) => e.module === r.module) ?? null,
  })),
)

const hasAnyReport = computed(() => policy.lastSyncReport.length > 0)

const reportTimeLabel = computed(() => {
  if (!hasAnyReport.value) return t('settings.storageNeverSynced')
  const latest = Math.max(...policy.lastSyncReport.map((e) => e.at || 0))
  if (!latest) return t('settings.storageNeverSynced')
  return new Date(latest).toLocaleString()
})

function moduleLabel(module: SyncModule): string {
  return t(rowByModule.get(module)?.nameKey ?? 'settings.storageModuleAiChat')
}

/** 分段点击：cloud→local 先弹确认；其余直接走 store（乐观更新+失败回滚）。 */
function onSegmentTap(module: SyncModule, mode: SyncMode): void {
  if (policy.modules[module] === mode) return
  if (!policy.allowOverride) {
    toast.warning(t('settings.storageManagedNoOverride'))
    return
  }
  if (mode === 'local') {
    confirmFor.value = module
    return
  }
  void applySwitch(module, 'cloud')
}

async function applySwitch(module: SyncModule, mode: SyncMode): Promise<boolean> {
  try {
    await policy.setModule(module, mode)
    return true
  } catch (err) {
    // store 已回滚；这里只负责让用户看见。
    toast.error(
      t('settings.storageSwitchFailed') + (err instanceof Error ? `：${err.message}` : ''),
    )
    return false
  }
}

/** 主操作：仅切换（云端副本保留，之后仍可手动清除）。 */
async function confirmSwitchOnly(): Promise<void> {
  const module = confirmFor.value
  if (!module) return
  confirmFor.value = null
  await applySwitch(module, 'local')
}

/** 次级操作占位：先 POST /api/v1/portal/cloud/purge 清云端副本，再切换。 */
async function confirmPurgeAndSwitch(): Promise<void> {
  const module = confirmFor.value
  if (!module) return
  purging.value = true
  try {
    await purgeCloudCopy([module])
    toast.success(t('settings.storagePurgeDone'))
    confirmFor.value = null
    await applySwitch(module, 'local')
  } catch (err) {
    // 清除失败：不切换（云端副本状态未知，不能当「已清除」处理），toast 告知。
    toast.error(
      t('settings.storagePurgeFailed', { reason: err instanceof Error ? err.message : String(err) }),
    )
  } finally {
    purging.value = false
  }
}
</script>

<style scoped>
.storage-sync-section {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

/* 说明条（圆角矩形信息条） */
.storage-intro {
  display: flex;
  gap: var(--space-2);
  padding: var(--space-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--brand-bg);
  color: var(--text-secondary);
  font-size: var(--text-xs);
  line-height: 1.7;
}

.storage-intro .material-symbols-outlined {
  font-size: 16px;
  color: var(--brand-primary);
  flex-shrink: 0;
  margin-top: 2px;
}

/* 模块行 */
.storage-module {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-2) 0;
  border-bottom: 1px solid var(--border);
}

.storage-module:last-of-type {
  border-bottom: none;
}

.module-icon {
  width: 36px;
  height: 36px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-sm);
  background: var(--bg-subtle);
  color: var(--brand-primary);
  flex-shrink: 0;
}

/* 六模块图标色（跟随设计稿的模块色相，落在主题变量上保持亮暗一致） */
.module-icon-ai-chat { color: var(--brand-primary); }
.module-icon-email { color: var(--warning); }
.module-icon-rss { color: var(--success); }
.module-icon-notes { color: var(--success); }
.module-icon-meetings { color: var(--brand-primary); }
.module-icon-tasks { color: var(--danger); }

.module-icon .material-symbols-outlined {
  font-size: 20px;
}

.module-content {
  flex: 1;
  min-width: 0;
}

.module-name {
  font-size: var(--text-base);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
}

.module-desc {
  font-size: var(--text-xs);
  color: var(--text-muted);
  margin-top: 2px;
}

/* 分段选择器：外容器 + 两个圆角矩形按钮（非圆形/非圆点） */
.segment {
  display: flex;
  gap: var(--space-1);
  padding: 3px;
  border-radius: var(--radius-md);
  background: var(--bg-subtle);
  border: 1px solid var(--border);
  flex-shrink: 0;
}

.segment-item {
  min-width: 56px;
  padding: var(--space-1) var(--space-2);
  border: none;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--text-secondary);
  font-size: var(--text-xs);
  cursor: pointer;
  transition: background var(--duration-fast) var(--ease-out),
    color var(--duration-fast) var(--ease-out);
}

.segment-item:disabled {
  cursor: not-allowed;
  opacity: 0.6;
}

.segment-item.segment-cloud.active {
  background: var(--brand-primary);
  color: var(--text-inverse);
  font-weight: var(--font-weight-semibold);
}

.segment-item.segment-local.active {
  background: var(--warning);
  color: var(--text-inverse);
  font-weight: var(--font-weight-semibold);
}

/* 上次同步报告（只读） */
.sync-report {
  border-top: 1px solid var(--border);
  padding-top: var(--space-3);
}

.report-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-2);
}

.report-title {
  font-size: var(--text-sm);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
}

.report-time {
  font-size: var(--text-xs);
  color: var(--text-muted);
}

.report-row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-1) 0;
  font-size: var(--text-sm);
  color: var(--text-secondary);
}

/* 状态点：小方块（圆角矩形），不用圆点 */
.report-dot {
  width: 8px;
  height: 8px;
  border-radius: 3px;
  flex-shrink: 0;
}

.report-dot.synced { background: var(--success); }
.report-dot.skipped { background: var(--warning); }
.report-dot.none { background: var(--border); }

.report-none {
  font-size: var(--text-xs);
  color: var(--text-muted);
}

.report-module {
  flex: 1;
}

.report-tag {
  font-size: var(--text-xs);
  font-weight: var(--font-weight-semibold);
  padding: 2px var(--space-2);
  border-radius: var(--radius-sm);
}

.report-tag.synced {
  background: var(--success-bg);
  color: var(--success);
}

.report-tag.skipped {
  background: var(--warning-bg);
  color: var(--warning);
}

.report-empty {
  font-size: var(--text-sm);
  color: var(--text-muted);
  padding: var(--space-2) 0;
}

/* 策略服务不可达提示（非阻塞） */
.storage-load-error {
  font-size: var(--text-xs);
  color: var(--warning);
  background: var(--warning-bg);
  border-radius: var(--radius-sm);
  padding: var(--space-2);
}

/* 确认框遮罩 + 卡片 */
.confirm-mask {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.45);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
  padding: var(--space-4);
}

.confirm-card {
  width: 100%;
  max-width: 360px;
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  padding: var(--space-4);
}

.confirm-card h3 {
  margin: 0 0 var(--space-2) 0;
  font-size: var(--text-base);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
}

.confirm-card p {
  margin: 0 0 var(--space-3) 0;
  font-size: var(--text-sm);
  color: var(--text-secondary);
  line-height: 1.7;
}

.confirm-actions {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.confirm-btn {
  width: 100%;
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  border: none;
  font-size: var(--text-sm);
  font-weight: var(--font-weight-semibold);
  cursor: pointer;
}

.confirm-btn.primary {
  background: var(--brand-gradient);
  color: var(--text-inverse);
}

.confirm-btn.secondary {
  background: var(--bg-subtle);
  color: var(--text-secondary);
  border: 1px solid var(--border);
}

.confirm-btn.secondary.danger {
  background: var(--danger-bg);
  color: var(--danger);
  border-color: var(--border);
}

.confirm-btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}
</style>
