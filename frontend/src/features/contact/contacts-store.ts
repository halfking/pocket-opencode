/**
 * contacts-store.ts — S2.3 本地优先联系人实体。
 *
 * Contact 使用 S0-C assetStore(kind='contact') 存储，meta_json 保存结构化字段：
 * { email, phone, organization, title, sourceEmailIds[], lastSeenAt }
 *
 * v1 聚合策略：从本地邮件发件人按 email 去重创建/更新联系人。
 * 会议说话人和联系人合并留给后续 Diarization 接入；本层保持来源引用可扩展。
 */
import { assetStore } from '../../native/asset-store'
import { localDB } from '../../native/local-db'
import { listEmails, type LocalEmail } from '../email/emails-store'

export interface ContactMeta {
  email: string
  phone?: string
  organization?: string
  title?: string
  sourceEmailIds: string[]
  sourceMeetingIds?: string[]
  lastSeenAt: number
}

export interface Contact {
  id: string
  displayName: string
  email: string
  phone?: string
  organization?: string
  title?: string
  sourceEmailIds: string[]
  sourceMeetingIds: string[]
  lastSeenAt: number
  createdAt: number
  updatedAt: number
}

function parseMeta(raw: string): ContactMeta {
  try {
    const meta = JSON.parse(raw) as Partial<ContactMeta>
    return {
      email: meta.email || '',
      phone: meta.phone,
      organization: meta.organization,
      title: meta.title,
      sourceEmailIds: meta.sourceEmailIds || [],
      sourceMeetingIds: meta.sourceMeetingIds || [],
      lastSeenAt: meta.lastSeenAt || 0,
    }
  } catch {
    return { email: '', sourceEmailIds: [], sourceMeetingIds: [], lastSeenAt: 0 }
  }
}

function fromAsset(asset: Awaited<ReturnType<typeof assetStore.get>>): Contact | null {
  if (!asset) return null
  const meta = parseMeta(asset.metaJson)
  return {
    id: asset.id,
    displayName: asset.title || meta.email || '未命名联系人',
    email: meta.email,
    phone: meta.phone,
    organization: meta.organization,
    title: meta.title,
    sourceEmailIds: meta.sourceEmailIds,
    sourceMeetingIds: meta.sourceMeetingIds || [],
    lastSeenAt: meta.lastSeenAt,
    createdAt: asset.createdAt,
    updatedAt: asset.updatedAt,
  }
}

export async function listContacts(workspaceId = 'default', limit = 200): Promise<Contact[]> {
  const assets = await assetStore.search({ workspaceId, kind: 'contact', limit })
  return assets.map(fromAsset).filter((contact): contact is Contact => contact !== null)
}

export async function getContact(id: string): Promise<Contact | null> {
  return fromAsset(await assetStore.get(id))
}

/** 通过邮箱查找联系人，避免重复创建。 */
export async function findContactByEmail(email: string, workspaceId = 'default'): Promise<Contact | null> {
  const normalized = email.trim().toLowerCase()
  if (!normalized) return null
  const rows = await localDB.query<{ id: string; title: string; body_text: string; meta_json: string; created_at: number; updated_at: number }>(
    `SELECT id, title, body_text, meta_json, created_at, updated_at
     FROM local_assets
     WHERE workspace_id = ? AND kind = 'contact' AND deleted_at IS NULL
       AND lower(json_extract(meta_json, '$.email')) = ?
     LIMIT 1`,
    [workspaceId, normalized],
  )
  if (!rows.length) return null
  return fromAsset({
    id: rows[0].id,
    workspaceId,
    kind: 'contact',
    title: rows[0].title,
    bodyText: rows[0].body_text,
    bodyEncrypted: false,
    metaJson: rows[0].meta_json,
    syncMode: 'e2ee_local_first',
    clientRev: 1,
    serverRev: 0,
    dirty: false,
    createdAt: rows[0].created_at,
    updatedAt: rows[0].updated_at,
  })
}

export async function saveContact(input: {
  id?: string
  displayName: string
  email: string
  phone?: string
  organization?: string
  title?: string
  sourceEmailIds?: string[]
  sourceMeetingIds?: string[]
  workspaceId?: string
}): Promise<Contact> {
  const existing = input.email ? await findContactByEmail(input.email, input.workspaceId) : null
  const meta: ContactMeta = {
    email: input.email.trim().toLowerCase(),
    phone: input.phone,
    organization: input.organization,
    title: input.title,
    sourceEmailIds: input.sourceEmailIds || existing?.sourceEmailIds || [],
    sourceMeetingIds: input.sourceMeetingIds || existing?.sourceMeetingIds || [],
    lastSeenAt: existing?.lastSeenAt || Date.now(),
  }
  const asset = await assetStore.upsert({
    id: input.id || existing?.id,
    workspaceId: input.workspaceId,
    kind: 'contact',
    title: input.displayName || input.email,
    bodyText: '',
    metaJson: JSON.stringify(meta),
    encryptBody: false,
    syncMode: 'e2ee_local_first',
  })
  return fromAsset(asset)!
}

/** 从本地邮件发件人聚合联系人，返回本次涉及的联系人。 */
export async function syncContactsFromEmails(workspaceId = 'default'): Promise<Contact[]> {
  const emails = await listEmails({})
  const grouped = new Map<string, LocalEmail[]>()
  for (const email of emails) {
    const key = email.fromAddress.trim().toLowerCase()
    if (!key) continue
    const list = grouped.get(key) || []
    list.push(email)
    grouped.set(key, list)
  }

  const contacts: Contact[] = []
  for (const [email, sourceEmails] of grouped) {
    const latest = sourceEmails.slice().sort((a, b) => b.date - a.date)[0]
    const existing = await findContactByEmail(email, workspaceId)
    const sourceEmailIds = Array.from(new Set([
      ...(existing?.sourceEmailIds || []),
      ...sourceEmails.map((item) => item.id),
    ]))
    contacts.push(await saveContact({
      id: existing?.id,
      displayName: latest.fromName || email,
      email,
      sourceEmailIds,
      workspaceId,
    }))
  }
  return contacts
}

export async function getContactEmails(contact: Contact): Promise<LocalEmail[]> {
  if (!contact.sourceEmailIds.length) return []
  const emails = await listEmails({})
  const ids = new Set(contact.sourceEmailIds)
  return emails.filter((email) => ids.has(email.id)).sort((a, b) => b.date - a.date)
}
