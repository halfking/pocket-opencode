/**
 * localagent/system-prompt.ts — system prompt 组装。
 *
 * 分层(对齐 pi buildSystemPrompt 的结构化拼接):
 *   1. 基础人格(或专家 systemPrompt 覆盖);
 *   2. 环境事实(当前时间/平台,模型最常犯错的锚点);
 *   3. 技能清单(渐进披露:只有 name/description,正文经 load_skill 工具取,
 *      pi formatSkillsForPrompt 同语义);
 *   4. 工具协议 + 工具清单(tool-protocol.buildToolProtocolPrompt)。
 */

import type { AgentTool, Expert, Skill } from './types.ts'
import { buildToolProtocolPrompt } from './tool-protocol.ts'

export function buildSystemPrompt(opts: {
  expert?: Expert
  skills: Skill[]
  tools: AgentTool[]
  now?: Date
  platform?: string
}): string {
  const now = opts.now ?? new Date()
  const parts: string[] = []

  parts.push(
    opts.expert?.systemPrompt ??
      [
        '你是 openpocket 手机 App 内置的本地智能体,运行在用户的设备上,可以调用工具完成本地操作。',
        '回答用简体中文,简洁、直接、可执行。',
        '涉及计算、获取实时信息、读写文件等场景时优先使用工具,不要凭空编造结果。',
      ].join('\n'),
  )

  const timeText = formatNow(now)
  parts.push(`## 环境\n- 当前时间:${timeText}\n- 运行平台:${opts.platform ?? 'mobile-app(WebView)'}`)

  if (opts.skills.length > 0) {
    const lines = opts.skills.map((s) => `- ${s.name}:${s.description}`)
    parts.push(
      [
        '## 可用技能',
        '以下是已安装技能的清单。当任务与某技能相关时,先调用 load_skill 工具(name 填技能名)获取完整操作指引,再按指引执行。',
        ...lines,
      ].join('\n'),
    )
  }

  parts.push(buildToolProtocolPrompt(opts.tools))
  return parts.join('\n\n')
}

function formatNow(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  const week = ['日', '一', '二', '三', '四', '五', '六'][d.getDay()]
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}(周${week})`
}
