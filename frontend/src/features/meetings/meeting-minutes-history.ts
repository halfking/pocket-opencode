import { http } from '../../api/http'
import { versionKey } from '../../native/portal-sync/contract.ts'
import type { RecordingOperation } from './recording-operation'

export interface MeetingMinutesSnapshot {
  id: string
  title: string
  minutes: string
  participants: string[]
  created_at: string
  updated_at: string
}

interface MeetingMinutesPage {
  user_id: string
  workspace_id: string
  meetings: MeetingMinutesSnapshot[]
  next_offset: number | null
}

export async function readMeetingMinutesHistory(operation: RecordingOperation, offset: number, signal?: AbortSignal) {
  operation.assertCloud()
  const owner = operation.cloudDb?.owner
  if (!owner) throw new Error('Verified meeting history owner required')
  const page = await http<MeetingMinutesPage>(`/api/meeting-minutes/history?offset=${offset}`, {
    method: 'GET', syncModule: 'meetings', cache: 'no-store', assertCurrent: operation.assertCloud, signal,
  })
  operation.assertCloud()
  if (page?.user_id !== owner.userId || page.workspace_id !== owner.workspaceId || !Array.isArray(page.meetings)
      || (page.next_offset !== null && (!Number.isSafeInteger(page.next_offset) || page.next_offset <= offset))) {
    throw new Error('Invalid meeting history scope or page')
  }
  const seen = new Set<string>()
  const minutes = page.meetings.map((row) => {
    if (!row || typeof row.id !== 'string' || !row.id || seen.has(row.id) || typeof row.title !== 'string'
        || typeof row.minutes !== 'string' || !Array.isArray(row.participants) || row.participants.some(name => typeof name !== 'string')
        || typeof row.created_at !== 'string' || typeof row.updated_at !== 'string') {
      throw new Error('Invalid meeting minutes snapshot')
    }
    versionKey(row.created_at)
    versionKey(row.updated_at)
    seen.add(row.id)
    return { id: row.id, title: row.title, minutes: row.minutes, participants: row.participants,
      created_at: row.created_at, updated_at: row.updated_at }
  })
  return { minutes, nextOffset: page.next_offset }
}
