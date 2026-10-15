<template>
  <BottomSheet :open="open" title="声纹库" @close="onClose">
    <div class="vp-sheet">
      <p class="hint">
        这些声纹保存在本机，之后每场录音都会用它们自动认人。
        <strong>认错了会一直沿用</strong>，所以认错时在这里删掉即可。
      </p>

      <div v-if="loading" class="state">载入中…</div>

      <div v-else-if="items.length === 0" class="state empty">
        还没有声纹。录音时点「说话人」给某个人起名，就会自动存在这里。
      </div>

      <ul v-else class="vp-list">
        <li v-for="vp in items" :key="vp.id" class="vp-row">
          <template v-if="editingId === vp.id">
            <input
              v-model="draft"
              class="edit-input"
              placeholder="显示名称"
              @keyup.enter="commitRename(vp.id)"
              @keyup.esc="editingId = ''"
            />
            <button type="button" class="mini primary" @click="commitRename(vp.id)">保存</button>
            <button type="button" class="mini" @click="editingId = ''">取消</button>
          </template>
          <template v-else>
            <span class="name">{{ vp.displayName }}</span>
            <span class="since">建于 {{ formatWhen(vp.createdAt) }}</span>
            <button type="button" class="mini" @click="startRename(vp)">改名</button>
            <button type="button" class="mini danger" @click="onDelete(vp)">删除</button>
          </template>
        </li>
      </ul>

      <button
        v-if="items.length > 0"
        type="button"
        class="clear-all"
        :disabled="busy"
        @click="onClearAll"
      >清空全部（{{ items.length }}）</button>
    </div>
  </BottomSheet>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { BottomSheet } from '@/components'
import { useConfirm } from '../../composables/useConfirm'
import {
  clearVoiceprints, deleteVoiceprint, listVoiceprints, renameVoiceprint,
  type LocalVoiceprint,
} from './voiceprints-store'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ close: [] }>()

const { confirm } = useConfirm()

const items = ref<LocalVoiceprint[]>([])
const loading = ref(false)
const busy = ref(false)
const editingId = ref('')
const draft = ref('')

function formatWhen(ts: number): string {
  return new Date(ts).toLocaleString('zh-CN', {
    month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit',
  })
}

async function load() {
  loading.value = true
  try {
    items.value = await listVoiceprints()
  } catch (e) {
    items.value = []
  } finally {
    loading.value = false
  }
}

watch(() => props.open, (v) => {
  if (!v) return
  editingId.value = ''
  draft.value = ''
  void load()
})

function startRename(vp: LocalVoiceprint) {
  editingId.value = vp.id
  draft.value = vp.displayName
}

async function commitRename(id: string) {
  const name = draft.value.trim()
  if (!name) return
  busy.value = true
  try {
    await renameVoiceprint(id, name)
    editingId.value = ''
    await load()
  } finally {
    busy.value = false
  }
}

async function onDelete(vp: LocalVoiceprint) {
  const ok = await confirm({
    message: `删除「${vp.displayName}」的声纹？之后录音不会再用它认人。`,
    danger: true,
    confirmText: '删除',
  })
  if (!ok) return
  busy.value = true
  try {
    await deleteVoiceprint(vp.id)
    if (editingId.value === vp.id) editingId.value = ''
    await load()
  } finally {
    busy.value = false
  }
}

async function onClearAll() {
  const ok = await confirm({
    message: `清空全部 ${items.value.length} 条声纹？之后所有录音都要重新认人。此操作不可撤销。`,
    danger: true,
    confirmText: '全部删除',
  })
  if (!ok) return
  busy.value = true
  try {
    await clearVoiceprints()
    editingId.value = ''
    await load()
  } finally {
    busy.value = false
  }
}

function onClose() {
  emit('close')
}
</script>

<style scoped>
.vp-sheet {
  padding: var(--space-4);
}

.hint {
  margin: 0 0 var(--space-4);
  font-size: var(--text-smd);
  color: var(--text-muted);
  line-height: 1.5;
}

.state {
  padding: var(--space-4);
  text-align: center;
  color: var(--text-muted);
  font-size: var(--text-base);
}

.vp-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin-bottom: var(--space-4);
}

.vp-row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: 12px;
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-base);
}

.name {
  flex: 1;
  font-size: var(--text-md);
  font-weight: 500;
  color: var(--text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.since {
  font-size: var(--text-2xs);
  color: var(--text-muted);
  white-space: nowrap;
}

.edit-input {
  flex: 1;
  padding: 8px 10px;
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-sm);
  background: var(--bg-base);
  font-size: var(--text-base);
  color: var(--text-primary);
}

.mini {
  flex-shrink: 0;
  padding: 6px 10px;
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-sm);
  background: var(--bg-subtle);
  color: var(--text-primary);
  font-size: var(--text-sm);
  cursor: pointer;
}

.mini.primary {
  background: var(--brand-primary);
  border-color: var(--brand-primary);
  color: var(--text-inverse);
}

.mini.danger {
  color: var(--danger, #d93025);
}

.clear-all {
  width: 100%;
  padding: 12px;
  border: 1px solid var(--danger, #d93025);
  border-radius: var(--radius-md);
  background: transparent;
  color: var(--danger, #d93025);
  font-size: var(--text-md);
  cursor: pointer;
}

.clear-all:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>