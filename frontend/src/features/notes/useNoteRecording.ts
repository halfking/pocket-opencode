/**
 * useNoteRecording — NoteRecorderRuntime 的页面级薄壳(2026-09-20 P0)。
 *
 * 录音所有权在进程级单例 native/recordingRuntime.ts:NoteListView 被
 * KeepAlive 驱逐或应用级导航离开时录音不断;停止只属于用户显式操作。
 * 页面不在场时 stop() 的产物暂存 runtime.pendingResult,重进后用
 * consumePendingResult() 补建语音草稿。
 *
 * 2026-10-06：这里同时**注册落库出口**（VoiceDraftSink）。
 * 起因是兜底全量转写是一段最长 10 分钟的 await，而在那段窗口里录音只存在于
 * 内存 Blob —— 真机三臂对照实测：同一段挂住的窗口，点「停止转写」音频在盘上，
 * `am force-stop` 则一个笔记目录都不建。草稿必须在长 await 之前落库。
 * 位置选在**模块作用域**：跨页停止时页面虽然被 KeepAlive 缓存，但模块早已加载，
 * 注册一直有效；而录音只能从笔记页的 FAB 起录，所以「没加载过这个模块」时
 * 不可能有在途录音，不需要额外兜底。
 */
import { noteRecorderRuntime as rt, type VoiceDraftSink } from '../../native/recordingRuntime'
import * as notesStore from './notes-store'
import { useAuthStore } from '../../stores/auth'
import type { RecordingPhase } from './note-recording'

/** 与 NoteListView/NoteEditView/NoteDetailView 同口径：本地库按 workspace_id 分区。 */
function currentWorkspaceId(): string {
  return useAuthStore().workspaceId || 'default'
}

/**
 * 落库出口的实现。
 *
 * create 与界面侧的 createVoiceDraft 走**同一个** notesStore.createNote，
 * 所以草稿形态、正文占位（「（语音草稿）」）、createdByVoice 标记、附件落盘
 * 全部一致 —— 不会出现「早落库的草稿」和「界面建的草稿」长得不一样。
 *
 * update 只改 content，**不传 media**：notes-persist 的 updateNote 在没有新媒体
 * 时会把旧的非 body 媒体合并回去（见 note-edit-preserves-media 的门），
 * 所以回填文字不会把音频删掉。
 */
const voiceDraftSink: VoiceDraftSink = {
  async create({ text, audioBlob, durationMs }) {
    const note = await notesStore.createNote({
      content: text || '（语音草稿）',
      contentType: 'voice',
      status: 'draft',
      createdByVoice: true,
      audioBlob,
      audioDurationMs: durationMs,
      workspaceId: currentWorkspaceId(),
    })
    return note.id
  },
  async update(id, text) {
    await notesStore.updateNote(id, { content: text }, currentWorkspaceId())
  },
}

rt.registerVoiceDraftSink(voiceDraftSink)

export function useNoteRecording() {
  return {
    phase: rt.phase,
    elapsedMs: rt.elapsedMs,
    transcript: rt.transcript,
    error: rt.error,
    recording: rt.recording,
    start: () => rt.start(),
    stop: () => rt.stop(),
    toggle: () => rt.toggle(),
    cancelTranscription: () => rt.cancelTranscription(),
    consumePendingResult: () => rt.consumePendingResult(),
  }
}

export type { RecordingPhase }
