import { http } from './http'

export interface EmailCleanupFilter {
  accountId?: string
  subject?: string
  from?: string
  since?: number
  until?: number
}

export interface EmailCleanupItem {
  id: string
  accountId: string
  uid?: number
  from: string
  subject: string
  date: number
}

export interface EmailCleanupReport {
  matched: number
  moved: number
  deleted: number
  deletedIds?: string[]
  failed?: string[]
  emails?: EmailCleanupItem[]
}

export function previewEmailCleanup(filter: EmailCleanupFilter): Promise<EmailCleanupReport> {
  return http('/api/emails/cleanup', {
    method: 'POST',
    body: JSON.stringify({ ...filter, dryRun: true }),
  })
}

export function runEmailCleanup(filter: EmailCleanupFilter): Promise<EmailCleanupReport> {
  return http('/api/emails/cleanup', {
    method: 'POST',
    body: JSON.stringify({ ...filter, dryRun: false }),
  })
}
