#!/usr/bin/env node
// Builds the existing Vite bundle for ArkWeb and copies only generated assets
// into the HarmonyOS rawfile directory. This intentionally does not call the
// Capacitor CLI: Capacitor supports the existing Android/iOS shells only.

import { existsSync } from 'node:fs'
import { cp, mkdir, readdir, readFile, rm } from 'node:fs/promises'
import { spawnSync } from 'node:child_process'
import { loadEnv } from 'vite'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const frontendRoot = path.resolve(__dirname, '..')
const harmonyRawfile = path.join(frontendRoot, 'harmony', 'entry', 'src', 'main', 'resources', 'rawfile')
const env = process.argv[2] ?? 'dev'
const modeByEnv = { dev: 'harmony-dev', staging: 'staging', prod: 'production' }
const mode = modeByEnv[env]

if (!mode) {
  console.error('Usage: node scripts/build-harmony.mjs <dev|staging|prod>')
  process.exit(1)
}

const envFile = path.join(frontendRoot, `.env.${mode}`)
if (!existsSync(envFile) && !process.env.VITE_API_BASE) {
  console.error(`[build-harmony] missing ${envFile}; copy harmony.env.example to ${path.basename(envFile)} or set VITE_API_BASE`)
  process.exit(1)
}

const profileEnv = loadEnv(mode, frontendRoot, '')
const apiBase = process.env.VITE_API_BASE || profileEnv.VITE_API_BASE
if (!apiBase) {
  console.error('[build-harmony] VITE_API_BASE must be set for every HarmonyOS build')
  process.exit(1)
}

let apiUrl
try {
  apiUrl = new URL(apiBase)
} catch {
  console.error('[build-harmony] VITE_API_BASE must be an absolute http(s) URL')
  process.exit(1)
}
if (!['http:', 'https:'].includes(apiUrl.protocol) || ['localhost', '127.0.0.1', '::1'].includes(apiUrl.hostname)) {
  console.error('[build-harmony] VITE_API_BASE must be a device-reachable non-loopback http(s) URL')
  process.exit(1)
}

const fast = process.env.MOBILE_FAST === '1'
const build = spawnSync('npm', ['run', fast ? 'build:fast' : 'build', '--', '--mode', mode], {
  cwd: frontendRoot,
  env: { ...process.env, FORCE_COLOR: '1' },
  stdio: 'inherit',
  shell: true,
})
if (build.status !== 0) process.exit(build.status ?? 1)

const dist = path.join(frontendRoot, 'dist')
const index = path.join(dist, 'index.html')
const assets = path.join(dist, 'assets')
if (!existsSync(index) || !existsSync(assets)) {
  console.error('[build-harmony] Vite output is incomplete: expected dist/index.html and dist/assets/')
  process.exit(1)
}

await rm(harmonyRawfile, { recursive: true, force: true })
await mkdir(harmonyRawfile, { recursive: true })
await cp(dist, harmonyRawfile, { recursive: true })

const assetEntries = await readdir(path.join(harmonyRawfile, 'assets'))
if (!existsSync(path.join(harmonyRawfile, 'index.html')) || !assetEntries.some((entry) => entry.endsWith('.js'))) {
  console.error('[build-harmony] rawfile sync verification failed')
  process.exit(1)
}

const assetFiles = assetEntries.filter((entry) => entry.endsWith('.js'))
const hasApiBase = await Promise.all(
  assetFiles.map(async (entry) => (await readFile(path.join(harmonyRawfile, 'assets', entry), 'utf8')).includes(apiBase)),
).then((results) => results.some(Boolean))
if (!hasApiBase) {
  console.error('[build-harmony] bundle verification failed: VITE_API_BASE was not embedded in rawfile assets')
  process.exit(1)
}

console.log(`[build-harmony] OK — ${env}; synced ${path.relative(frontendRoot, harmonyRawfile)}`)
