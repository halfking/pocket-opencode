import { test } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, writeFile, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { createServer } from 'node:http'

const execute = promisify(execFile)
const cli = new URL('../cdp.mjs', import.meta.url).pathname

async function runProbe(appId, expression = 'document.title') {
  const directory = await mkdtemp(join(tmpdir(), 'pocket-cdp-test-'))
  const adbLog = join(directory, 'adb.log')
  const adbPath = join(directory, 'adb')
  const preloadPath = join(directory, 'preload.mjs')
  await writeFile(adbPath, '#!/bin/sh\nprintf "%s\\n" "$*" >> "$CDP_TEST_ADB_LOG"\ncase "$*" in\n devices) printf "List of devices attached\\nfixture device\\n" ;;\n *pidof*) printf "123\\n" ;;\nesac\n', { mode: 0o700 })
  await writeFile(preloadPath, `
    globalThis.WebSocket = class extends EventTarget {
      constructor() { super(); queueMicrotask(() => this.dispatchEvent(new Event('open'))) }
      send(raw) {
        const { id } = JSON.parse(raw)
        queueMicrotask(() => this.dispatchEvent(new MessageEvent('message', {
          data: JSON.stringify({ id, result: { result: { value: 'device page' } } })
        })))
      }
      close() {}
    }
  `)
  const server = createServer((request, response) => {
    response.setHeader('Content-Type', 'application/json')
    response.end(JSON.stringify([{ type: 'page', url: 'https://localhost/', webSocketDebuggerUrl: 'ws://fixture' }]))
  })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  try {
    let result
    try {
      result = await execute(process.execPath, ['--import', preloadPath, cli, 'eval', expression], {
        timeout: 2500,
        env: { ...process.env, POCKET_SERIAL: 'fixture', POCKET_APP_ID: appId,
          POCKET_ADB_BIN: adbPath, POCKET_CDP_PORT: String(server.address().port), CDP_TEST_ADB_LOG: adbLog },
      })
    } catch (error) {
      result = error
    }
    const commands = await readFile(adbLog, 'utf8').catch(() => '')
    return { result, commands }
  } finally {
    await new Promise(resolve => server.close(resolve))
    await rm(directory, { recursive: true, force: true })
  }
}

test('CDP CLI exits promptly after a successful response', async () => {
  const { result } = await runProbe('com.kaixuan.opencode.pocket')
  assert.equal(result.stdout.trim(), 'device page')
  assert.equal(result.killed, undefined, 'completed CDP requests must release their timers')
})

test('CDP CLI targets the explicitly selected coexisting package', async () => {
  const { commands } = await runProbe('com.kaixuan.opencode.pocket.audit')
  assert.match(commands, /pidof com\.kaixuan\.opencode\.pocket\.audit\n/)
})

test('CDP CLI rejects an invalid package before contacting a device', async () => {
  const { result, commands } = await runProbe('com.example; invalid')
  assert.equal(result.code, 2)
  assert.match(result.stderr, /POCKET_APP_ID/)
  assert.equal(commands, '')
})
