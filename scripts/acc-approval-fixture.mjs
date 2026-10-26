#!/usr/bin/env node
/**
 * 夹具：造出「ACC 派单正在等人工审批」这个前提（induce），事后清掉（restore）。
 *
 * ★ 为什么前提由夹具造，而不是让 flow 去点「委托给 ACC」：
 *
 *   1. 弹窗里的输入框在无障碍树里是 `[EditText] t="" cd=""`（placeholder 与
 *      label 都不暴露）⇒ 只能按坐标点 ⇒ 而弹窗里任何一处内容改动都会让
 *      sheet 变高、坐标全部位移（实测：sheet 顶部从 y≈1200 变成 y=0，
 *      标题输入框从 y≈1350 移到 y≈324）。⇒ 坐标判据天生脆弱。
 *   2. 「委托给 ACC」这条链在设备上已经由 .maestro/acc-delegate-stop.yaml
 *      覆盖（含派单 + 停止）。这条流要验的是**审批面**：App 看得到待批项、
 *      答得动、答完任务往下走。
 *
 * 所以这里用 API 直接委托一单「要写文件」的任务（智能体必然要审批），
 * 然后等 daemon 把待批项挂上（`GET /api/acc/approvals` 出现 pending）。
 *
 * 用法（由 scripts/maestro-run.mjs 按 .maestro-flows.json 的 fixture 字段调用）：
 *   node scripts/acc-approval-fixture.mjs --induce
 *   node scripts/acc-approval-fixture.mjs --restore
 */
import { makeApi, clearResidual, delegateWithVerify } from './lib/acc-fixture-common.mjs'

const TITLE = 'maestro-acc-approve'
const { api, login } = makeApi()

async function induce() {
  await login()
  await clearResidual(api, 'maestro-')

  const prompt =
    '请在 /tmp/maestro-acc-approve 目录下创建文件 hello.txt，内容写一行 hello，' +
    '然后读取该文件并把内容原样告诉我。'
  // ★ 2026-10-27：写请求失败时**先问 ACC 有没有建出来**再决定要不要重试
  //   （acc_create_task 没有幂等键 ⇒ 盲重试会建出重复任务；
  //     而 ACC 的 MCP 面确实会回 EOF/超时）。
  const res = await delegateWithVerify(api, { title: TITLE, description: prompt, prompt })
  if (!res.ok) {
    throw new Error(`委托失败 ${res.status}：${JSON.stringify(res.body).slice(0, 300)}`)
  }
  console.log(`[fixture] 已委托：task_id=${res.task_id} dispatch_id=${res.dispatch_id}${res.recovered ? '（失败后从 ACC 侧确认已建）' : ''}`)

  // 等 daemon 把待批项挂上（App 的卡片靠它出现）。
  const deadline = Date.now() + 120_000
  while (Date.now() < deadline) {
    await new Promise((r) => setTimeout(r, 3000))
    const a = await api('GET', '/api/acc/approvals?status=pending')
    const items = (a.body && a.body.approvals) || []
    if (items.length > 0) {
      console.log(`[fixture] ✅ 待批项已出现：${items[0].id} title=${JSON.stringify(items[0].title)}`)
      return
    }
  }
  throw new Error('120s 内没有待批项出现 ⇒ 前提没造出来（这条流必须响亮失败）')
}

async function restore() {
  try {
    await login()
    await clearResidual(api, 'maestro-')
  } catch (e) {
    console.log(`[fixture] 还原跳过：${e.message}`)
  }
}

const mode = process.argv[2]
if (mode === '--induce') {
  await induce()
} else if (mode === '--restore') {
  await restore()
} else {
  await restore()
}
