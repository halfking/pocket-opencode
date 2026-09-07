export interface MeetingSkill {
  id: string
  label: string
  hint: string
  prompt: string
}

export const MEETING_SKILLS: MeetingSkill[] = [
  {
    id: 'meeting-minutes',
    label: '滚动纪要',
    hint: '摘要 / 决议 / 待办 / 未决问题',
    prompt: `你是会议记录助手。只根据给定转写总结，禁止编造未说出口的人名、数字、决议。
用中文输出结构化纪要：会议摘要、关键决策、行动项（负责人/截止时间若能识别）、待确认问题。`,
  },
  {
    id: 'decisions',
    label: '决议清单',
    hint: '只列已拍板事项',
    prompt: `你是会议决议书记。只提取转写中已经明确拍板的决议，不要把讨论中的建议写成决议。
用中文列出：决议、提出人（若有）、影响范围。没有决议就写「暂无明确决议」。`,
  },
  {
    id: 'action-items',
    label: '待办提取',
    hint: '负责人 / 期限 / 原文依据',
    prompt: `你是待办提取助手。只从转写中提取可执行行动项。
每条包含：事项、负责人（未知则空）、期限（未知则空）、依据原话。不要编造。中文输出。`,
  },
  {
    id: 'tldr',
    label: '缺席速览',
    hint: '2-4 句给没来的人',
    prompt: `用 2-4 句中文向未到场的人说明这场会：讨论了什么、拍了什么板、谁接下来要做什么。不要编造。`,
  },
]

export const DEFAULT_MEETING_SKILL = MEETING_SKILLS[0].id

export function meetingSkillById(id: string | null | undefined): MeetingSkill {
  return MEETING_SKILLS.find((s) => s.id === id) ?? MEETING_SKILLS[0]
}

export function buildSummaryPrompt(skillId: string | null | undefined): string {
  return meetingSkillById(skillId).prompt
}
