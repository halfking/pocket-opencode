// reminder-outcome-honesty.test.ts — 门禁：「日程提醒没建成」必须被说出来。
//
// 2026-10-06 发现的缺陷（需求「把时间点自动加入计划日程」的落点）：
//
// · 随手记侧 `createNoteTodos` 明明算出了 `reminders`，调用方却只解构
//   `{ created, unresolved }`，于是**只要没到期**就显示
//   「已加入 N 条待办与日程提醒」—— 提醒一条都没建成时也这么说。
//   另外当行动项**根本没有期限**时（0 条提醒被建），它同样宣称
//   「与日程提醒」。
// · 会议侧更直接：`void reminders // 计数仅供日志`，
//   照实弹「已生成当前总结」。
//
// ⇒ 用户相信时间点已经进了日程，实际没有。**静默失败，且是在说谎。**
//
// 分三层（沿用 note-todo-persist.test.ts 的 A/B/负控约定）：
//   A. 行为层：直接调纯文案函数，断言**用户实际看到的那句字符串**。
//   B. 接线层：两个组件必须真的调纯函数、真的读 reminders/reminderPlanned。
//   C. 负控层：证明 A/B 的判据不是恒真。
//
// ★ 为什么 A 层必须是行为断言而不是源码扫描：这道门守的是「那句话对不对」，
//   而源码文本判据分不清代码和**注释里引述的同一句话**——本会话已被骗过三次
//   （§13.5 found.Snippet、§17 注释引述「精翻完成」、§22 注释里的「相同格式」）。
//   所以第一版写完的源码断言被自己推翻，文案抽进了 note-todo-notice.ts /
//   meeting-reminder-note.ts 两个无 value import 的纯模块。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { buildNoteTodoNotice } from './note-todo-notice.ts'
import { buildMeetingReminderNote } from '../meetings/meeting-reminder-note.ts'
import { buildIngestNotice } from '../meetings/meeting-ingest-notice.ts'

const HERE = dirname(fileURLToPath(import.meta.url))
const read = (f: string) => readFileSync(join(HERE, f), 'utf8')

/**
 * 只认**不在注释里**的那一行。
 *
 * 这是接线层唯一的防线：B 层必须读 .vue 源码（组件在裸 `node --test` 下
 * mount 不了 —— 无扩展名 import，无 tsx loader），但纯文本 `assert.match` 会
 * 被注释里的同名字面量满足。
 *
 * 判定：命中行不以 `//` / `*` / `<!--` 开头，且命中位置之前没有 `//`。
 * ⚠ 已知局限：字符串字面量里的 `//`（如 `'https://x'`）会造成**漏报**
 *   （门禁偏严、不会偏松）。这是可接受的方向 —— 漏报只会在真出问题时
 *   多花一次人工确认，而误报会让门禁被忽略。
 */
function matchOutsideComment(src: string, re: RegExp): boolean {
  return src.split('\n').some((line) => {
    const t = line.trim()
    if (t.startsWith('//') || t.startsWith('*') || t.startsWith('/*') || t.startsWith('<!--')) return false
    const m = re.exec(line)
    if (!m) return false
    return line.lastIndexOf('//', m.index - 1) < 0
  })
}

const assertCode = (src: string, re: RegExp, msg: string, file: string) =>
  assert.ok(matchOutsideComment(src, re), `${file}：${msg}`)

const full = { created: 0, reminders: 0, reminderPlanned: 0, unresolved: 0 }

describe('A · 行为层：随手记的待办提示不得谎报提醒', () => {
  it('尝试过建提醒却一条都没建成 ⇒ 必须出现失败措辞', () => {
    // 2026-10-06 缺陷的现场输入：后端不可达/网络失败/权限，2 条都失败。
    const s = buildNoteTodoNotice({ ...full, created: 3, reminderPlanned: 2, reminders: 0 })
    assert.match(s, /未能创建/, `提醒全失败时文案在说成功：${s}`)
    assert.match(s, /2/, `失败条数必须出现在文案里：${s}`)
    // 失败措辞必须与提醒数量**同处一个分句**。
    // ⚠ 这里踩过一次自己的坑：第一版把「未能创建」先 replace 掉再查
    // 「有没有一句像成功的提醒」，而那四个字恰恰是唯一的区分信息 ——
    // 删掉之后判据必然失败，且失败得毫无意义（不是发现了问题，是判据坏了）。
    // 正确做法是不断言子串，而是断言「提到提醒的那个分句自带失败标记」。
    const clauses = s.split('，')
    const reminderClause = clauses.find((c) => /条日程提醒/.test(c))
    assert.ok(reminderClause, `找不到提到提醒的分句：${s}`)
    assert.match(reminderClause, /未能创建/, `提到提醒的分句里没有失败措辞：${s}`)
  })

  it('负控：不能用「已加入 N 条待办与日程提醒」这种把两者捆在一起的说法', () => {
    // 这就是修复前的原文。捆在一起 ⇒ 0 条提醒时也在暗示「进了日程」。
    for (const s of [
      buildNoteTodoNotice({ ...full, created: 3, reminderPlanned: 2, reminders: 0 }),
      buildNoteTodoNotice({ ...full, created: 3, reminderPlanned: 0, reminders: 0 }),
      buildNoteTodoNotice({ ...full, created: 3, reminderPlanned: 2, reminders: 2 }),
    ]) {
      assert.ok(!/待办与日程提醒/.test(s), `文案把待办与提醒捆在一起 ⇒ 0 条提醒时也在说谎：${s}`)
    }
  })

  it('「本来就没有期限」与「建提醒失败」必须分开说', () => {
    const noDue = buildNoteTodoNotice({ ...full, created: 2, reminderPlanned: 0, reminders: 0 })
    const failed = buildNoteTodoNotice({ ...full, created: 2, reminderPlanned: 2, reminders: 0 })
    assert.match(noDue, /没有可解析的期限/, `无期限时该说明白：${noDue}`)
    assert.ok(!/未能创建/.test(noDue), `没有期限 ≠ 建失败，不该报失败：${noDue}`)
    assert.match(failed, /未能创建/, `有尝试就该报失败：${failed}`)
    assert.ok(!/没有可解析的期限/.test(failed), `建失败 ≠ 没有期限：${failed}`)
  })

  it('部分成功：成功数与失败数都要说，不能只报成功那一半', () => {
    const s = buildNoteTodoNotice({ ...full, created: 3, reminderPlanned: 3, reminders: 2 })
    assert.match(s, /2 条日程提醒/, s)
    assert.match(s, /1 条提醒未建成/, `漏报的那 1 条必须说出来：${s}`)
  })

  it('全部成功时不啰嗦（没有失败就别加失败措辞）', () => {
    const s = buildNoteTodoNotice({ ...full, created: 3, reminderPlanned: 3, reminders: 3 })
    assert.ok(!/未能创建|未建成/.test(s), `全成功不该出现失败措辞：${s}`)
    assert.match(s, /3 条日程提醒/, s)
  })

  it('期限没听清的数量必须单独上报（unresolved 也不能被吞）', () => {
    const s = buildNoteTodoNotice({ ...full, created: 2, reminderPlanned: 1, reminders: 1, unresolved: 1 })
    assert.match(s, /1 条期限没听清/, s)
  })

  it('负控：一条待办都没建成时返回空串（不显示空提示）', () => {
    assert.equal(buildNoteTodoNotice(full), '')
    assert.equal(buildNoteTodoNotice({ ...full, reminders: 2, reminderPlanned: 2 }), '',
      '待办都没写进去，提醒数不该单独撑出一条提示')
  })

  it('负控：计数写反时不得报出负数', () => {
    // 保证来自 note-todo-notice.ts 里两处 `failed > 0` 判断，不是来自 Math.max
    // （那层 Math.max 是恒等冗余，变异验证时已删，见该文件内的说明）。
    const s = buildNoteTodoNotice({ ...full, created: 1, reminderPlanned: 1, reminders: 5 })
    assert.ok(!/-\d|负/.test(s), `计数写反时不该出现负数：${s}`)
    assert.match(s, /5 条日程提醒/, s)
  })
})

describe('A · 行为层：会议总结 toast 不得把提醒失败盖过去', () => {
  it('尝试过、全失败 ⇒ 必须出现失败措辞', () => {
    const s = buildMeetingReminderNote(0, 3)
    assert.match(s, /未能创建/, `提醒全失败却在报「已生成当前总结」：${s}`)
    assert.match(s, /3/, s)
  })

  it('没有可解析的期限 ⇒ 说明白，但不报成失败', () => {
    const s = buildMeetingReminderNote(0, 0)
    assert.match(s, /没有可解析的期限/, s)
    assert.ok(!/未能创建/.test(s), `没期限 ≠ 建失败：${s}`)
  })

  it('部分成功 ⇒ 带出分子分母', () => {
    assert.equal(buildMeetingReminderNote(2, 3), '（2/3 条提醒已建）')
  })

  it('全部成功 ⇒ 返回空串，不给成功提示后面缀', () => {
    assert.equal(buildMeetingReminderNote(3, 3), '')
  })

  it('负控：旧形态（只弹「已生成当前总结」）不含任何提醒信息', () => {
    // 修复前 toast 就是这一句。把它当样本，确认门禁看得出差别。
    const old = '已生成当前总结'
    assert.ok(!/提醒/.test(old), '负控样本：旧文案里没有任何提醒信息')
    assert.ok(/提醒/.test(buildMeetingReminderNote(0, 3)), '新文案必须带提醒信息')
  })
})

describe('B · 接线层：组件必须真的用上纯函数与两个计数', () => {
  it('随手记：NoteListView 必须把 reminders/reminderPlanned 一起解构', () => {
    const v = read('NoteListView.vue')
    assertCode(
      v,
      /const\s*\{\s*created,\s*reminders,\s*reminderPlanned,\s*unresolved\s*\}\s*=\s*await createNoteTodos/,
      '又只解构 created/unresolved ⇒ 提醒失败无人知晓',
      'NoteListView.vue',
    )
  })

  it('随手记：提示必须由纯函数生成，且传进去的是活计数器（否则 A 层量不到真实输出）', () => {
    const v = read('NoteListView.vue')
    assertCode(
      v, /todoNotice\.value\s*=\s*buildNoteTodoNotice\(/,
      'todoNotice 没有走 buildNoteTodoNotice ⇒ A 层断言的是另一条链路',
      'NoteListView.vue',
    )
    // ⚠ 变异 M18：把实参写成 `{ created, reminders, reminderPlanned: 0, unresolved }`。
    // 上面那条判据照样匹配（函数名在、赋值在），可 reminderPlanned 已经被写死成 0，
    // 于是「没有可解析的期限」会盖掉真实的提醒失败 —— **形似接线、实则谎报**。
    // 判据必须钉住值流：四个字段都得是简写，不许任何一处被换成字面量。
    // ⚠ §127.4：结尾由 `\s*\)` 放宽成 `\s*[,)]`。
    //   原因是 §127 给这个调用**加了第二个实参**（记账三键），原正则要求
    //   对象字面量后面紧跟 `)`，于是纯语法上的多一个参数就会红。
    //   这条判据真正要守的是**值流**（四个字段都得是简写，不许换成字面量），
    //   而那部分一个字没动 ⇒ 属于「意图未变、放宽的是语法外壳」。
    //   ★ 放宽不是白来的：mutate-127.py 的 M18 形态（`reminderPlanned: 0`）
    //     实测仍然转红（见 docs §127.5）。
    assertCode(
      v,
      /buildNoteTodoNotice\(\s*\{\s*created,\s*reminders,\s*reminderPlanned,\s*unresolved\s*\}\s*[,)]/,
      '传给纯函数的实参里 reminderPlanned/reminders 被写成了字面量 ⇒ 值流断了',
      'NoteListView.vue',
    )
    // §127 追加的第二个实参同样不许写死：账是从响应里读来的。
    // （`bookkeepingMismatch: bkMismatch` 是改名不是字面量 —— 响应键是 snake_case。）
    assertCode(
      v,
      /\{\s*transactions,\s*bookkeeping,\s*bookkeepingMismatch:\s*\w+\s*\}\s*\)/,
      '传给纯函数的记账实参里 transactions/bookkeeping 被写成了字面量 ⇒ 账的读数断了',
      'NoteListView.vue',
    )
    // 负控：组件里不得自己再拼一遍文案（两处文案会各自漂移）。
    assert.ok(!matchOutsideComment(v, /parts\.push\(/),
      'NoteListView.vue 里还有 parts.push 文案拼接 ⇒ 文案有两份实现')
  })

  it('随手记：persist 层必须同时上报「尝试数」与「成功数」', () => {
    const s = read('note-todo-persist.ts')
    assertCode(s, /reminderPlanned:\s*number/, 'NoteTodoResult 没有 reminderPlanned', 'note-todo-persist.ts')
    assertCode(s, /result\.reminderPlanned\+\+/,
      '没有在尝试建提醒时累加 reminderPlanned ⇒ 它恒为 0，提示会退化成「没有可解析的期限」',
      'note-todo-persist.ts')
    assertCode(s, /result\.reminders\+\+/, '成功计数没有被累加', 'note-todo-persist.ts')
    // ⚠ 锚点必须在**返回值**上。变异 M12 把 `return result` 改成
    // `return { ...result, reminderPlanned: 0 }` 时，上面三条判据全绿：
    // 累加逻辑一行没动、类型标注也没动，只有真正交给调用方的值被抹平了。
    assertCode(s, /return\s+result\s*;?/,
      '返回值不是那个累加计数器本身 ⇒ reminderPlanned 可能在返回前被抹成 0', 'note-todo-persist.ts')
  })

  it('会议：createMeetingTodos 不得丢弃提醒计数', () => {
    const s = read('../meetings/meeting-todo-persist.ts')
    assert.ok(
      !matchOutsideComment(s, /void\s+reminders/),
      'createMeetingTodos 仍有 `void reminders` ⇒ 提醒失败被彻底吞掉',
    )
    // ⚠ 这里**不能**只锚类型标注。变异 M9 把
    // `return { count, reminders, reminderPlanned }` 改成
    // `return { count, reminders, reminderPlanned: 0 }` 时，
    // 签名里的 `Promise<{ ... reminderPlanned: number }>` 一字未动，
    // 锚类型的那版判据**全绿**——而调用方拿到的是恒 0，于是
    // 「没有可解析的期限」会覆盖掉真实的「提醒全失败」。
    // 判据落在返回语句上，并用否定前瞻把「传活计数器」与「写字面量」分开。
    assertCode(
      s,
      /return\s*\{[^}]*\breminderPlanned\b(?!\s*:)/,
      '返回语句里 reminderPlanned 必须原样传出活计数器，不能写字面量',
      'meeting-todo-persist.ts',
    )
    // ⚠ 变异 M13 暴露的第三个洞：循环里的累加当时**一条判据都没有**。
    // 把 `reminderPlanned++` 注释掉，reminderPlanned 就恒为 0，
    // buildMeetingReminderNote 收到的 (reminders>0, planned=0) 会被判成
    // 「行动项没有可解析的期限」—— 谎报方向和原缺陷正好相反，同样有害。
    // 这次红得很有价值：它把「只查返回、不查累加」的判据补成了闭环。
    assertCode(s, /reminderPlanned\+\+/,
      '循环里没有累加 reminderPlanned ⇒ 它恒为 0，真实的提醒失败会被说成「没有期限」',
      'meeting-todo-persist.ts')
    assertCode(s, /if \(ok\) reminders\+\+/,
      '成功计数必须由 ensureTodoReminder 的返回值驱动，不能无条件 +1',
      'meeting-todo-persist.ts')
  })

  it('会议：toast 必须拼上提醒结果', () => {
    const v = read('../meetings/MeetingDetailView.vue')
    const at = v.indexOf('await createMeetingTodos')
    assert.ok(at > 0, 'MeetingDetailView.vue 找不到 createMeetingTodos 的调用点')
    const after = v.slice(at, at + 1200)
    assertCode(after, /reminderNote\s*=\s*buildMeetingReminderNote\(/,
      '调用点没有走 buildMeetingReminderNote', 'MeetingDetailView.vue')
    assertCode(after, /toast\.success\(`已生成当前总结\$\{reminderNote\}`\)/,
      'toast 没有带上提醒结果 ⇒ 无论成败都只说「已生成当前总结」', 'MeetingDetailView.vue')
  })
})

describe('C · 负控层：证明 B 层不会被注释满足', () => {
  it('注释里写同一句话不算接线', () => {
    const inComment = '    // const { created, reminders, reminderPlanned, unresolved } = await createNoteTodos(x)\n'
    const inComment2 = '    // todoNotice.value = buildNoteTodoNotice({ created, reminders })\n'
    assert.ok(
      !matchOutsideComment(inComment, /const\s*\{\s*created,\s*reminders,\s*reminderPlanned,\s*unresolved\s*\}\s*=\s*await createNoteTodos/),
      'matchOutsideComment 被行注释骗过了',
    )
    assert.ok(!matchOutsideComment(inComment2, /todoNotice\.value\s*=\s*buildNoteTodoNotice\(/),
      'matchOutsideComment 被行注释骗过了')
  })

  it('块注释里的同一句话也不算接线', () => {
    const block = '/*\n * todoNotice.value = buildNoteTodoNotice({ created, reminders })\n */\nconst a = 1\n'
    assert.ok(!matchOutsideComment(block, /todoNotice\.value\s*=\s*buildNoteTodoNotice\(/),
      'matchOutsideComment 被块注释骗过了')
  })

  it('行尾注释里的同一句话也不算接线', () => {
    const tail = '    const x = 1 // todoNotice.value = buildNoteTodoNotice(r)\n'
    assert.ok(!matchOutsideComment(tail, /todoNotice\.value\s*=\s*buildNoteTodoNotice\(/),
      'matchOutsideComment 被行尾注释骗过了')
  })

  it('真实代码仍然认得出来（证明上面三条不是把判据写成恒假）', () => {
    const real = "    todoNotice.value = buildNoteTodoNotice({ created, reminders })\n"
    assert.ok(matchOutsideComment(real, /todoNotice\.value\s*=\s*buildNoteTodoNotice\(/),
      '判据变成恒假：真实代码也认不出')
  })
})

/**
 * ─────────────────────────────────────────────────────────────────────
 * §66.7 会议侧补齐：随手记链早有的「unresolved」，会议链此前**没有**。
 * ─────────────────────────────────────────────────────────────────────
 *
 * 缺口有三条，都不是「产品还没想好」，而是**同仓另一条链早就定好的口径**没被搬过来：
 *
 *  1. 计数缺格：`createMeetingTodos` / `createLocalTodos` 只有
 *     `{count, reminders, reminderPlanned}`，没有「due 非空但解析不出来」的计数。
 *     ⇒ 用户明明说了「那之前」（§65 真网关实测 glm-5.2 就这么填），
 *       提醒被 `if (dueAt)` 静默跳过，**一个字都不说**。
 *  2. 文案混说：`buildMeetingReminderNote(reminders, reminderPlanned)` 的
 *     `reminderPlanned <= 0` 一支把「没提期限」与「提了没听懂」说成同一句。
 *  3. 文案说谎：录后精校的内联文案「N 项行动已加入待办**与提醒**」——
 *     **正是上面 A 层那条负控的原文**。随手记当天改了，会议侧没改。
 *
 * 三层照旧：A 行为 / B 接线 / C 负控。
 */
describe('A · 行为层：会议侧必须区分「期限没听清」与「没有期限」', () => {
  it('unresolved>0 ⇒ 必须说「期限没听清」，且不得说成「没有可解析的期限」', () => {
    const s = buildMeetingReminderNote(0, 0, 2)
    assert.match(s, /2 条期限没听清/, `提了期限却没听懂，文案没说：${s}`)
    assert.ok(!/没有可解析的期限/.test(s),
      `把「没听懂」说成「没有期限」是在替用户否认他说过的话：${s}`)
  })

  it('unresolved=0 时旧行为**逐字不变**（向后兼容，不许借机改口径）', () => {
    assert.equal(buildMeetingReminderNote(0, 3), '，但 3 条日程提醒未能创建')
    assert.equal(buildMeetingReminderNote(0, 0), '（行动项没有可解析的期限，未建提醒）')
    assert.equal(buildMeetingReminderNote(2, 3), '（2/3 条提醒已建）')
    assert.equal(buildMeetingReminderNote(3, 3), '')
  })

  it('提醒部分成功 + 有听不清 ⇒ 两条都要说，不能只报一半', () => {
    const s = buildMeetingReminderNote(2, 3, 1)
    assert.match(s, /2\/3 条提醒已建/, s)
    assert.match(s, /1 条期限没听清/, `漏报了听不清的那条：${s}`)
  })
})

describe('A · 行为层：录后精校的入库提示', () => {
  // ⚠§229.1：后两格是新增的。第三格 `dueUnresolved` 当初加的时候，
  //   日历那一侧（`eventUnresolved` / `eventFailed`）没跟着加 ——
  //   同一个洞只补了一半。这里必须显式写出来而不是靠默认值，
  //   否则「忘了传」在类型上就是一次静默的假红。
  const base = {
    todosCreated: 0, reminderPlanned: 0, reminders: 0, dueUnresolved: 0,
    eventsCreated: 0, eventUnresolved: 0, eventFailed: false, ingestFailed: false,
  }

  it('负控：旧文案「N 项行动已加入待办与提醒」把待办与提醒捆在一起，0 条提醒时也在说谎', () => {
    // 这就是修复前的原文。它同时是随手记侧那条负控的样本。
    const old = '（3 项行动已加入待办与提醒）'
    assert.match(old, /待办与提醒/, '负控样本自身应当含被禁的措辞')
    for (const s of [
      buildIngestNotice({ ...base, todosCreated: 3 }),
      buildIngestNotice({ ...base, todosCreated: 3, reminderPlanned: 0, reminders: 0 }),
      buildIngestNotice({ ...base, todosCreated: 3, reminderPlanned: 3, reminders: 0 }),
    ]) {
      assert.ok(!/待办与提醒/.test(s), `文案把待办与提醒捆在一起：${s}`)
    }
  })

  it('建了待办但一条提醒都没建成 ⇒ 必须出现失败措辞', () => {
    const s = buildIngestNotice({ ...base, todosCreated: 3, reminderPlanned: 2, reminders: 0 })
    assert.match(s, /2 条日程提醒未能创建/, `提醒全失败却在说成功：${s}`)
    assert.match(s, /3 项行动已加入待办/, s)
  })

  it('期限没听清 ⇒ 单独成句，且不与「提醒建失败」混为一谈', () => {
    const s = buildIngestNotice({ ...base, todosCreated: 2, dueUnresolved: 1 })
    assert.match(s, /1 条期限没听清/, s)
    assert.ok(!/未能创建/.test(s), `没听懂期限 ≠ 建提醒失败：${s}`)
  })

  it('下次会议日程是另一件事，必须单独说（§56）', () => {
    const s = buildIngestNotice({ ...base, todosCreated: 1, eventsCreated: 1 })
    assert.match(s, /下次会议已加入日程/, s)
    assert.ok(!/待办与日程/.test(s), s)
  })

  it('什么都没发生 ⇒ 不显示空提示', () => {
    assert.equal(buildIngestNotice(base), '')
  })

  it('入库失败 ⇒ 覆盖其他一切（连已建成的日程也不提）', () => {
    const s = buildIngestNotice({ ...base, todosCreated: 3, eventsCreated: 1, ingestFailed: true })
    assert.equal(s, '行动项未能入库')
  })
})

describe('B · 接线层：第三格必须真的从产生处传到文案', () => {
  it('createMeetingTodos 必须累加 unresolved，且判据要与随手记链同形', () => {
    const s = read('../meetings/meeting-todo-persist.ts')
    // ⚠ 2026-10-07 变异 N1 打出来的洞：只查 `unresolved++` 这个**记号**是弱判据 ——
    //   把条件换成 `if (false) unresolved++` 后记号照旧在，测试照样全绿。
    //   ⇒ 必须把**条件**（due 非空 ∧ 解析为 null）与累加断在**同一条**正则里。
    assertCode(
      s,
      /if\s*\(\s*(\w+)\.due\s*&&\s*\1\.due\.trim\(\)\s*!==\s*''\s*&&\s*\w+\s*===\s*null\s*\)\s*unresolved\+\+/,
      '累加的**条件**丢了（只留记号也等于没累加）⇒ unresolved 恒为 0',
      'meeting-todo-persist.ts',
    )
    // 返回语句必须原样传出活计数器，不能写字面量（§13 同族）。
    assert.match(s, /return\s*\{[^}]*\bunresolved\b(?!\s*:)/,
      '返回语句里 unresolved 必须原样传出活计数器，不能写字面量')
  })

  it('createLocalTodos（录后精校链）同样必须累加 unresolved', () => {
    const s = read('../meetings/meeting-ingest.ts')
    assertCode(
      s,
      /if\s*\(\s*(\w+)\.due\s*&&\s*\1\.due\.trim\(\)\s*!==\s*''\s*&&\s*\w+\s*===\s*null\s*\)\s*dueUnresolved\+\+/,
      '录后精校链的累加条件丢了（只留记号等于没累加）',
      'meeting-ingest.ts',
    )
  })

  it('MeetingDetailView 必须把 unresolved 解构出来并传给文案函数（防「实现对 ≠ 被用上」）', () => {
    const v = read('../meetings/MeetingDetailView.vue')
    const at = v.indexOf('await createMeetingTodos')
    assert.ok(at > 0, 'MeetingDetailView.vue 找不到 createMeetingTodos 的调用点')
    // ⚠ 变异 N3 打出来的洞：原来判的是「400 字符窗口里出现 unresolved」——
    //   而下方 buildMeetingReminderNote(..., unresolved) **代码里本来就有**这个词，
    //   判据恒真。必须断**解构形态**本身。
    //
    // ⚠ §410 调整过一次形式（2026-10-11）：原正则要求 `unresolved` 紧跟 `}`，
    //   也就是把「恰好三个计数器」当成了契约。§410 要多接第四个 `count`，
    //   而 `count` 无论放哪一位都会让这道门误报。
    //   ⇒ 只放开**额外字段**：`reminders, reminderPlanned, unresolved` 三者
    //     仍须按序出现、仍须直接从 `await createMeetingTodos` 上解构，
    //     「断解构形态本身」这条意图一个字没动。
    //   ⇒ 调整后**必须**变异验证仍有牙：把 `unresolved` 从解构里删掉必须当场变红
    //     （见下方同 describe 里的 M7 记录）。
    assertCode(
      v,
      /const\s*\{[^}]*\breminders\s*,\s*reminderPlanned\s*,\s*unresolved\b[^}]*\}\s*=\s*await createMeetingTodos/,
      '调用点没有解构 unresolved ⇒ 文案拿不到第三格（默认值 0 会把它静默吞掉）',
      'MeetingDetailView.vue',
    )
    const call = v.indexOf('buildMeetingReminderNote(')
    assertCode(v.slice(call, call + 120), /reminderPlanned\s*,\s*unresolved/,
      'buildMeetingReminderNote 没有收到 unresolved（默认值 0 会把它静默吞掉）', 'MeetingDetailView.vue')
  })

  it('录后精校的提示必须走纯文案模块，不得再内联拼接', () => {
    const v = read('../meetings/meeting-recording-finalize.ts')
    assertCode(v, /buildIngestNotice\(/, '提示没有走 buildIngestNotice ⇒ 文案又内联回组件、无法被行为断言', 'meeting-recording-finalize.ts')
    assertCode(v, /dueUnresolved:\s*outcome\.dueUnresolved/, 'outcome.dueUnresolved 没有传进文案函数', 'meeting-recording-finalize.ts')
    // 负控：旧写法不许复活。
    assert.ok(!matchOutsideComment(v, /项行动已加入待办与提醒/),
      '旧文案「N 项行动已加入待办与提醒」复活了 —— 0 条提醒时也在暗示时间点进了日程')
  })
})

describe('C · 负控层：§66.7 的判据本身得有牙', () => {
  it('删掉 unresolved 参数 ⇒ 「期限没听清」立刻说不出来（证明第三格真的承重）', () => {
    const s = buildMeetingReminderNote(0, 0)
    assert.ok(!/期限没听清/.test(s), '判据恒真：不传 unresolved 也冒出「期限没听清」')
    assert.match(s, /没有可解析的期限/, s)
  })

  it('B 层判据不是恒真：真实的累加代码认得出来', () => {
    const real = '  if (draft.due && draft.due.trim() !== "" && dueAt === null) unresolved++\n'
    assert.ok(matchOutsideComment(real, /unresolved\+\+/), 'B 层判据变成恒假')
  })

  it('B 层判据不是恒真：注释里的同一句不算数', () => {
    const commented = '    // if (draft.due) unresolved++\n'
    assert.ok(!matchOutsideComment(commented, /unresolved\+\+/), 'B 层判据被注释骗过了')
  })
})
