import { encryptString, decryptString } from '../crypto'
import { useCryptoConfig } from '../../stores/crypto-config'
import { PortalRecordStore } from './record-store.ts'
import type { RecordAdapter, RecordItem } from './record-contract.ts'
import type { SyncDb } from './contract.ts'

export function portalRecordStore(db: SyncDb, adapter: RecordAdapter): PortalRecordStore {
  return new PortalRecordStore(
    db,
    adapter,
    async (item) => {
      const json = JSON.stringify(item)
      return useCryptoConfig().shouldEncryptField() ? `e1:${await encryptString(json)}` : json
    },
    async (value) => JSON.parse(value.startsWith('e1:') ? await decryptString(value.slice(3)) : value) as RecordItem,
  )
}
