import { computed, type Ref } from 'vue'
import { useRouter } from 'vue-router'
import { useConfirm } from '../../composables/useConfirm'
import { useToast } from '../../composables/useToast'
import { deleteMeetingAudio } from '../../native/meeting-audio'
import { canDispatchMeeting, type MeetingStudioAction } from './meeting-page-actions'
import { archiveMeeting, deleteMeeting, unarchiveMeeting, type LocalMeeting } from './meetings-store'
import { handoffMeetingToAcc } from './meeting-todo-persist'

export function useMeetingStudio(
  meeting: Ref<LocalMeeting | null>,
  load: () => Promise<void>,
) {
  const router = useRouter()
  const toast = useToast()
  const { confirm } = useConfirm()
  const canDispatch = computed(() => meeting.value ? canDispatchMeeting(meeting.value) : false)

  async function onStudioAction(id: MeetingStudioAction): Promise<'classify' | void> {
    const m = meeting.value
    if (!m) return
    if (id === 'classify') return 'classify'
    if (id === 'archive') {
      await archiveMeeting(m.id)
      await load()
      toast.success('已归档')
      return
    }
    if (id === 'restore') {
      await unarchiveMeeting(m.id)
      await load()
      toast.success('已恢复')
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
        toast.error(e instanceof Error ? e.message : '下达失败')
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
      await deleteMeeting(m.id)
      try { await deleteMeetingAudio(m.id) } catch { /* ok */ }
      toast.success('已删除')
      await router.replace('/meetings')
    }
  }

  return { canDispatch, onStudioAction }
}
