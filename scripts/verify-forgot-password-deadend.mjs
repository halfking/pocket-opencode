/**
 * verify-forgot-password-deadend.mjs
 *
 * The production-shaped case: no SMTP, no DEBUG_ECHO. This is the shape a real
 * deployment has, and it is the one that used to strand users silently.
 *
 * What it asserts, and why each assertion matters:
 *   A. send-code still returns 200        — anti-enumeration is deliberate, we
 *                                          are NOT changing that behaviour.
 *   B. delivery === 'none'               — the new signal is present...
 *   C. debug_code is absent              — ...and it is the real deployment
 *                                          shape, not the dev echo.
 *   D. the frontend gate blocks on A+B+C — importing the SAME module the view
 *                                          uses, so this is the real decision
 *                                          path, not a re-implementation.
 *
 * If D ever passes while delivery is 'none' and no code is echoed, the silent
 * stranding is back.
 */
const BASE = process.env.PROBE_BASE || 'http://127.0.0.1:8097'
const EMAIL = process.env.PROBE_EMAIL || 'probe-auth3@invalid.test'

let failures = 0
function step(name, ok, detail) {
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? '  ' + detail : ''}`)
  if (!ok) failures++
}

const res = await fetch(BASE + '/api/auth/send-code', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ email: EMAIL, purpose: 'reset' }),
})
const body = await res.json()

step('A.send-code still 200 (anti-enumeration preserved)', res.status === 200, `status=${res.status}`)
step('B.delivery === "none"', body.delivery === 'none', `delivery=${JSON.stringify(body.delivery)}`)
step('C.no debug_code (production shape)', !body.debug_code, `debug_code=${JSON.stringify(body.debug_code)}`)

const { judgeCodeDelivery } = await import(
  new URL('../frontend/src/features/auth/code-delivery.ts', import.meta.url).href
)
const verdict = judgeCodeDelivery(body)
step('D.frontend gate BLOCKS the stepper', verdict.advance === false,
  `advance=${verdict.advance} error=${JSON.stringify(verdict.error)}`)
step('D.error is user-facing and actionable', !!verdict.error && verdict.error.includes('邮件服务器'),
  verdict.error)

console.log(failures === 0
  ? '\nRESULT: dead-end is detected and surfaced instead of silently stranding the user'
  : `\nRESULT: ${failures} step(s) failed`)
process.exit(failures === 0 ? 0 : 1)
