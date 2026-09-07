import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { buildSummaryPrompt, meetingSkillById, MEETING_SKILLS } from './meeting-skills.ts'

describe('meeting-skills', () => {
  it('falls back to rolling minutes for unknown skill ids', () => {
    assert.equal(meetingSkillById('nope').id, 'meeting-minutes')
    assert.equal(meetingSkillById(null).id, 'meeting-minutes')
  })

  it('keeps each skill prompt distinct', () => {
    const prompts = MEETING_SKILLS.map((s) => buildSummaryPrompt(s.id))
    assert.equal(new Set(prompts).size, MEETING_SKILLS.length)
    assert.match(buildSummaryPrompt('action-items'), /行动项|待办/)
  })
})
