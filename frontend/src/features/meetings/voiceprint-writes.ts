/**
 * voiceprint-writes.ts — 声纹库写入的**决策与 SQL**，刻意不 import 任何原生模块。
 *
 * 为什么单独拆出来（不是为了整洁，是为了它能被真的测到）：
 *   voiceprints-store.ts 一 import `local-db.ts`，Node 的 ESM 解析器就死在
 *   `import './sqlite-web-init'`（无扩展名）上 —— ERR_MODULE_NOT_FOUND。
 *   （同款问题当时让 flashcardIo.test.ts 挂在 test-coverage-waivers 里；
 *     那三处 import 已修、豁免已清空。）
 *   于是「改名会不会把样本数 +1」这个**行为**问题，在这套测试栈里根本测不了。
 * 把「写成哪条 SQL / 走 insert 还是 rename」这个决策搬到这里，
 * 它就变成一个纯函数，可以直接在 Node 里跑。
 *
 * ⚠ 这里的每条 SQL 都是**产品的决定**，不是占位：
 *   把 RENAME 里的 display_name 换成 sample_count，就复现了 §47.2 那个 bug。
 */

// ---------------------------------------------------------------------------
// 归属作用域版本（2026-10-29 引入；2026-11-03 起**只有**这一组）
//
// ⚠ 为什么只有带归属条件的这一组：
//   声纹是**生物特征**（embedding）⇒ 归属错一次就是把 A 的声纹特征交给 B 用。
//   ⇒ App 侧只跑 scoped 语句（voiceprints-store.ts 只 import 这一组），
//     归属未知的行永不被读出/删改。
//
// ⚠ 2026-11-03：删掉了此前并存的**未归属**那组（SQL_LIST / SQL_EXISTS /
//   SQL_RENAME / SQL_DELETE_ONE / SQL_DELETE_ALL / SQL_INSERT / SQL_ENROLL_AGAIN）。
//   那组是 owner scoping 改造前的遗留：App 早已不引用它们，只有行为门拿它们当
//   「纯字符串锚点」⇒ 于是门守着的是**没人跑的 SQL**，而真正在跑的 scoped 组
//   反而没人守（列形状漂移不会被抓到）。
//   ⇒ 做法：锚点直接指到 scoped 组，行为门的判据**只多不少**，
//     死导出也随之清零（不必给死能力基线加任何豁免）。
// ---------------------------------------------------------------------------

/**
 * 归属作用域的列声纹：只列属于该归属的。
 * ⚠ 不取 sample_count —— 理由见 voiceprints-store.ts 的注释
 *   （「改过几次名」不是「几个样本」，取出来会被当样本数展示）。
 */
export const SQL_LIST_SCOPED =
  'SELECT id, display_name, created_at FROM local_voiceprints WHERE owner_user_id = ? AND owner_workspace_id = ? ORDER BY created_at DESC'

/** 归属作用域的查存在（upsert 判定用）。 */
export const SQL_EXISTS_SCOPED =
  'SELECT id FROM local_voiceprints WHERE id = ? AND owner_user_id = ? AND owner_workspace_id = ?'

/**
 * 归属作用域的改名：**只**改 display_name。
 * ⚠ 刻意不含 sample_count / embedding ——
 *   「改一次名字」不是「又多了一个音频样本」，更不该覆盖掉已注册的声纹向量。
 */
export const SQL_RENAME_SCOPED =
  'UPDATE local_voiceprints SET display_name = ? WHERE id = ? AND owner_user_id = ? AND owner_workspace_id = ?'

/** 归属作用域的删除单条。 */
export const SQL_DELETE_ONE_SCOPED =
  'DELETE FROM local_voiceprints WHERE id = ? AND owner_user_id = ? AND owner_workspace_id = ?'

/** 归属作用域的清空全部。 */
export const SQL_DELETE_ALL_SCOPED =
  'DELETE FROM local_voiceprints WHERE owner_user_id = ? AND owner_workspace_id = ?'

/** 归属作用域的首次注册：带归属列，样本数从 1 起。 */
export const SQL_INSERT_SCOPED =
  'INSERT INTO local_voiceprints (id, display_name, embedding, sample_count, created_at, owner_user_id, owner_workspace_id) VALUES (?,?,?,?,?,?,?)'

/** 归属作用域的再 enroll：向量覆盖、样本数 +1。 */
export const SQL_ENROLL_AGAIN_SCOPED =
  'UPDATE local_voiceprints SET display_name = ?, embedding = ?, sample_count = ? WHERE id = ? AND owner_user_id = ? AND owner_workspace_id = ?'

export type VoiceprintWritePlan =
  | { op: 'insert' }
  | { op: 'rename' }

/**
 * 「给一位说话人起名」这条路径该走哪一支。
 *
 * 存在的走 rename（**不**动样本数/向量），不存在的走 insert（带向量落盘）。
 * 这就是 labelSpeaker 需要的形状：首次标注要留下向量，之后改名不该被记成又 enroll 了一次。
 */
export function planVoiceprintWrite(exists: boolean): VoiceprintWritePlan {
  return exists ? { op: 'rename' } : { op: 'insert' }
}

/** 再 enroll 一个音频样本后的样本数。null/缺省视作 1。 */
export function nextSampleCount(before: number | null | undefined): number {
  return (before ?? 1) + 1
}
