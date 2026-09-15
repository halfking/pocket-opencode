/**
 * localagent/skills.ts — 技能注册表(SKILL.md 标准,pi skills.ts 同款字段)。
 *
 * MVP:技能以内置 SKILL.md 字符串承载(TS 常量),parseSkillMd 负责解析
 * frontmatter,与「从文件系统/市场下载的技能」走同一入口——二期 marketplace
 * 技能包落地后只需把文件内容喂给 parseSkillMd。
 */

import type { Skill } from './types.ts'

/** 解析 SKILL.md:YAML frontmatter(name/description)+ markdown 正文。 */
export function parseSkillMd(raw: string): Skill | null {
  const m = /^---\r?\n([\s\S]*?)\r?\n---\r?\n?([\s\S]*)$/.exec(raw)
  if (!m) return null
  const meta = parseFrontmatter(m[1])
  const name = (meta['name'] ?? '').trim()
  const description = (meta['description'] ?? '').trim()
  if (!name || !description) return null
  if (!/^[a-z0-9][a-z0-9-]{0,63}$/.test(name)) return null
  return { name, description: description.slice(0, 1024), body: m[2].trim() }
}

/** 极简 frontmatter 解析:key: value 行(值可带引号);不支持嵌套——技能标准用不到。 */
function parseFrontmatter(block: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const line of block.split(/\r?\n/)) {
    const idx = line.indexOf(':')
    if (idx <= 0) continue
    const key = line.slice(0, idx).trim()
    let val = line.slice(idx + 1).trim()
    if ((val.startsWith('"') && val.endsWith('"')) || (val.startsWith("'") && val.endsWith("'"))) {
      val = val.slice(1, -1)
    }
    if (key) out[key] = val
  }
  return out
}

// ---------------------------------------------------------------------------
// 内置技能(SKILL.md 原文)
// ---------------------------------------------------------------------------

const SKILL_DEEP_READ = `---
name: deep-read
description: 深度阅读长文本/文章,输出分层摘要(一句话 → 要点 → 细节)与行动建议
---

# 深度阅读

对用户提供的长文本执行三段式消化:

1. **一句话总结**:30 字以内概括核心。
2. **要点清单**:3-7 条,每条一行,保留关键数字与主体。
3. **细节与例外**:值得注意的反例、条件、数据来源。
4. **行动建议**:如果适合,给出 1-3 条可执行下一步。

规则:不遗漏数字与日期;引用原文用「」;中文输出。
`

const SKILL_TRIP_PLAN = `---
name: trip-plan
description: 制定行程计划:按天拆分、时间线排列、标注交通/餐饮/备选,并生成任务计划卡
---

# 行程规划

为用户制定行程时:

1. 先确认:日期天数、同行人、预算档位、节奏偏好(紧凑/松弛)。信息不全时先问最关键的 1-2 个问题。
2. 用 task_plan 工具把每天/每个大项建成计划条目,让用户看到推进。
3. 输出按天分节:时间线(时段 + 活动 + 地点)、交通衔接、餐饮建议、备选方案(雨天/排队)。
4. 标注预估花费区间,汇总预算。

规则:同一时段不安排两个地点;景点间交通必须现实(查不了就标注「需确认」)。
`

const SKILL_MEETING_NOTES = `---
name: meeting-notes
description: 把会议记录/录音转写整理成结构化纪要:决议、待办、风险
---

# 会议纪要

整理输入的会议内容为:

1. **会议概要**:主题、时间、参与人(缺失则标注)。
2. **关键讨论**:按议题分节,每节 2-4 行。
3. **决议**:编号列出已定事项。
4. **待办**:表格化 — 事项 | 负责人 | 截止时间(缺失标 TBD)。
5. **风险与开放问题**。

规则:忠实于原文,不臆造决议;口语转书面;人名保留原样。
`

const SKILL_WEEKLY_REPORT = `---
name: weekly-report
description: 把本周零散记录整理成周报:成果、数据、下周计划
---

# 周报生成

输入零散记录后输出:

1. **本周成果**:3-5 条,动词开头,能量化则量化。
2. **数据看板**:关键数字(完成的任务数、发票处理量等,来自用户输入)。
3. **问题与阻塞**:1-3 条。
4. **下周计划**:3-5 条,按优先级排序。

规则:避免流水账;按「价值」而非「活动」组织语言。
`

const SKILL_INVOICE_EXTRACT = `---
name: invoice-extract
description: 从文本中提取发票要素:开票方、金额、税额、日期、发票号,输出结构化清单
---

# 发票要素提取

从用户粘贴的邮件/文本中提取发票信息:

1. 逐张发票输出:开票方 | 发票号 | 开票日期 | 金额(含税) | 税额 | 税率 | 类别。
2. 缺失字段标「—」,不要猜测。
3. 末尾汇总:总金额、总税额、张数。
4. 金额可疑(大小写不符、税率异常)时单独提示。

规则:金额保留两位小数;币种默认人民币,有外币时注明。
`

const SKILL_UNIT_CONVERT = `---
name: unit-convert
description: 单位与货币换算:长度/重量/温度/面积/速率/数据量,计算类任务先算后答
---

# 单位换算

1. 识别数值与单位,用 calculate 工具完成换算(温度换算先列公式)。
2. 输出:原值 → 换算值(保留合理精度),附换算系数。
3. 货币汇率不在工具能力内:如需汇率,明确告知用户无法获取实时汇率,按用户提供的汇率计算。

常见系数:1 英里=1.609344km;1 磅=0.45359237kg;1 加仑(美)=3.785411784L;1°F=5/9°C 差值。
`

const BUILT_IN_RAW = [
  SKILL_DEEP_READ,
  SKILL_TRIP_PLAN,
  SKILL_MEETING_NOTES,
  SKILL_WEEKLY_REPORT,
  SKILL_INVOICE_EXTRACT,
  SKILL_UNIT_CONVERT,
]

/** 内置技能注册表(解析失败的开发期错误直接抛,便于发现)。 */
export const builtinSkills: Skill[] = BUILT_IN_RAW.map((raw) => {
  const s = parseSkillMd(raw)
  if (!s) throw new Error('localagent: builtin skill failed to parse')
  return s
})

/** 注册表:按名取技能(内置 + 动态注册)。 */
export class SkillRegistry {
  private skills = new Map<string, Skill>()

  constructor(initial: Skill[] = []) {
    for (const s of initial) this.register(s)
  }

  register(skill: Skill): void {
    this.skills.set(skill.name, skill)
  }

  get(name: string): Skill | undefined {
    return this.skills.get(name)
  }

  list(): Skill[] {
    return [...this.skills.values()]
  }
}
