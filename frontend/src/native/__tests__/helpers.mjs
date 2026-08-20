/**
 * Node 测试共用：node:sqlite 实现的 SqlDb + schema 初始化。
 * 与生产 LocalDB（Capacitor SQLite）实现同一接口，跑真实 SQL。
 */
import { DatabaseSync } from 'node:sqlite'
import { SCHEMA_SQL } from '../schema.ts'

export class NodeSqlDb {
  constructor() {
    this.db = new DatabaseSync(':memory:')
    this.db.exec(SCHEMA_SQL)
  }

  async run(sql, params = []) {
    const res = this.db.prepare(sql).run(...params)
    return Number(res.changes)
  }

  async all(sql, params = []) {
    return this.db.prepare(sql).all(...params)
  }
}
