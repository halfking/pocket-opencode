<script setup lang="ts">
/**
 * SessionWorkspaceView — P2 横屏/平板 master-detail 工作台（E5-S1）。
 *
 * <840px：只呈现列表，选择后沿用 /sessions/:id 单栏详情路由。
 * ≥840px：SplitLayout 同屏显示列表 + 会话详情；选择写入 query，页面刷新可恢复。
 */
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import SplitLayout from '../../components/SplitLayout.vue'
import { useBreakpoint } from '../../composables/useBreakpoint'
import { isLobsterReady, lobsterReady } from '../../native/lobster-init'
import SessionListView from './SessionListView.vue'
import SessionConversationView from './SessionConversationView.vue'

interface SelectedSession {
  id: string
  title: string
}

const route = useRoute()
const router = useRouter()
const { isFoldableExpanded } = useBreakpoint()

const selectedId = computed(() => String(route.query.selected || ''))
const selectedTitle = computed(() => String(route.query.title || ''))
const selectedInstance = computed(() => String(route.query.instance_id || ''))
// 窄屏不挂载 detail（SplitLayout 也只是 display:none）：避免后台残留 store
// 会话、WS/审批轮询照跑。选中 query 与断点解耦，宽窄切换不残留错配。
const showDetailPane = computed(() => isFoldableExpanded.value && selectedId.value !== '')
// 读响应式 lobsterReady：工作台挂载后才解锁（登录回跳）时 detail 也能自动挂上
const detailReady = computed(() => showDetailPane.value && lobsterReady.value)
const workspaceHasDetail = computed(() => selectedId.value !== '')

function clearSelection(instanceId: string): void {
  if (!selectedId.value) return
  void router.replace({
    path: '/sessions',
    query: instanceId ? { instance_id: instanceId } : {},
  })
}

function selectSession(session: SelectedSession, instanceId: string): void {
  if (!isLobsterReady()) {
    void router.push({
      path: '/login',
      query: { returnTo: route.fullPath, unlock: '1' },
    })
    return
  }
  if (!isFoldableExpanded.value) {
    void router.push({
      path: `/sessions/${session.id}`,
      query: { instance_id: instanceId, title: session.title || '' },
    })
    return
  }
  void router.replace({
    path: '/sessions',
    query: {
      selected: session.id,
      instance_id: instanceId,
      title: session.title || '',
    },
  })
}
</script>

<template>
  <SplitLayout class="session-workspace" :class="{ 'has-detail': workspaceHasDetail }">
    <template #master>
      <SessionListView embedded @select="selectSession" @context-change="clearSelection" />
    </template>

    <template #detail>
      <SessionConversationView
        v-if="detailReady"
        :key="`${selectedInstance}:${selectedId}`"
        embedded
        :session-id="selectedId"
        :instance-id="selectedInstance"
        :title="selectedTitle"
        @close="clearSelection(selectedInstance)"
      />
      <div v-else-if="showDetailPane" class="detail-empty" role="status">
        <span class="material-symbols-outlined" aria-hidden="true">lock</span>
        <h2>工作区已锁定</h2>
        <p>请先解锁本地工作区后查看会话详情。</p>
      </div>
      <div v-else class="detail-empty">
        <span class="material-symbols-outlined" aria-hidden="true">forum</span>
        <h2>选择一个会话</h2>
        <p>会话内容会在此处打开，列表与详情可独立滚动。</p>
      </div>
    </template>
  </SplitLayout>
</template>

<style scoped>
.session-workspace {
  height: 100%;
  min-height: 0;
}

.session-workspace :deep(.master-pane),
.session-workspace :deep(.detail-pane) {
  height: 100%;
  min-height: 0;
  overflow: auto;
}

.detail-empty {
  display: grid;
  height: 100%;
  min-height: 360px;
  place-content: center;
  padding: var(--space-6);
  color: var(--text-secondary);
  text-align: center;
}

.detail-empty .material-symbols-outlined {
  margin: 0 auto var(--space-2);
  font-size: 40px;
  color: var(--text-muted);
}

.detail-empty h2 {
  margin: 0;
  color: var(--text-primary);
  font-size: var(--text-lg);
}

.detail-empty p {
  margin: var(--space-2) 0 0;
  font-size: var(--text-base);
}

@media (max-width: 839px) {
  .session-workspace {
    height: auto;
    min-height: 0;
  }

  .session-workspace :deep(.master-pane) {
    overflow: visible;
  }
}
</style>
