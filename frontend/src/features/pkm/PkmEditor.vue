<!--
  PkmEditor.vue — S1.1 TipTap WYSIWYG 编辑器组件

  自包含：传入 noteId 自动加载，编辑时 debounce 保存。
  - title input + TipTap body
  - [[标题]] 自动转双向链接（wikilink.ts 输入规则）
  - 点击 wikilink → emit('navigate', target)，父视图负责跳转/创建
  - emit('saved', note) 保存后通知父组件（用于刷新 backlinks）

  用法：
    <PkmEditor :note-id="id" @navigate="onNav" @saved="onSaved" />
-->
<template>
  <div class="pkm-editor">
    <input
      v-model="title"
      class="pkm-title"
      placeholder="无标题"
      @input="scheduleSave"
    />
    <EditorContent v-if="editor" :editor="editor" class="pkm-body" />
    <div v-if="saving" class="pkm-saving">保存中…</div>
    <div v-else-if="lastSaved" class="pkm-saving saved">已保存</div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, onBeforeUnmount, shallowRef } from 'vue'
import { Editor, EditorContent } from '@tiptap/vue-3'
import StarterKit from '@tiptap/starter-kit'
import { Wikilink } from './wikilink'
import { getNote, saveNote, type PkmNote } from './pkm-store'

const props = defineProps<{
  noteId: string
  /** 可选 dailyDate，传入则保存时带上（Daily Note 用）。 */
  dailyDate?: string
}>()

const emit = defineEmits<{
  (e: 'navigate', target: string): void
  (e: 'saved', note: PkmNote): void
}>()

const title = ref('')
const saving = ref(false)
const lastSaved = ref(false)
const editor = shallowRef<Editor | null>(null)
const currentNote = shallowRef<PkmNote | null>(null)
let saveTimer: ReturnType<typeof setTimeout> | null = null

/** 加载笔记并初始化编辑器。 */
async function load(id: string) {
  const note = await getNote(id)
  currentNote.value = note
  title.value = note?.title ?? ''
  // 销毁旧编辑器（noteId 变化时）
  editor.value?.destroy()
  editor.value = new Editor({
    extensions: [StarterKit, Wikilink],
    content: note?.html ?? '',
    onUpdate: scheduleSave,
    editorProps: {
      // 点击 wikilink 节点 → emit navigate，交给父视图跳转
      handleClick: (_view, pos, event) => {
        const target = event.target as HTMLElement
        const wl = target.closest('a[data-wikilink]')
        if (wl) {
          emit('navigate', wl.getAttribute('data-target') || '')
          return true
        }
        return false
      },
    },
  })
}

/** 防抖保存：停止输入 800ms 后落库。 */
function scheduleSave() {
  if (saveTimer) clearTimeout(saveTimer)
  saving.value = true
  lastSaved.value = false
  saveTimer = setTimeout(doSave, 800)
}

async function doSave() {
  if (!editor.value) return
  try {
    const html = editor.value.getHTML()
    const saved = await saveNote({
      id: props.noteId,
      title: title.value || '无标题',
      html,
      dailyDate: props.dailyDate,
    })
    currentNote.value = saved
    lastSaved.value = true
    emit('saved', saved)
  } catch (e) {
    console.error('[pkm-editor] save failed:', e)
  } finally {
    saving.value = false
  }
}

watch(
  () => props.noteId,
  (id) => {
    if (id) load(id)
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  if (saveTimer) clearTimeout(saveTimer)
  // 组件销毁前把未保存内容落盘
  if (editor.value && currentNote.value) {
    doSave()
  }
  editor.value?.destroy()
})
</script>

<style scoped>
.pkm-editor {
  display: flex;
  flex-direction: column;
  height: 100%;
}
.pkm-title {
  border: none;
  outline: none;
  font-size: 22px;
  font-weight: 700;
  padding: 16px 16px 8px;
  background: transparent;
  color: var(--text-primary, #111);
}
.pkm-body {
  flex: 1;
  overflow-y: auto;
  padding: 0 16px 80px;
}
.pkm-body :deep(.ProseMirror) {
  min-height: 200px;
  outline: none;
  line-height: 1.7;
  font-size: 15px;
}
.pkm-body :deep(.wikilink) {
  color: var(--accent, #2563eb);
  background: var(--wikilink-bg, #eef2ff);
  border-radius: 4px;
  padding: 0 3px;
  cursor: pointer;
  text-decoration: none;
}
.pkm-body :deep(.wikilink):hover {
  text-decoration: underline;
}
.pkm-saving {
  position: fixed;
  bottom: 80px;
  right: 16px;
  font-size: 11px;
  color: var(--text-secondary, #888);
  background: var(--bg-elevated, #fff);
  padding: 3px 8px;
  border-radius: 10px;
  box-shadow: 0 1px 4px rgba(0, 0, 0, 0.1);
}
.pkm-saving.saved {
  color: var(--success, #16a34a);
}
</style>
