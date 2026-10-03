/**
 * learning-streak-view.ts — Learning Core streak 响应的形状校验。
 *
 * ## 为什么需要它
 *
 * `fetchStreak()` 走 `http<LearningStreakView>()`，那个泛型**只是类型断言，
 * 没有任何运行时校验**。后端一旦返回 200 但体是残缺的（网关降级兜底 `{}`、
 * Learning Core 刚起还没建表、信封改版），TS 不会拦，运行时也不会——
 * 页面会拿着一个"类型上完全合法"的对象去解引用。
 *
 * 代价曾经是整页白屏：StudyHubView 的模板写 `streak.streak.current`，是**两级**
 * 解引用，而模板的 `v-if="streak"` 只挡住了外层为 null，挡不住内层缺失。
 * streak 本来是这一页最次要的一块（一个连续天数角标），却能把闪卡列表一起带走。
 *
 * 抽成纯函数而不是就地 if，是为了让它能单测——形状校验这种逻辑最怕的
 * 恰恰是「改的人不知道自己在防什么」，而 `{}` / `null` / 部分体这些边界
 * 只有断言出来才改得动。
 */
import type { LearningStreakView } from '../../types/learning'

/**
 * 校验并归一化 streak 响应。
 *
 * @returns 形状可信时返回该对象；否则返回 null，调用方据此整块隐藏。
 *
 * 判定条件：非 null 的对象，且 `streak` 是对象且 `current` 是有限数字。
 * 只判 `current` 是不是数字就够——模板真正解引用的字段就是它，
 * `next` / `milestone` 缺失在模板里本来就有 v-if 兜底。
 */
export function normalizeStreakView(raw: unknown): LearningStreakView | null {
  if (!raw || typeof raw !== 'object') return null
  const v = raw as Partial<LearningStreakView>
  if (!v.streak || typeof v.streak !== 'object') return null
  if (typeof v.streak.current !== 'number' || !Number.isFinite(v.streak.current)) return null
  return raw as LearningStreakView
}
