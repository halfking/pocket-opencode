/**
 * tools.test.mjs — 内置工具:计算器 / 沙箱 FS / task_plan / load_skill /
 * current_time / device_info。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'

import { evaluateExpression } from '../tools/calc.ts'
import { normalizePath, MemoryFsBackend, setFsBackend } from '../tools/fs.ts'
import { createBuiltinTools, resetPlanState, currentPlan } from '../tools/index.ts'
import { SkillRegistry } from '../skills.ts'
import { builtinSkills } from '../skills.ts'

test('计算器:优先级 / 幂运算 / 函数 / 除零', () => {
  assert.equal(evaluateExpression('1+2*3'), 7)
  assert.equal(evaluateExpression('(1+2)*3'), 9)
  assert.equal(evaluateExpression('2^3^2'), 512) // 右结合
  assert.equal(evaluateExpression('sqrt(9)+max(1,5,3)'), 8)
  assert.equal(evaluateExpression('-3+5'), 2)
  assert.equal(evaluateExpression('10 % 3'), 1)
  assert.equal(evaluateExpression('23*7+128'), 289)
  assert.equal(evaluateExpression('pi'), Math.PI)
  assert.throws(() => evaluateExpression('1/0'), /除数为 0/)
  assert.throws(() => evaluateExpression('alert(1)'), /未知函数/)
  assert.throws(() => evaluateExpression('1+;'), /无法解析|多余/)
})

test('normalizePath:拒绝越界与反斜杠,容忍首尾斜杠与 .', () => {
  assert.equal(normalizePath('/notes/todo.md'), 'notes/todo.md')
  assert.equal(normalizePath('./a//b/'), 'a/b')
  assert.equal(normalizePath(''), '')
  assert.throws(() => normalizePath('../etc/passwd'), /\.\./)
  assert.throws(() => normalizePath('a/../../b'), /\.\./)
  assert.throws(() => normalizePath('a\\b'), /反斜杠/)
})

test('MemoryFsBackend:写读列删', async () => {
  const mem = new Map()
  const store = {
    getItem: (k) => (mem.has(k) ? mem.get(k) : null),
    setItem: (k, v) => mem.set(k, v),
    removeItem: (k) => mem.delete(k),
    key: (i) => [...mem.keys()][i] ?? null,
    get length() {
      return mem.size
    },
  }
  const fs = new MemoryFsBackend(store)
  await fs.write('notes/todo.md', '买牛奶')
  assert.equal(await fs.read('notes/todo.md'), '买牛奶')
  await fs.write('notes/idea.md', 'x')
  assert.deepEqual(await fs.list('notes'), ['idea.md', 'todo.md'])
  assert.deepEqual(await fs.list(''), ['notes/'])
  await fs.remove('notes/idea.md')
  await assert.rejects(() => fs.read('notes/idea.md'), /不存在/)
  await assert.rejects(() => fs.remove('notes/idea.md'), /不存在/)
})

test('task_plan:set / update / 非法输入', async () => {
  const tools = createBuiltinTools({ skills: new SkillRegistry(builtinSkills) })
  const plan = tools.find((t) => t.name === 'task_plan')
  resetPlanState()
  const events = []
  const ctx = { signal: new AbortController().signal, emit: (e) => events.push(e) }

  const setRes = await plan.execute(
    { action: 'set', items: JSON.stringify([{ title: '查天气' }, { title: '订票', status: 'in_progress' }, { title: '打包' }]) },
    ctx,
  )
  assert.ok(setRes.ok)
  assert.equal(currentPlan().length, 3)
  assert.equal(events.filter((e) => e.type === 'plan').length, 1)

  const upd = await plan.execute({ action: 'update', index: '1', status: 'done' }, ctx)
  assert.ok(upd.ok)
  assert.equal(currentPlan()[0].status, 'done')

  assert.ok(!(await plan.execute({ action: 'update', index: '99', status: 'done' }, ctx)).ok)
  assert.ok(!(await plan.execute({ action: 'update', index: '1', status: 'nope' }, ctx)).ok)
  assert.ok(!(await plan.execute({ action: 'set', items: 'not-json' }, ctx)).ok)
  assert.ok(!(await plan.execute({ action: 'bad' }, ctx)).ok)
})

test('load_skill:命中与未命中', async () => {
  const tools = createBuiltinTools({ skills: new SkillRegistry(builtinSkills) })
  const load = tools.find((t) => t.name === 'load_skill')
  const ctx = { signal: new AbortController().signal }
  const hit = await load.execute({ name: 'deep-read' }, ctx)
  assert.ok(hit.ok)
  assert.match(hit.result, /深度阅读/)
  const miss = await load.execute({ name: 'nope' }, ctx)
  assert.ok(!miss.ok)
  assert.match(miss.error, /技能不存在/)
})

test('current_time / device_info 可执行且低风险', async () => {
  const tools = createBuiltinTools({ skills: new SkillRegistry(builtinSkills) })
  const ctx = { signal: new AbortController().signal }
  const time = tools.find((t) => t.name === 'current_time')
  const timeRes = await time.execute({}, ctx)
  assert.ok(timeRes.ok)
  assert.match(timeRes.result, /星期/)
  const dev = tools.find((t) => t.name === 'device_info')
  assert.ok((await dev.execute({}, ctx)).ok)
  for (const t of [time, dev]) assert.equal(t.risk, 'low')
})

test('read/write/list_files:走沙箱后端,write_file 为 medium 风险', async () => {
  const mem = new Map()
  const store = {
    getItem: (k) => (mem.has(k) ? mem.get(k) : null),
    setItem: (k, v) => mem.set(k, v),
    removeItem: (k) => mem.delete(k),
    key: (i) => [...mem.keys()][i] ?? null,
    get length() {
      return mem.size
    },
  }
  setFsBackend(new MemoryFsBackend(store))
  const tools = createBuiltinTools({ skills: new SkillRegistry(builtinSkills) })
  const ctx = { signal: new AbortController().signal }
  const write = tools.find((t) => t.name === 'write_file')
  const read = tools.find((t) => t.name === 'read_file')
  const list = tools.find((t) => t.name === 'list_files')

  assert.equal(write.risk, 'medium')
  const w = await write.execute({ path: 'notes/todo.md', content: '买牛奶' }, ctx)
  assert.ok(w.ok)
  const r = await read.execute({ path: 'notes/todo.md' }, ctx)
  assert.equal(r.result, '买牛奶')
  const l = await list.execute({ path: 'notes' }, ctx)
  assert.deepEqual(l.result, 'todo.md')
  const bad = await read.execute({ path: '../../etc/passwd' }, ctx)
  assert.ok(!bad.ok)
  setFsBackend(null)
})
