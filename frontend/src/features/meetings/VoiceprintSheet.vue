<template>
  <BottomSheet :open="open" title="声纹库" @close="onClose">
    <div class="vp-sheet">
      <p class="hint">
        这些声纹保存在本机，之后每场录音都会用它们自动认人。
        <strong>认错了会一直沿用</strong>，所以认错时在这里删掉即可。
      </p>


      <!--
        §857/D18：破坏性动作失败提示放在**互斥链之外**（下面紧跟着
        `v-if="loading" / v-else-if="loadError" / v-else-if="items.length === 0" / v-else`）。
        ⚠ 为什么不塞进 `ul v-else` 里：清空失败时列表确实还在、会落到那个分支，
          但下一次「重试加载」一旦失败就会落到 `loadError` 分支 ⇒ 提示随之消失
          ⇒ 用户刚看到的「没删掉」提示会凭空不见。§856 同一教训。
      -->
      <p v-if="actionError" class="state empty action-error" role="alert">{{ actionError }}</p>
      <div v-if="loading" class="state">载入中…</div>
      <!-- ⚠§822：加载失败**必须显示错误，不能显示「还没有声纹」**。
           那不是「用户没有声纹」，而是「我们没读出来」—— 两者对用户的含义完全相反，
           且后者会让人以为录过的人全都白录了。 -->
      <div v-else-if="loadError" class="state empty">
        声纹读取失败：{{ loadError }}<br />
        <span class="hint">这不代表你没有声纹，请重试或稍后再打开。</span>
      </div>

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

      <!-- ⚠§822：加载失败时 items 保留的是**上一次**读到的内容（不是假的空列表），
           这时不能让它参与「清空全部」的计数与入口 —— 否则用户可能对着过期数字做破坏性操作。 -->
      <button
        v-if="items.length > 0 && !loadError"
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
import { useApiError } from '../../composables/useApiError'
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
/** §822：读取失败的原因。空字符串 = 正常（没有错误）。 */
/** §857/D18：破坏性动作（清空全部）自己的失败，与 loadError 分开。 */
const actionError = ref('')
const loadError = ref('')
const apiError = useApiError()

function formatWhen(ts: number): string {
  return new Date(ts).toLocaleString('zh-CN', {
    month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit',
  })
}

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    items.value = await listVoiceprints()
  } catch (e) {
    // ⚠§822：此前把失败伪装成「空列表」⇒ 用户被告知自己 0 条声纹。
    //   这里**保留上一次成功读到的 items**（不清空、不伪造），只登记错误。
    //   模板会在错误态下优先显示错误，不会露出可能过期的列表。
    // ⚠§822：走仓库既定的错误映射（`apiError`），**不把原始 e.message 拼给用户** ——
    //   `check:raw-error` 正是守这条的，第一版我就是在这里被它抓到的。
    loadError.value = apiError(e, '声纹读取失败')
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
  if (busy.value) return
  // §857/D18：清掉上一条失败提示，否则它会一直挂到下次成功。
  actionError.value = ''
  busy.value = true
  try {
    // ⚠⚠ §857/D18：原来只有 try/finally。`clearVoiceprints` → `localDB.run(DELETE_ALL)`
    //   **不吞错** ⇒ 失败时 rejection 直接逃逸，而列表还停在旧数据、界面零反馈。
    //   ⚠ 这个动作的**代价被确认弹窗放大了**：用户已经过了「此操作不可撤销」的
    //     危险确认，却什么都没发生 ⇒ 他会以为删掉了（列表没变却又不敢重试），
    //     或者反复点同一颗按钮。
    //   失败时**保留列表原样**（下面的 `editingId/load` 排在 await 之后，
    //   天然满足），只把错误说出来 —— 与同文件 `load()` 的 §822 修法同构。
    await clearVoiceprints()
    editingId.value = ''
    await load()
  } catch (e) {
    // ⚠ 走仓库既定的 `apiError` 映射，不把原始 e.message（可能是 SQLITE_BUSY）拼给用户
    //   —— `check:raw-error` 正是守这条的。
    actionError.value = apiError(e, '声纹删除失败，请稍后重试')
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