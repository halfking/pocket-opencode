/**
 * vector.ts — 向量索引单例 + 平台接线（§881 之后这里只剩「接线」）。
 *
 * 原本索引本体住在这个文件里、顶层 import `localDB` 单例
 * ⇒ **「更新一条已有笔记后检索是否还认新内容」这条性质在 node 里测不到**
 *（`local-db` → `sqlite-web-init` 那一族是无扩展名 import，简报第 94 条）。
 * ⇒ 可测核心搬到 `vector-index.ts`（只依赖注入的 db），
 *   本文件只负责把生产单例接上去 —— 与 §879 的 `notes-fts-backfill` 同一手法。
 */
import { localDB, localDbAsSql } from './local-db'
import { VectorIndex } from './vector-index'

export { VectorIndex, normalize, blobToFloat32, DIM } from './vector-index'
export type { VectorMatch, VectorDb } from './vector-index'

/** 向量索引单例，全 App 共享。 */
export const vectorIndex = new VectorIndex(localDbAsSql(localDB))
