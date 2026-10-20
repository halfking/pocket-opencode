/**
 * embed-reuse.ts — 增量重嵌的**纯决策层**（§899）。
 *
 * ⚠⚠ 为什么把这两条决策抽成纯函数：
 *   第一版的「换模型 ⇒ 全部重嵌」这条判据是**源码门**（在实现里找两串字），
 *   结果变异「把条件改成 `if (false && modelChanged)`」**全绿** ——
 *   源码门对**同形**改动无效（§95.3 的老教训）。
 *   ⇒ 改成把决策放进纯函数，判据直接验它的**行为**。
 *   ⇒ 本仓同款手法：§820「决策放纯函数」。
 *
 * ⚠ 本模块**不得** import 任何网络/数据库/浏览器 API —— 它只回答「该怎么办」。
 */

/** 一条笔记已存的段信息（与 `vector-index.ts::existingChunks` 的返回形状对应）。 */
export interface PreviousChunks {
  /** 每段文本的哈希，顺序与 `vectors` 一一对应 */
  hashes: string[]
  /** 每段的向量 */
  vectors: Float32Array[]
  /** 生成这些向量的模型标识 */
  model: string
}

/**
 * 换模型时是否必须**全部重新嵌入**。
 *
 * ⚠ 为什么换模型就不能复用：不同 embedding 模型的向量**不在同一个语义空间**里，
 *   混在一起算余弦相似度没有意义（分数会乱）。
 *
 * @param apiModel   本次 API 调用返回的模型（`null` = 还没调过 API，全部段都复用了）
 * @param prevModel  库里那批向量是用哪个模型生成的
 * @param reusedCount 本次实际复用了多少段
 */
export function needsFullRebuild(
  apiModel: string | null,
  prevModel: string | null,
  reusedCount: number,
): boolean {
  // 没调 API ⇒ 全部段都复用了 ⇒ 不存在新旧混用 ⇒ 不需要重嵌。
  if (apiModel === null) return false
  // 没有旧模型 ⇒ 没有可混用的东西。
  if (prevModel === null) return false
  // 一段都没复用 ⇒ 本来就全部调了 API ⇒ 不需要「再来一遍」。
  if (reusedCount === 0) return false
  return apiModel !== prevModel
}
