import type { RecordAdapter, RecordItem } from '../../native/portal-sync/record-contract.ts'
import type { SyncDb } from '../../native/portal-sync/contract.ts'
import { portalRecordStore } from '../../native/portal-sync/record-storage.ts'

/** This projection cannot carry audio, transcript, file paths or linked notes. */
export const meetingSyncAdapter: RecordAdapter = {
  module: 'meetings',
  kind: 'record',
  payload(input) {
    if (Object.keys(input).some((key) => !['held_at', 'attendees', 'minutes'].includes(key)))
      throw new Error('Unknown meeting sync field; local original retained')
    if (
      input.held_at !== undefined &&
      (typeof input.held_at !== 'string' || (input.held_at !== '' && !Number.isFinite(Date.parse(input.held_at))))
    )
      throw new Error('Invalid meeting date')
    if (input.minutes !== undefined && typeof input.minutes !== 'string') throw new Error('Invalid meeting minutes')
    if (
      input.attendees !== undefined &&
      (!Array.isArray(input.attendees) || input.attendees.some((value) => typeof value !== 'string'))
    )
      throw new Error('Invalid meeting attendees')
    return { held_at: input.held_at ?? '', attendees: input.attendees ?? [], minutes: input.minutes ?? '' }
  },
  materialize(item, id, guard, owner) {
    const modified = Date.parse(item.client_updated_at),
      scope = [id, owner.userId, owner.workspaceId]
    if (item.deleted)
      return [
        {
          statement: `UPDATE local_meetings SET deleted_at=?,updated_at=? WHERE id=? AND owner_user_id=? AND owner_workspace_id=? AND ${guard.clause}`,
          values: [modified, modified, ...scope, ...guard.values],
        },
      ]
    const payload = meetingSyncAdapter.payload(item.payload),
      started = payload.held_at ? Date.parse(String(payload.held_at)) : modified
    return [
      {
        statement: `INSERT INTO local_meetings(id,owner_user_id,owner_workspace_id,title,participants,summary,status,started_at,created_at,updated_at)
      SELECT ?,?,?,?,?,?,'completed',?,?,? WHERE ${guard.clause}
      ON CONFLICT(id,owner_user_id,owner_workspace_id) DO UPDATE SET title=excluded.title,participants=excluded.participants,
        summary=excluded.summary,started_at=excluded.started_at,updated_at=excluded.updated_at,deleted_at=NULL`,
        values: [
          ...scope,
          item.title,
          JSON.stringify(payload.attendees),
          payload.minutes,
          started,
          modified,
          modified,
          ...guard.values,
        ],
      },
    ]
  },
}
export function meetingPortalStore(db: SyncDb) {
  return portalRecordStore(db, meetingSyncAdapter)
}
export function meetingPayload(meeting: {
  startedAt: number
  participants: string[]
  summary: string | null
}): RecordItem['payload'] {
  return {
    held_at: new Date(meeting.startedAt).toISOString(),
    attendees: [...meeting.participants],
    minutes: meeting.summary ?? '',
  }
}
