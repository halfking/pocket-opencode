<!--
  PkmNoteView.vue — 通用笔记编辑页 /pkm/n/:id（含 new）。

  - 加载/新建笔记 → PkmEditor 编辑
  - editor 点击 wikilink → useWikilinkNav 跳转/创建
  - 底部 BacklinksPanel 显示反向链接（保存后刷新）
-->
<template>
  <AppLayout>
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
          :refresh-key="refreshTick"
          @open="openNote"
        />
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AppLayout from '../../app/AppLayout.vue'
import PkmEditor from './PkmEditor.vue'
import BacklinksPanel from './BacklinksPanel.vue'
import { getNote, saveNote, type PkmNote } from './pkm-store'
import { useWikilinkNav } from './use-wikilink-nav'

const route = useRoute()
const router = useRouter()
const { navigate } = useWikilinkNav()

const loading = ref(true)
const noteId = ref('')
const currentTitle = ref('')
const refreshTick = ref(0) // 保存后递增，触发 BacklinksPanel 重查

const rawId = computed(() => route.params.id as string)

async function loadOrCreate(id: string) {
  loading.value = true
  if (id === 'new' || !id) {
    // 新建空笔记
    const created = await saveNote({ title: '无标题', html: '' })
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
