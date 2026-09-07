import { listSnapshots, upsertSnapshots } from '../../native/list-sync/snapshot-store'
import { pickEmailDetailBody } from './email-body-pick'

export { pickEmailDetailBody }

const NS = 'email_bodies'

export async function readEmailBodyLocal(id: string): Promise<string> {
  const rows = await listSnapshots<{ body: string }>(NS)
  return rows.find((row) => row.id === id)?.payload.body || ''
}

export async function writeEmailBodyLocal(id: string, body: string): Promise<void> {
  if (!id || !body) return
  await upsertSnapshots(NS, [{
    id,
    updatedAt: Date.now(),
    dirty: false,
    payload: { body },
  }])
}

export async function clearEmailBodyLocal(id: string): Promise<void> {
  if (!id) return
  await upsertSnapshots(NS, [{
    id,
    updatedAt: Date.now(),
    dirty: false,
    payload: { body: '' },
  }])
}
