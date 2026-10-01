// append-handoff-v2b.mjs — 追加 §4.78.8 / §4.78.9 收尾片段。CRLF / 无 BOM。幂等。
import fs from 'node:fs';

const DOC = 'C:/workspace/openpocket/wt3/docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md';
const PART = 'C:/workspace/openpocket/wt3/docs/handoff/_part-4.78b.md';
const MARK = '§4.78.8 收尾';

let doc = fs.readFileSync(DOC, 'utf8');
if (doc.includes(MARK)) { console.log('已包含 §4.78.8/9，跳过（幂等）'); process.exit(0); }
let part = fs.readFileSync(PART, 'utf8').replace(/\r?\n/g, '\r\n');
if (!doc.endsWith('\r\n')) doc += '\r\n';
const out = doc + part.replace(/^\r\n/, '');
const buf = Buffer.from(out, 'utf8');
fs.writeFileSync(DOC, buf);
const back = buf.toString('utf8');
console.log(`已追加 §4.78.8/9  bytes=${buf.length}  CRLF=${(back.match(/\r\n/g) || []).length}  bareLF=${(back.match(/(?<!\r)\n/g) || []).length}  BOM=${buf[0] === 0xef}`);
