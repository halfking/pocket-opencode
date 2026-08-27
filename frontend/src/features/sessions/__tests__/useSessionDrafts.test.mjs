/**
 * useSessionDrafts 测试（工程无组件测试基建，SessionComposer 的行为规则
 * 全部下沉到本 composable / 纯函数层，node --test 覆盖）：
 *   - 指令模板 chips：文案/顺序冻结、仅"停下"二次确认、模板文本=label
 *   - 转写 / initialText 追加纪律（不覆盖已有输入）
 *   - 草稿 load / 500ms 防抖落盘 / flush / clear（send 清理）
 *   - 草稿 key 切换（targets 换目标）
 *   - 存储降级（Node 无 SQLite → 内存，不抛错）
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { ref } from 'vue'

import {
  DRAFT_SAVE_DEBOUNCE_MS,
  QUICK_COMMANDS,
  appendToDraft,
  applyInitialText,
  shouldConfirmCommand,
  truncateChipLabel,
  useSessionDrafts,
} from '../useSessionDrafts.ts'
import { MemoryDraftStore } from '../../../native/draftStore.ts'

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

// ── 指令模板（契约 §4 冻结） ──

test('指令模板：文案与顺序按契约 §4 冻结', () => {
  assert.deepEqual(
    QUICK_COMMANDS.map((c) => c.label),
    ['继续', '停下', '总结当前进展', '跑测试', '忽略错误继续'],
  )
})

test('指令模板：一点即发的模板文本 = chip 文案', () => {
  for (const cmd of QUICK_COMMANDS) {
    assert.equal(cmd.message, cmd.label, `${cmd.label} 的 message 应等于 label`)
  }
})

test('指令模板：仅"停下"需要二次确认', () => {
  const stop = QUICK_COMMANDS.find((c) => c.label === '停下')
  assert.ok(stop, '停下指令存在')
  assert.equal(shouldConfirmCommand(stop), true)
  assert.ok(stop.confirmText, '停下必须带确认文案')
  for (const cmd of QUICK_COMMANDS) {
    if (cmd.label !== '停下') {
      assert.equal(shouldConfirmCommand(cmd), false, `${cmd.label} 不应需要确认`)
    }
  }
})

// ── 追加纪律（voice 转写 / ?prompt= 预填共用） ──

test('appendToDraft：空草稿填充、已有内容空格衔接（转写不覆盖输入）', () => {
  assert.equal(appendToDraft('', '你好'), '你好')
  assert.equal(appendToDraft('已有', '你好'), '已有 你好')
  assert.equal(appendToDraft('已有  ', '你好'), '已有 你好')
  assert.equal(appendToDraft('已有', '   '), '已有')
})

test('applyInitialText：一次性预填沿用追加纪律', () => {
  assert.equal(applyInitialText('', '?prompt=文本'), '?prompt=文本')
  assert.equal(applyInitialText('草稿', '新增'), '草稿 新增')
})

test('truncateChipLabel：~12 字符截断', () => {
  assert.equal(truncateChipLabel('短的'), '短的')
  assert.equal(truncateChipLabel('a'.repeat(12)), 'a'.repeat(12), '恰好 12 不截断')
  assert.equal(truncateChipLabel('b'.repeat(13)), 'b'.repeat(12) + '…')
})

// ── 草稿读写 ──

test('默认防抖窗口 = 契约 §4 的 500ms', () => {
  assert.equal(DRAFT_SAVE_DEBOUNCE_MS, 500)
})

test('useSessionDrafts：挂载载入已存草稿', async () => {
  const store = new MemoryDraftStore()
  await store.saveDraft('sess_1', '上次没发完的话', 1)

  const drafts = useSessionDrafts({ sessionId: () => 'sess_1', store })
  await drafts.hydrate()
  assert.equal(drafts.text.value, '上次没发完的话')
})

test('useSessionDrafts：载入不覆盖已有输入（预填/用户输入优先）', async () => {
  const store = new MemoryDraftStore()
  await store.saveDraft('sess_1', '旧草稿', 1)

  const drafts = useSessionDrafts({ sessionId: () => 'sess_1', store })
  drafts.text.value = '用户已输入'
  await drafts.hydrate()
  assert.equal(drafts.text.value, '用户已输入')
})

test('useSessionDrafts：输入防抖落盘（窗口内不写、过后写最终值）', async () => {
  const store = new MemoryDraftStore()
  const drafts = useSessionDrafts({
    sessionId: () => 'sess_1',
    store,
    debounceMs: 40,
    now: () => 42,
  })

  drafts.text.value = 'v1'
  await sleep(10)
  assert.equal(await store.getDraft('sess_1'), null, '防抖窗口内不落盘')

  await sleep(90)
  const saved = await store.getDraft('sess_1')
  assert.equal(saved.text, 'v1')
  assert.equal(saved.updatedAt, 42, '写入带 updated_at')

  // 连续输入合并为一次最终值
  drafts.text.value = 'v2'
  drafts.text.value = 'v2-plus'
  await sleep(160)
  assert.equal((await store.getDraft('sess_1')).text, 'v2-plus')
})

test('useSessionDrafts：清空输入删除草稿行（不留空草稿）', async () => {
  const store = new MemoryDraftStore()
  const drafts = useSessionDrafts({ sessionId: () => 'sess_1', store, debounceMs: 20 })

  drafts.text.value = '临时'
  await sleep(80)
  assert.equal((await store.getDraft('sess_1')).text, '临时')

  drafts.text.value = ''
  await sleep(80)
  assert.equal(await store.getDraft('sess_1'), null)
})

test('useSessionDrafts：flush() 取消挂起防抖、立即落盘', async () => {
  const store = new MemoryDraftStore()
  const drafts = useSessionDrafts({ sessionId: () => 'sess_1', store, debounceMs: 500 })

  drafts.text.value = '立刻保存'
  await drafts.flush()
  assert.equal((await store.getDraft('sess_1')).text, '立刻保存')
})

test('useSessionDrafts：clear() 立即清空文本并删行（send 时调用）', async () => {
  const store = new MemoryDraftStore()
  const drafts = useSessionDrafts({ sessionId: () => 'sess_1', store, debounceMs: 500 })

  drafts.text.value = '要发送的内容'
  await drafts.clear()
  assert.equal(drafts.text.value, '')
  assert.equal(await store.getDraft('sess_1'), null)
})

test('useSessionDrafts：切换草稿 key（targets 换目标）旧草稿落盘、新草稿载入', async () => {
  const store = new MemoryDraftStore()
  await store.saveDraft('sess_B', 'B 的草稿', 1)

  const key = ref('sess_A')
  const drafts = useSessionDrafts({ sessionId: () => key.value, store, debounceMs: 500 })

  drafts.text.value = 'A 的输入'
  key.value = 'sess_B' // 模拟 update:target 后 modelTarget 变化
  await sleep(30)

  // 旧 key 的草稿已落盘（不带防抖等待）
  assert.equal((await store.getDraft('sess_A')).text, 'A 的输入')
  // 当前输入已切换到新 key 的草稿
  await drafts.hydrate()
  assert.equal(drafts.text.value, 'B 的草稿')
})

// ── 存储降级 ──

test('resolveDraftStore：Node 无 SQLite 基建时降级内存存储且不抛错', async () => {
  const { resolveDraftStore, sharedMemoryDraftStore } = await import('../useSessionDrafts.ts')
  const store = await resolveDraftStore()
  await store.saveDraft('fallback_key', 'x', 1)
  assert.equal((await sharedMemoryDraftStore().getDraft('fallback_key')).text, 'x')
})
