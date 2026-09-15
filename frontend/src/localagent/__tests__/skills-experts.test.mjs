/**
 * skills-experts.test.mjs — SKILL.md 解析 / 内置技能 / 注册表 / 专家白名单。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'

import { parseSkillMd, builtinSkills, SkillRegistry } from '../skills.ts'
import { builtinExperts, getExpert } from '../experts.ts'

test('parseSkillMd:合法 frontmatter', () => {
  const skill = parseSkillMd(
    '---\nname: deep-read\ndescription: "深度阅读长文本"\n---\n\n# 正文\n指引内容',
  )
  assert.ok(skill)
  assert.equal(skill.name, 'deep-read')
  assert.equal(skill.description, '深度阅读长文本')
  assert.match(skill.body, /# 正文/)
})

test('parseSkillMd:缺 name/description 或名字非法返回 null', () => {
  assert.equal(parseSkillMd('---\ndescription: x\n---\nbody'), null)
  assert.equal(parseSkillMd('---\nname: Bad Name\ndescription: x\n---\nbody'), null)
  assert.equal(parseSkillMd('---\nname: ok\ndescription: x\n---\nbody').name, 'ok')
  assert.equal(parseSkillMd('no frontmatter at all'), null)
})

test('内置技能全部合法且含核心技能', () => {
  assert.ok(builtinSkills.length >= 5)
  const names = new Set(builtinSkills.map((s) => s.name))
  for (const expected of ['deep-read', 'trip-plan', 'meeting-notes', 'invoice-extract', 'unit-convert']) {
    assert.ok(names.has(expected), `缺少技能 ${expected}`)
  }
  for (const s of builtinSkills) {
    assert.ok(s.name.length <= 64)
    assert.ok(s.description.length > 0 && s.description.length <= 1024)
    assert.ok(s.body.length > 0)
  }
})

test('SkillRegistry:注册/读取/列表', () => {
  const reg = new SkillRegistry(builtinSkills)
  reg.register({ name: 'custom-x', description: '自定义', body: '...' })
  assert.ok(reg.get('custom-x'))
  assert.ok(!reg.get('nope'))
  assert.ok(reg.list().some((s) => s.name === 'custom-x'))
})

test('专家:内置四个,allowedTools 是工具名子集,正文非空', () => {
  assert.equal(builtinExperts.length, 4)
  assert.equal(getExpert('general').name, 'general')
  assert.equal(getExpert('not-exist').name, 'general') // 兜底
  for (const e of builtinExperts) {
    assert.ok(e.systemPrompt.length > 20, `${e.name} systemPrompt 过短`)
    if (e.allowedTools) {
      const known = new Set([
        'current_time', 'calculate', 'device_info', 'http_fetch',
        'read_file', 'write_file', 'list_files', 'task_plan', 'load_skill',
      ])
      for (const t of e.allowedTools) assert.ok(known.has(t), `${e.name} 引用未知工具 ${t}`)
    }
  }
})
