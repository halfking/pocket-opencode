/**
 * shell/hyperPages.ts — 按页累积 + 按稳定 rowId 去重。
 *
 * 与 `continuousList.ts` 的分工：这里只管「页 → 行」的纯数据变换，
 * 不发请求、不管状态机。这样去重和「同页重试原位替换」这两条最容易错的
 * 规则可以脱离运行时单测。
 *
 * 两条契约（UI规范 07 §3）：
 *   - 同一页重复请求成功后**原位替换**那一页，不能追加成两份；
 *   - 跨页按稳定 rowId 去重。数据频繁变化时页号可能跳过/重复，
 *     去重只能消重、**不能补漏**——不假装当前分页接口有快照一致性。
 *
 * ⚠️ 容量说明：本模块**没有**容量淘汰。nbjl3 那边注释里写着 bounded，
 * 但那是注释不是实现；本仓不复制那个误会。内存预算由
 * continuousList 的 virtualizationThreshold / windowCacheThreshold 负责，
 * 超限时调用方应显式裁剪旧页并记录游标与 anchor 以便返回补取。
 */

import type { ListRow } from './continuousList.ts'

export interface HyperPages<Row extends ListRow> {
  /** 原位写入第 page 页（1 起）。 */
  replace(page: number, rows: Row[]): void
  /** 累加一页（等价 replace，对已存在页做替换）。 */
  append(page: number, rows: Row[]): void
  /** 按稳定 rowId 去重后的全部行，保持插入顺序。 */
  all(): readonly Row[]
  count(): number
  /** 已写入的页号，升序。 */
  pageNumbers(): number[]
  /** 某页的行。 */
  page(page: number): readonly Row[]
  /** 清空（筛选变化 / 换账号）。 */
  reset(): void
  /** 裁掉最旧的页，返回被裁掉的页号。用于窗口缓存。 */
  evictOldestPages(keepFromPage: number): number[]
}

export function createHyperPages<Row extends ListRow>(): HyperPages<Row> {
  // page(1起) → rows。用 Map 保持插入序，便于 pageNumbers() 升序输出。
  const byPage = new Map<number, Row[]>()

  function all(): readonly Row[] {
    const seen = new Set<string>()
    const out: Row[] = []
    const pages = [...byPage.keys()].sort((a, b) => a - b)
    for (const p of pages) {
      for (const row of byPage.get(p) ?? []) {
        if (seen.has(row.id)) continue
        seen.add(row.id)
        out.push(row)
      }
    }
    return out
  }

  return {
    replace(page, rows) {
      if (page < 1) return
      // 原位替换：同页重试成功不追加成两份。
      byPage.set(page, [...rows])
    },
    append(page, rows) {
      if (page < 1) return
      byPage.set(page, [...(byPage.get(page) ?? []), ...rows])
    },
    all,
    count() {
      return all().length
    },
    pageNumbers() {
      return [...byPage.keys()].sort((a, b) => a - b)
    },
    page(page) {
      return byPage.get(page) ?? []
    },
    reset() {
      byPage.clear()
    },
    evictOldestPages(keepFromPage) {
      const dropped: number[] = []
      for (const p of [...byPage.keys()].sort((a, b) => a - b)) {
        if (p < keepFromPage) {
          dropped.push(p)
          byPage.delete(p)
        }
      }
      return dropped
    },
  }
}
