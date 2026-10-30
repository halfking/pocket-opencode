import type { RecordAdapter } from '../../native/portal-sync/record-contract.ts'

export type PortalMailKind = 'account' | 'summary'

/** Portable metadata only. Credentials, body, attachments and translations have no field here. */
export function portalMailPayload(input: Record<string, unknown>, kind?: string): Record<string, string> {
  if (kind !== 'account' && kind !== 'summary') throw new Error('Unsupported email metadata kind')
  const keys = kind === 'account' ? ['address', 'display_name', 'last_sync_at'] : ['from', 'subject', 'snippet', 'received_at']
  if (Object.keys(input).some(key => !keys.includes(key))) throw new Error('Unknown email metadata field; original retained')
  const out: Record<string, string> = {}
  for (const key of keys) {
    const value = input[key]
    if (value !== undefined && typeof value !== 'string') throw new Error(`Email metadata ${key} must be a string`)
    out[key] = typeof value === 'string' ? value : ''
  }
  if (kind === 'account' && !out.address.trim()) throw new Error('Email metadata address is required')
  if (kind === 'summary' && new TextEncoder().encode(out.snippet).length > 4096) throw new Error('Email snippet exceeds 4096 bytes; original retained')
  return out
}

export const portalMailAdapter: RecordAdapter = {
  module: 'email', kind: 'summary', kinds: ['account', 'summary'], payload: portalMailPayload,
  externalId: (id, kind) => id.startsWith(`${kind}:`) ? id : `${kind}:${id}`,
  // The encrypted projection is the read model. No guessed local account FK or
  // credential is created, and a remote tombstone cannot delete native mail.
  materialize: () => [],
}
