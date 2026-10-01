// decode-hier.mjs — 按指定编码读回 `maestro hierarchy` 的原始输出。
//
// 为什么需要：Maestro 是 JVM 程序，Windows 中文环境下 JVM 默认按 GBK 往 stdout 写，
// 而 Node 的 readFileSync('utf8') 会把 GBK 字节当成坏 UTF-8，读出来全是乱码。
// 判据是能不能读出「登录」「用户名」这类已知文案——读不出就是编码选错了，不是 dump 坏了。
//
// 用法：node scripts/decode-hier.mjs <file> [gbk|utf8] | node scripts/parse-hier.mjs /dev/stdin
import { readFileSync, writeFileSync } from 'node:fs'

const p = process.argv[2]
const enc = process.argv[3] || 'gbk'
if (!p) { console.error('用法: node scripts/decode-hier.mjs <file> [gbk|utf8]'); process.exit(2) }

const buf = readFileSync(p)
const text = new TextDecoder(enc).decode(buf)
// 自证：文件里应能认出这些已知文案，否则说明编码猜错了。
const probes = ['登录', '用户名', 'OpenCode Pocket', 'WebView']
const hit = probes.filter((s) => text.includes(s))
writeFileSync(p.replace(/\.json$/, '.decoded.json'), text, 'utf8')
console.error(`[${enc}] 识别到 ${hit.length}/${probes.length} 个探针: ${hit.join(' / ') || '<无>'}`)
console.error(`已写出 ${p.replace(/\.json$/, '.decoded.json')}`)
