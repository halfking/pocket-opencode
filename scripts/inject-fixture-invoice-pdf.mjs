// 把一张真格式发票 PDF 的 base64 注入 IMAP 夹具（幂等：已注入则跳过）。
// 手工贴 2KB base64 太容易出错，交给脚本。
import { readFileSync, writeFileSync, existsSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const fixture = join(here, 'imap-fixture-mails.mjs')
const b64File = process.argv[2] || join(process.env.TEMP || '', 'fixture-invoice.b64')
if (!existsSync(b64File)) {
  console.error('missing base64 file:', b64File)
  process.exit(1)
}
const src = readFileSync(fixture, 'utf8')
if (src.includes('validInvoicePdfB64')) {
  console.log('already injected, nothing to do')
  process.exit(0)
}
const b64 = readFileSync(b64File, 'utf8').trimEnd()
const block = [
  '',
  '// 一张**真格式**的发票 PDF（base64 由 backend/internal/email/gen_fixture_invoice_test.go 生成）。',
  '// 之前这里用的是退化 PDF（只有 Catalog、无页树）：采集器能落盘，但 A4 网格导出会被',
  '// pdfcpu 拒绝，导致「发票导出」这条需求在夹具环境里永远拿不到真实产物。',
  'const validInvoicePdfB64 =',
  b64,
  "  ''",
  '',
].join('\n')
const anchor = src.indexOf('export function buildMails')
if (anchor < 0) { console.error('buildMails anchor not found'); process.exit(1) }
writeFileSync(fixture, src.slice(0, anchor) + block + src.slice(anchor), 'utf8')
console.log('injected validInvoicePdfB64, file now', readFileSync(fixture, 'utf8').length, 'bytes')
