// D40: the settings action must use the real sign-out flow.
// Authentication persistence/recovery checks moved to actual Pinia behavior tests:
// src/api/__tests__/auth-owner-activation.test.mjs (snapshot failure, cleanup failure,
// legacy recovery, atomic publication and authenticated retry). Function-shape scans
// cannot model the asynchronous owner barrier or the new authoritative snapshot.
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { readFileSync } from 'node:fs'

const source = readFileSync(new URL('../features/settings/SettingsView.vue', import.meta.url), 'utf8').replace(/\r\n/g, '\n')
const script = /<script[^>]*>([\s\S]*?)<\/script>/.exec(source)?.[1] ?? ''
const cleanScript = script.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\/\/[^\n]*/g, '')
const body = /async function handleLogout\(\)\s*\{([\s\S]*?)\n\}/.exec(cleanScript)?.[1] ?? ''

test('settings sign-out awaits the auth store before navigating to login', () => {
  assert.ok(body, 'Settings sign-out action must exist')
  const logout = body.indexOf('await authStore.logout()')
  const navigation = body.indexOf("router.push('/login')")
  assert.ok(logout >= 0 && navigation > logout)
  assert.doesNotMatch(body, /removeItem\(\s*'pocket_user'\s*\)/)
})

test('a legacy user-key-only sign-out would fail the settings guard', () => {
  const defective = body.replace('await authStore.logout()', "localStorage.removeItem('pocket_user')")
  assert.notEqual(defective, body)
  assert.match(defective, /removeItem\(\s*'pocket_user'\s*\)/)
  assert.equal(defective.includes('await authStore.logout()'), false)
})
