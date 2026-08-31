/**
 * rules-format — 邮件过滤策略的前端格式模型。
 *
 * 后端 rules/engine.go 支持双格式：
 *   - 新格式 { rules: [{ type, pattern, actions }] }（actions 支持对象带副参数）
 *   - 旧格式 { whitelist, blacklist, keywords }
 *
 * 本模块负责：账户 rules JSON ⇄ 编辑态 EmailRuleEntry[] 的往返转换，
 * 以及旧格式检测（提示用户保存时将升级为新格式）。
 */
import type { EmailRuleEntry } from '../../api/email'

export interface RulesContainer {
  rules?: EmailRuleEntry[]
  whitelist?: string[]
  blacklist?: string[]
  keywords?: string[]
}

/** 旧格式 → 新格式规则列表；新格式原样深拷贝进入编辑态。 */
export function parseRules(raw: unknown): EmailRuleEntry[] {
  if (!raw || typeof raw !== 'object') return []
  const c = raw as RulesContainer
  if (Array.isArray(c.rules)) {
    return c.rules.map((r) => ({ ...r, actions: [...r.actions] }))
  }
  const out: EmailRuleEntry[] = []
  for (const p of c.whitelist ?? []) {
    out.push({ type: 'sender-whitelist', pattern: p, actions: ['mark-important'] })
  }
  for (const p of c.blacklist ?? []) {
    out.push({ type: 'sender-blacklist', pattern: p, actions: ['archive'] })
  }
  for (const p of c.keywords ?? []) {
    out.push({ type: 'subject-keyword', pattern: p, actions: [{ name: 'label-category', category: 'work' }] })
  }
  return out
}

/** 是否旧格式（无 rules 数组但有黑白名单/关键词）。 */
export function isLegacyRules(raw: unknown): boolean {
  if (!raw || typeof raw !== 'object') return false
  const c = raw as RulesContainer
  if (Array.isArray(c.rules)) return false
  return !!(c.whitelist?.length || c.blacklist?.length || c.keywords?.length)
}

/** 编辑态 → 可直接写回账户的新格式 JSON（剔除空模式/无动作的行）。 */
export function serializeRules(entries: EmailRuleEntry[]): { rules: EmailRuleEntry[] } {
  return {
    rules: entries
      .filter((r) => r.pattern.trim().length > 0 && r.actions.length > 0)
      .map((r) => ({ type: r.type, pattern: r.pattern.trim(), actions: [...r.actions] })),
  }
}
