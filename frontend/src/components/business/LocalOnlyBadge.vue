<template>
  <!--
    LocalOnlyBadge — 「仅本地 · 不同步」小横条。
    模块在 syncPolicyStore 里被切到 local 时渲染（v-if 由本组件内部判断，
    调用方只需传 module）。纯展示组件，圆角矩形（设计约束：禁圆点/圆形按钮）。
  -->
  <span v-if="isLocal" class="local-only-badge" role="status">
    <span class="material-symbols-outlined" aria-hidden="true">smartphone</span>
    {{ t('settings.storageOnlyLocalBadge') }}
    <span v-if="module === 'rss' || module === 'email'">{{ t('settings.storageLocalCloudUnavailable') }}</span>
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useSyncPolicyStore } from '../../stores/syncPolicyStore'
import type { SyncModule } from '../../api/syncPolicyContract.ts'

const props = defineProps<{
  /** 模块标识（rss / email / notes(pkm) / meetings …），与 sync-policy 契约一致。 */
  module: SyncModule
}>()

const { t } = useI18n()
const policy = useSyncPolicyStore()

const isLocal = computed(() => policy.modules[props.module] === 'local')
</script>

<style scoped>
.local-only-badge {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 3px var(--space-2);
  border-radius: var(--radius-sm);
  background: var(--warning-bg);
  color: var(--warning);
  font-size: var(--text-xs);
  font-weight: var(--font-weight-semibold);
  line-height: 1.4;
  flex-wrap: wrap;
}

.local-only-badge .material-symbols-outlined {
  font-size: var(--text-base);
}
</style>
