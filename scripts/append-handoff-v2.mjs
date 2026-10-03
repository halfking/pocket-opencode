// append-handoff-v2.mjs — 把 docs/handoff/_part-4.78.md 追加到主 handoff 末尾。
// 保持 CRLF、无 BOM。幂等。
import fs from 'node:fs';

const DOC = 'C:/workspace/openpocket/wt3/docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md';
const PART = 'C:/workspace/openpocket/wt3/docs/handoff/_part-4.78.md';
const MARK = '§4.78 外部审计四条证据缺口的逐条回应';

let doc = fs.readFileSync(DOC, 'utf8');
if (doc.includes(MARK)) {
  console.log('已包含 §4.78，跳过（幂等）');
  process.exit(0);
}
let part = fs.readFileSync(PART, 'utf8').replace(/\r?\n/g, '\r\n');
if (doc.includes(part.trim().slice(0, 200))) { console.log('片段正文已存在，跳过'); process.exit(0); }

if (!doc.endsWith('\r\n')) doc += '\r\n';
const out = doc + part.replace(/^\r\n/, '');
const buf = Buffer.from(out, 'utf8');
fs.writeFileSync(DOC, buf);
const back = buf.toString('utf8');
console.log(`已追加 §4.78  bytes=${buf.length}  CRLF=${(back.match(/\r\n/g) || []).length}  bareLF=${(back.match(/(?<!\r)\n/g) || []).length}  BOM=${buf[0] === 0xef}`);
