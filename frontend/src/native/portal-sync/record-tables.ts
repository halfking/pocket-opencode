export const recordTables = [
  // A failed assertion aborts the whole source/projection/outbox transaction.
  // Successful assertions insert nothing, so this table remains empty.
  `CREATE TABLE IF NOT EXISTS local_portal_record_write_guards (
    allowed INTEGER NOT NULL CONSTRAINT portal_record_write_conflict CHECK(allowed=1))`,
  `CREATE TABLE IF NOT EXISTS local_portal_records (
    owner_user_id TEXT NOT NULL,owner_workspace_id TEXT NOT NULL,module TEXT NOT NULL,kind TEXT NOT NULL,ext_id TEXT NOT NULL,source_id TEXT NOT NULL,
    item_json TEXT NOT NULL,version_key TEXT NOT NULL,op_id TEXT NOT NULL,reset_epoch INTEGER,
    PRIMARY KEY(owner_user_id,owner_workspace_id,module,kind,ext_id),UNIQUE(owner_user_id,owner_workspace_id,module,source_id))`,
  `CREATE TABLE IF NOT EXISTS local_portal_record_outbox (
    owner_user_id TEXT NOT NULL,owner_workspace_id TEXT NOT NULL,module TEXT NOT NULL,kind TEXT NOT NULL,op_id TEXT NOT NULL,ext_id TEXT NOT NULL,
    item_json TEXT NOT NULL,reset_epoch INTEGER,last_error TEXT NOT NULL DEFAULT '',
    PRIMARY KEY(owner_user_id,owner_workspace_id,module,op_id))`,
]
