<!-- 目录选择器：移动邮件到目录（可顺手新建目录）。详情页与列表多选共用。 -->
<template>
  <BottomSheet :model-value="open" title="移动到目录" height="auto" aria-label="选择目标目录" @update:model-value="emit('update:open', $event)">
    <div class="new-row">
      <input v-model="newName" class="new-input" placeholder="新建目录名" @keyup.enter="createAndEmit" />
      <button type="button" class="new-btn" :disabled="!newName.trim() || busy" @click="createAndEmit">新建</button>
    </div>
    <button type="button" class="sheet-item" :class="{ on: current === '' }" @click="pick('')">
      收件箱
    </button>
    <button
      v-for="f in folders"
      :key="f.id"
      type="button"
      class="sheet-item"
      :class="{ on: current === f.name }"
      @click="pick(f.name)"
    >
      <span class="folder-name">{{ f.displayName }}</span>
      <span v-if="f.emailCount > 0" class="folder-count">{{ f.emailCount }}</span>
    </button>
    <p v-if="loadError" class="err">{{ loadError }}</p>
  </BottomSheet>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { BottomSheet } from '../../components'
import {
  createFolder, listLocalFolders, syncFoldersFromServer,
  type LocalFolder,
} from './email-folders-store'

const props = defineProps<{
  open: boolean
  /** 当前邮件所在目录（高亮 + 移回收件箱判断）。 */
  current?: string
  /** 新建目录挂在哪个账户下；缺省取本地第一个账户。 */
  accountId?: string
}>()

const emit = defineEmits<{
  'update:open': [v: boolean]
  pick: [folderName: string]
}>()

const folders = ref<LocalFolder[]>([])
const loadError = ref('')
const newName = ref('')
const busy = ref(false)

async function load() {
  loadError.value = ''
  try {
    folders.value = await listLocalFolders()
    // 后台对齐服务端目录（离线时静默用镜像）。
    syncFoldersFromServer().then((fresh) => { folders.value = fresh }).catch(() => {})
  } catch (e: any) {
    loadError.value = e?.message || '目录加载失败'
  }
}

watch(() => props.open, (v) => { if (v) void load() })

function pick(name: string) {
  emit('update:open', false)
  emit('pick', name)
}

async function createAndEmit() {
  const name = newName.value.trim()
  if (!name || busy.value) return
  busy.value = true
  try {
    const accountId = props.accountId || (await firstAccountId())
    if (!accountId) throw new Error('没有可用邮箱账户')
    await createFolder(accountId, name)
    newName.value = ''
    folders.value = await listLocalFolders()
    emit('update:open', false)
    emit('pick', name)
  } catch (e: any) {
    loadError.value = e?.message || '创建目录失败'
  } finally {
    busy.value = false
  }
}

/** 账户 ID 取第一封可见邮件的 accountId 由调用方保证；这里兜底取本地第一个账户。 */
async function firstAccountId(): Promise<string> {
  const { listAccounts } = await import('./emails-store')
  const accs = await listAccounts()
  return accs[0]?.id || ''
}
</script>

<style scoped>
.new-row { display: flex; gap: 8px; padding: 4px 0 10px; }
.new-input { flex: 1; min-height: 38px; border: 1px solid var(--border); border-radius: var(--radius-sm); padding: 0 10px; background: var(--bg-card); color: var(--text-primary); font-size: var(--text-base); }
.new-btn { min-height: 38px; padding: 0 14px; border: none; border-radius: var(--radius-sm); background: var(--brand-primary); color: var(--text-inverse); }
.new-btn:disabled { opacity: .5; }
.sheet-item { display: flex; justify-content: space-between; align-items: center; width: 100%; text-align: left; padding: 12px 4px; border: none; background: transparent; color: var(--text-primary); font-size: var(--text-md); cursor: pointer; }
.sheet-item.on { color: var(--brand-primary); font-weight: 600; }
.folder-count { font-size: var(--text-sm); color: var(--text-muted); }
.err { color: var(--danger); font-size: var(--text-sm); margin: 6px 0 0; }
</style>
