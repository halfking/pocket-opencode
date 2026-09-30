<!--
  PkmNoteView.vue — 通用笔记编辑页 /pkm/n/:id（含 new）。

  - 加载/新建笔记 → PkmEditor 编辑
  - editor 点击 wikilink → useWikilinkNav 跳转/创建
  - 底部 BacklinksPanel 显示反向链接（保存后刷新）
-->
<template>
      <div class="pkm-note-view">
      <div v-if="loading" class="state">加载中…</div>
      <template v-else-if="noteId">
        <PkmEditor
          :key="noteId"
          :note-id="noteId"
          @navigate="onNavigate"
          @saved="onSaved"
        />
        <BacklinksPanel
          :target-title="currentTitle"
          :workspace-id="currentWorkspaceId()"
          :refresh-key="refreshTick"
          @open="openNote"
        />
      </template>
    </div>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import PkmEditor from './PkmEditor.vue'
import BacklinksPanel from './BacklinksPanel.vue'
import { getNote, saveNote, type PkmNote } from './pkm-store'
import { useWikilinkNav } from './use-wikilink-nav'
import { markListDirty } from '../../composables/list-scene-store'
import { useAuthStore } from '../../stores/auth'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const { navigate } = useWikilinkNav()

/**
 * 资产表 local_assets 按 workspace_id 分区。写入必须显式带上当前登录 workspace，
 * 否则 asset-store.ts:114 的 `?? 'default'` 会把行落到 default 分区，而列表页
 * （PkmTodayView）按 auth.workspaceId 读 —— 表现为「保存成功但列表看不到」。
 * 与 NoteListView / NoteEditView 的同名函数保持一致。
 */
function currentWorkspaceId(): string {
  return auth.workspaceId || 'default'
}

const loading = ref(true)
const noteId = ref('')
const currentTitle = ref('')
const refreshTick = ref(0) // 保存后递增，触发 BacklinksPanel 重查

const rawId = computed(() => route.params.id as string)

async function loadOrCreate(id: string) {
  loading.value = true
  if (id === 'new' || !id) {
    // 新建空笔记
    const created = await saveNote({ title: '无标题', html: '', workspaceId: currentWorkspaceId() })
    router.replace(`/pkm/n/${created.id}`)
    noteId.value = created.id
    currentTitle.value = created.title
  } else {
    const note = await getNote(id)
    if (!note) {
      // 不存在 → 回 Today
      router.replace('/pkm/today')
      return
    }
    noteId.value = note.id
    currentTitle.value = note.title
  }
  loading.value = false
}

function onSaved(note: PkmNote) {
  currentTitle.value = note.title
  refreshTick.value++
  markListDirty('pkm-today')
}

async function onNavigate(target: string) {
  await navigate(target)
}

function openNote(id: string) {
  router.push(`/pkm/n/${id}`)
}

watch(rawId, (id) => {
  if (id) loadOrCreate(id)
}, { immediate: true })
</script>

<style scoped>
.pkm-note-view {
  display: flex;
  flex-direction: column;
  min-height: 100%;
}
.state {
  padding: 40px;
  text-align: center;
  color: var(--text-secondary, #888);
}
</style>
