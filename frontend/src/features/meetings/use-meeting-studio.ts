import { computed, type Ref } from 'vue'
import { useRouter } from 'vue-router'
import { useConfirm } from '../../composables/useConfirm'
import { useToast } from '../../composables/useToast'
import { useApiError } from '../../composables/useApiError'
import { deleteMeetingAudio } from '../../native/meeting-audio'
import {
  describeRecordActionFailure,
  RECORD_ACTION_FAIL_TAG,
  RECORD_MEDIA_CLEANUP_FAIL_TAG,
} from '../../utils/action-failure-notice'
import { canDispatchMeeting, type MeetingStudioAction } from './meeting-page-actions'
import { archiveMeeting, deleteMeeting, unarchiveMeeting, type LocalMeeting } from './meetings-store'
import { handoffMeetingToAcc } from './meeting-todo-persist'
import { captureRecordingDb } from './recording-operation.ts'

export function useMeetingStudio(
  meeting: Ref<LocalMeeting | null>,
  load: () => Promise<void>,
) {
  const router = useRouter()
  const toast = useToast()
  const apiError = useApiError()
  const { confirm } = useConfirm()
  const canDispatch = computed(() => meeting.value ? canDispatchMeeting(meeting.value) : false)

  async function onStudioAction(id: MeetingStudioAction): Promise<'classify' | void> {
    const db = captureRecordingDb()
    const m = meeting.value
    if (!m) return
    if (id === 'classify') return 'classify'
    // ⚠⚠ §838：这三处此前是裸 await + **无条件**的成功 toast。写失败时 toast
    //   不出现（它排在 await 之后）⇒ 用户只看到菜单点了没反应，零说明。
    if (id === 'archive') {
      try {
        await archiveMeeting(m.id, db)
        db.assertCurrent()
        await load()
        toast.success('已归档')
      } catch (e) {
        console.error(RECORD_ACTION_FAIL_TAG, 'archive', e)
        toast.error(describeRecordActionFailure('archive', { error: e }))
      }
      return
    }
    if (id === 'restore') {
      try {
        await unarchiveMeeting(m.id, db)
        db.assertCurrent()
        await load()
        toast.success('已恢复')
      } catch (e) {
        console.error(RECORD_ACTION_FAIL_TAG, 'restore', e)
        toast.error(describeRecordActionFailure('restore', { error: e }))
      }
      return
    }
    if (id === 'dispatch-acc') {
      if (!canDispatch.value) {
        toast.error('请先总结或生成待办')
        return
      }
      try {
        const task = await handoffMeetingToAcc(m)
        toast.success('已下达任务给 ACC')
        await router.push(`/settings/scheduled-tasks/${task.id}`)
      } catch (e) {
        toast.error(apiError(e, 'errors.operateFailed'))
      }
      return
    }
    if (id === 'delete') {
      const ok = await confirm({
        title: '删除会议',
        message: `确定删除「${m.title || '未命名会议'}」？此操作不可撤销。`,
        confirmText: '删除',
        danger: true,
      })
      if (!ok) return
      db.assertCurrent()
      // ⚠ §838：音频清理失败此前是 `catch { /* ok */ }` ⇒ blob 永久残留、零痕迹。
      //   主体已删除不必重试，但残留占空间，必须说出来。
      try {
        await deleteMeeting(m.id, db)
      } catch (e) {
        console.error(RECORD_ACTION_FAIL_TAG, 'delete', e)
        toast.error(describeRecordActionFailure('delete', { error: e }))
        return
      }
      try {
        await deleteMeetingAudio(m.id, db)
      } catch (e) {
        console.error(RECORD_MEDIA_CLEANUP_FAIL_TAG, e)
        toast.error(describeRecordActionFailure('delete', { error: e, mediaCleaned: false }))
      }
      db.assertCurrent()
      toast.success('已删除')
      await router.replace('/meetings')
    }
  }

  return { canDispatch, onStudioAction }
}
