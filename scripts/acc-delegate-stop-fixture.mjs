#!/usr/bin/env node
/**
 * 夹具：给 .maestro/acc-delegate-stop.yaml 清场（induce = 清，跑完 = 再清）。
 *
 * ★ 为什么这条流也需要夹具（2026-10-26 12:30 实测）：
 *   失败现场的屏上**同时有两条**「maestro-acc-stop」卡片
 *   （一条是上一轮跑完残留的，一条是这一轮刚建的）：
 *
 *     maestro-acc-stop   进行中 · 38 秒
 *     maestro-acc-stop   进行中 · 35 秒
 *
 *   ⇒ 长按「maestro-acc-stop」时 Maestro 选中的是**哪一条**取决于渲染顺序
 *   ⇒ 「停止」可能作用在**上一轮**那条上 ⇒ 末尾断言对错了行。
 *   ⇒ 残留必须由夹具在跑之前清掉（acc-approval 那条流早就有这个夹具，
 *     这条流此前是裸流 ⇒ 等于没清）。
 *
 * 用法（由 scripts/maestro-run.mjs 按 .maestro-flows.json 的 fixture 字段调用）：
 *   node scripts/acc-delegate-stop-fixture.mjs --induce
 *   node scripts/acc-delegate-stop-fixture.mjs --restore
 */
import { makeApi, clearResidual } from './lib/acc-fixture-common.mjs'

// ★ 探针标题必须与 .maestro/acc-delegate-stop.yaml 的 inputText 逐字节一致
//   （flow 靠它长按选卡、断言卡片消失；这里靠它清残留）。
//   命名约束：**无连字符的驼峰** —— zh-CN locale 下 GBoard 会把带连字符的
//   ASCII 改写（实测 "maestro-acc-stop" 落库为「马estrogen－ACC－」），
//   literal 断言必红；纯字母驼峰不受影响（2026-11-19 实测定案）。
export const PROBE_TITLE = 'maestroAccStop'

const { api, login } = makeApi()

async function main() {
  await login()
  await clearResidual(api, 'maestro')
}

try {
  await main()
} catch (e) {
  console.error(`[fixture] ❌ ${e.message}`)
  process.exit(1)
}
