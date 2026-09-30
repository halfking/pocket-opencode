/**
 * i18n 缺 key 处理（BUG-AO）。
 *
 * 背景：`study.due.allClear` 等 13 个 key 长期「代码在用、9 个语言文件全缺」，
 * vue-i18n 缺 key 时直接把 key 字符串回显给用户，界面上出现
 * `study.due.allClear` 这种机器串，而且**没有任何告警**——它是靠人肉看截图
 * 偶然发现的，不是被系统报出来的。
 *
 * 这里只做**检测**，不改变用户看到的文案：
 *  - 缺 key 仍回显 key 本身（改成别的会让「界面上出现可疑字符串」这个信号消失，
 *    反而更难发现；是否给生产环境换中性占位符属于产品决策，不在这里替用户定）；
 *  - 但每次缺 key 会打一条 console.warn，且按 key 去重，避免一个循环里刷几百行。
 *
 * 单独成模块而不是内联在 i18n/index.ts：index.ts 依赖 pinia/vue-i18n 与
 * 无扩展名的相对 import，Node 的 ESM 解析器跑不起来，没法进 `node --test`。
 * 与 api/tasks-url.ts 同一个理由。
 */

/** 已告警过的 key，避免同一个 key 在渲染循环里刷屏。 */
const warned = new Set<string>()

/** 仅供测试：清空去重表。 */
export function resetMissingWarnState(): void {
  warned.clear()
}

/** 仅供测试：当前已告警的 key 集合。 */
export function warnedKeys(): string[] {
  return [...warned]
}

/**
 * vue-i18n `missing` 钩子。
 *
 * @param locale 当前语言
 * @param key 缺失的 key
 * @returns 回显 key 本身（保持既有可见行为，让问题在界面上依然可被看见）
 */
export function onMissingKey(locale: string, key: string): string {
  const dedupKey = `${locale}:${key}`
  if (!warned.has(dedupKey)) {
    warned.add(dedupKey)
    // eslint-disable-next-line no-console
    console.warn(`[i18n] 缺失文案 key：${key}（当前语言 ${locale}）`)
  }
  return key
}
