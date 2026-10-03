// test-file-census-reporter.mjs — node --test 的**复合 reporter**：
// 一边原样转发明细（委托给内置 spec reporter），一边把「哪个文件真的产出了用例」写进
// POCKET_TEST_CENSUS 指定的 JSON。
//
// 为什么需要它：npm script 里的 `node --test "src/**/*.test.mjs"` 是 **glob**，
// 而 node 不会告诉你它到底展开了哪些文件。glob 写错一个字符、路径分隔符不对、
// 或者新测试落在 glob 之外时，node 只会**安静地少跑甚至什么都不跑**，退出码仍是 0。
// 「959 个用例全绿」和「91 个文件都被执行了」是两件事，本 reporter 把后者变成可核对的事实。
//
// 之所以委托内置 spec 而不是自己打印：spec 的输出格式（失败堆栈、耗时、汇总）由 Node
// 维护，自己手写一份只会漂移；本文件只做两件额外的事——计数与落盘。
import { spec } from 'node:test/reporters'
import { writeFileSync } from 'node:fs'
import { Transform } from 'node:stream'

// 注意：spec 是**工厂函数**，必须调用才拿到流（`spec()` 返回 Transform）
const inner = spec()
inner.on('data', (chunk) => process.stdout.write(chunk))

/** @type {Record<string, number>} 绝对路径 → 该文件产出的**真实用例**数 */
const census = {}

/**
 * node 会为每个测试文件本身也发一个 test:pass，而它的 name 就是文件路径；
 * 真实用例的 name 是用例标题。两者必须分开：
 *   - 文件级事件只用来登记「这个文件确实被 node 跑到了」（计数 0），
 *     这样"跑到了却一个用例都没产出"和"压根没被执行"在下游是两个不同的诊断；
 *   - 真实用例才累加计数，否则一个空文件也会被算成 1，"空转绿灯"就判不出来了。
 */
function isFileLevel(data) {
  return Boolean(data && data.file) && (data.name === data.file || String(data.name).endsWith('.test.mjs'))
}
function isRealCase(data) {
  return Boolean(data && data.file) && !isFileLevel(data)
}

const reporter = new Transform({
  objectMode: true,
  transform(event, _enc, cb) {
    const data = event.data
    if (isFileLevel(data) && (event.type === 'test:pass' || event.type === 'test:fail')) {
      census[data.file] ??= 0
    } else if (isRealCase(data) && (event.type === 'test:pass' || event.type === 'test:fail')) {
      census[data.file] = (census[data.file] ?? 0) + 1
    }
    // 明细照旧交给 spec，测试失败时的输出与不加本 reporter 时完全一致
    if (!inner.write(event)) inner.once('drain', cb)
    else cb()
  },
  flush(cb) {
    const target = process.env.POCKET_TEST_CENSUS
    if (target) {
      try {
        writeFileSync(target, JSON.stringify(census), 'utf8')
      } catch (e) {
        process.stderr.write(`⚠️ 测试普查落盘失败：${e.message}\n`)
      }
    }
    inner.end()
    cb()
  },
})

export default reporter
