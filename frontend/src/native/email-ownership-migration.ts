import { SCHEMA_SQL } from './schema'

interface MigrationConnection {
  execute(sql: string, transaction: boolean): Promise<unknown>
  query(sql: string, values?: unknown[]): Promise<{ values?: Record<string, unknown>[] }>
  beginTransaction(): Promise<unknown>
  commitTransaction(): Promise<unknown>
  rollbackTransaction(): Promise<unknown>
}
const tables = ['local_email_accounts', 'local_emails', 'local_email_folders', 'local_email_ops', 'local_email_invoices'] as const

/** Keep unprovable historical ownership blank; rebuild all five tables atomically. */
export async function migrateEmailOwnership(conn: MigrationConnection): Promise<void> {
  const columns = new Map<string, Record<string, unknown>[]>()
  for (const table of tables) columns.set(table, (await conn.query(`PRAGMA table_info(${table})`)).values ?? [])
  if (tables.every(table => columns.get(table)?.filter(c => Number(c.pk) > 0).map(c => c.name).sort().join(',') === 'id,owner_user_id,owner_workspace_id')) return
  await conn.beginTransaction()
  try {
    // Temporary parent names prevent SQLite from retargeting the new FK to the old table.
    for (const table of tables) {
      const definition = SCHEMA_SQL.match(new RegExp(`CREATE TABLE IF NOT EXISTS ${table} \\([^]*?\\n\\);`))?.[0]
      if (!definition) throw new Error(`Missing email schema: ${table}`)
      const sql = definition.replace(new RegExp(`CREATE TABLE IF NOT EXISTS ${table}\\b`), `CREATE TABLE ${table}_owned_v1`).replace(/REFERENCES local_email_accounts\(/g, 'REFERENCES local_email_accounts_owned_v1(')
      await conn.execute(sql, false)
      const names = columns.get(table)!.map(c => String(c.name))
      const values = names.map(name => name.startsWith('owner_') ? `COALESCE(${name}, '')` : name)
      if (names.length) await conn.execute(`INSERT INTO ${table}_owned_v1 (${names.join(',')}) SELECT ${values.join(',')} FROM ${table}`, false)
    }
    // Drop children first so ON DELETE CASCADE cannot discard legacy mail during the rebuild.
    for (const table of [...tables].reverse()) await conn.execute(`DROP TABLE ${table}`, false)
    for (const table of tables) await conn.execute(`ALTER TABLE ${table}_owned_v1 RENAME TO ${table}`, false)
    const indexes = SCHEMA_SQL.match(/CREATE INDEX IF NOT EXISTS idx_email[^;]+;/g) ?? []
    for (const sql of indexes) await conn.execute(sql, false)
    await conn.commitTransaction()
  } catch (error) {
    await conn.rollbackTransaction()
    throw error
  }
}
