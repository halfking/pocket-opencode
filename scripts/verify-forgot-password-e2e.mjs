/**
 * verify-forgot-password-e2e.mjs
 *
 * Drives the full forgot-password flow against a live pocketd and answers one
 * question: on a deployment with NO SMTP configured, can a real user actually
 * recover their password?
 *
 * The instance under test is started with POCKET_SMTP_DEBUG_ECHO=true and no
 * POCKET_SMTP_HOST, which makes notify.NewClient() return nil and makes
 * send-code echo the code in the response body. That is the only way to drive
 * the flow without a real mail server.
 *
 * A throwaway account is registered; the operator's own account is never
 * touched. Sequence:
 *   1. healthz
 *   2. send-code(register) -> debug_code
 *   3. register the throwaway account
 *   4. send-code(reset)    -> debug_code
 *   5. forgot-password     -> 200
 *   6. NEGATIVE CONTROL: login with the OLD password must FAIL
 *   7. login with the NEW password must SUCCEED
 *
 * Step 6 is what makes this decisive: without it, a 200 from step 5 only proves
 * the handler returned, not that the password actually changed.
 */
const BASE = process.env.PROBE_BASE || 'http://127.0.0.1:8098'
const EMAIL = process.env.PROBE_EMAIL || 'probe-auth@invalid.test'
const USER = 'probe_auth_' + Date.now().toString(36)
const OLD_PW = 'Old-Passw0rd!probe'
const NEW_PW = 'New-Passw0rd!probe'

let failures = 0
function step(name, ok, detail) {
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? '  ' + detail : ''}`)
  if (!ok) failures++
}

async function post(path, body) {
  const res = await fetch(BASE + path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  const text = await res.text()
  let json
  try { json = JSON.parse(text) } catch { json = null }
  return { status: res.status, json, text }
}

async function code(purpose) {
  const r = await post('/api/auth/send-code', { email: EMAIL, purpose })
  return r
}

// 1. health
let hz
try {
  const res = await fetch(BASE + '/healthz')
  hz = await res.text()
} catch (e) {
  console.log(`FAIL  1.healthz  instance unreachable: ${e.message}`)
  process.exit(1)
}
step('1.healthz', true, hz.trim())

// 2. register-purpose code
const rc = await code('register')
step('2.send-code(register)', rc.status === 200, `status=${rc.status} body=${rc.text.slice(0, 120)}`)
const regCode = rc.json && rc.json.debug_code
step('2.debug_code present (SMTP unconfigured => email never sent)', !!regCode,
  regCode ? 'code echoed' : 'NO debug_code -> cannot drive flow without a mail server')

// 3. register throwaway account
const reg = await post('/api/auth/register', {
  email: EMAIL, code: regCode, username: USER, password: OLD_PW,
})
step('3.register', reg.status === 200, `status=${reg.status} body=${reg.text.slice(0, 160)}`)

// 4. reset-purpose code
const sc = await code('reset')
step('4.send-code(reset)', sc.status === 200, `status=${sc.status} body=${sc.text.slice(0, 120)}`)
const resetCode = sc.json && sc.json.debug_code
step('4.debug_code present', !!resetCode, resetCode ? 'code echoed' : 'NO debug_code')

// 5. forgot-password
const fp = await post('/api/auth/forgot-password', {
  email: EMAIL, code: resetCode, new_password: NEW_PW,
})
step('5.forgot-password', fp.status === 200, `status=${fp.status} body=${fp.text.slice(0, 120)}`)

// 6. NEGATIVE CONTROL: the old password must no longer work
const oldLogin = await post('/api/auth/login', { username: USER, password: OLD_PW })
step('6.NEG-CTRL old password rejected', oldLogin.status !== 200,
  `status=${oldLogin.status} (200 would mean the password never changed)`)

// 7. the new password must work
const newLogin = await post('/api/auth/login', { username: USER, password: NEW_PW })
step('7.new password accepted', newLogin.status === 200, `status=${newLogin.status}`)

console.log(`\nthrowaway account left in DB: ${EMAIL} / ${USER}`)
console.log(failures === 0 ? 'RESULT: all steps behaved as coded' : `RESULT: ${failures} step(s) failed`)
process.exit(failures === 0 ? 0 : 1)
