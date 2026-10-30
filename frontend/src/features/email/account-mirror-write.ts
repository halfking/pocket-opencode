/**
 * 远程账户下行到本地镜像的 SQL。
 *
 * 服务端不会把 IMAP 凭证回传给客户端。本地表 credential_encrypted 又是
 * NOT NULL；jeep-sqlite 还会把 '' 绑成 NULL。因此：
 *   - 新行 INSERT 写非空占位符，不能绑 '' / null
 *   - 已有行只 UPDATE 元数据，绝不走 UPSERT（UPSERT 会先按 INSERT 做 NOT NULL）
 */
import { ownerColumnValues, ownerScopeSql, type MeetingOwner } from '../../native/meeting-ownership.ts'
export const REMOTE_MIRROR_CREDENTIAL = '[remote-mirror]'

export interface MirrorAccountInput {
  id: string
  displayName: string
  emailAddress: string
  imapHost: string
  imapPort: number
  authType: string
  syncIntervalMin: number
  enabled: boolean
  updatedAt: number
  /** Scoped metadata responses must never replace device connection settings. */
  metadataOnly?: boolean
}

export function buildMirrorAccountWrite(
  local: { createdAt: number } | null,
  acc: MirrorAccountInput,
  createdAt: number,
  owner: MeetingOwner | null = null,
): { sql: string; values: unknown[] } {
  const scope = ownerScopeSql(owner)
  if (local) {
    if (acc.metadataOnly) return {
      sql: `UPDATE local_email_accounts SET display_name=?, email_address=?, updated_at=? WHERE id=? AND ${scope.clause}`,
      values: [acc.displayName, acc.emailAddress, acc.updatedAt, acc.id, ...scope.params],
    }
    return {
      sql: `UPDATE local_email_accounts SET
        display_name=?, email_address=?, imap_host=?, imap_port=?,
        auth_type=?, sync_interval_min=?, enabled=?, updated_at=?
      WHERE id=? AND ${scope.clause}`,
      values: [
        acc.displayName, acc.emailAddress, acc.imapHost, acc.imapPort,
        acc.authType ?? 'password', acc.syncIntervalMin, acc.enabled ? 1 : 0,
        acc.updatedAt, acc.id, ...scope.params,
      ],
    }
  }
  return {
    sql: `INSERT INTO local_email_accounts
       (id, display_name, email_address, imap_host, imap_port, auth_type, credential_encrypted,
        sync_interval_min, enabled, created_at, updated_at, owner_user_id, owner_workspace_id)
     VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
    values: [
      acc.id, acc.displayName, acc.emailAddress, acc.metadataOnly ? '[remote-metadata]' : acc.imapHost, acc.metadataOnly ? 993 : acc.imapPort,
      acc.metadataOnly ? 'password' : acc.authType ?? 'password', REMOTE_MIRROR_CREDENTIAL,
      acc.metadataOnly ? 15 : acc.syncIntervalMin, !acc.metadataOnly && acc.enabled ? 1 : 0, createdAt, acc.updatedAt, ...ownerColumnValues(owner),
    ],
  }
}
