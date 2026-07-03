/**
 * note-adapter.ts — 把 notesStore.LocalNote 映射为 NoteCard 组件期望的 Note
 *
 * 字段名差异：store 用 camelCase，组件用 snake_case。
 * 字段裁剪：组件不关心 workspaceId / category / contentType / createdByVoice。
 * 默认值：null 字段转 undefined（让组件条件渲染"无标签"时不显示）。
 */
import type { LocalNote } from '../../features/notes/notes-store'
import type { Note } from './NoteCard.vue'

/** 取首段文字做 fallback 标题（最多 24 字符，与 NoteListView 现有行为一致） */
function fallbackTitle(n: LocalNote): string {
  if (n.title) return n.title
  return n.content.slice(0, 24)
}

export function toCardNote(n: LocalNote): Note {
  return {
    id: n.id,
    title: fallbackTitle(n),
    content: n.content,
    domain: n.domain ?? undefined,
    tags: n.tags ?? undefined,
    audio_path: n.audioPath ?? undefined,
    created_at: n.createdAt,
    updated_at: n.updatedAt,
  }
}

export function toCardNotes(arr: LocalNote[]): Note[] {
  return arr.map(toCardNote)
}