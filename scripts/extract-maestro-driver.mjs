/** 从 maestro-client.jar 里解出 driver APK，确认包名/版本是否与手头那份一致 */
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { execFileSync } from 'node:child_process'

const JAR = 'C:/workspace/openpocket/logs/maestro/dist/maestro/lib/maestro-client.jar'
const OUT = 'C:/workspace/openpocket/logs/maestro/driver-extracted'
mkdirSync(OUT, { recursive: true })

const listing = execFileSync('C:/Program Files/Eclipse Adoptium/jdk-21.0.12.101-hotspot/bin/jar.exe',
  ['tf', JAR], { encoding: 'utf8', maxBuffer: 33554432 })
const apks = listing.split(/\r?\n/).filter((l) => /\.apk$/i.test(l.trim()))
console.log('jar 内 APK 条目:')
for (const a of apks) console.log('  ' + a.trim())

for (const a of apks) {
  const name = a.trim().split('/').pop()
  execFileSync('C:/Program Files/Eclipse Adoptium/jdk-21.0.12.101-hotspot/bin/jar.exe',
    ['xf', JAR, a.trim()], { cwd: OUT })
  console.log(`解出: ${name} -> ${OUT}`)
}
