// append-handoff-aw.mjs — 把 docs/handoff/_part-4.77.md 追加到主 handoff 末尾。
//
// 保持 CRLF、无 BOM（主文档约定）。幂等：已追加过就拒绝二次追加。
import fs from 'node:fs';

const DOC = 'C:/workspace/openpocket/wt3/docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md';
const PART = 'C:/workspace/openpocket/wt3/docs/handoff/_part-4.77.md';
const MARK = '§4.77 BUG-AW：会议数据零持久化';

let doc = fs.readFileSync(DOC, 'utf8');
if (doc.includes(MARK)) {
  console.log('已包含 §4.77，跳过（幂等）');
  process.exit(0);
}

let part = fs.readFileSync(PART, 'utf8');
if (doc.includes(part.trim().slice(0, 200))) {
  console.log('片段正文已存在，跳过');
  process.exit(0);
}

// 统一成 CRLF 再拼
part = part.replace(/\r?\n/g, '\r\n');
if (!doc.endsWith('\r\n')) doc += '\r\n';
const out = doc + part.replace(/^\r\n/, '');

const buf = Buffer.from(out, 'utf8');
fs.writeFileSync(DOC, buf);

const back = buf.toString('utf8');
console.log(`已追加 §4.77`);
console.log(`  bytes=${buf.length}`);
console.log(`  CRLF=${(back.match(/\r\n/g) || []).length} bareLF=${(back.match(/(?<!\r)\n/g) || []).length} BOM=${buf[0] === 0xef}`);
console.log(`  末行: ${JSON.stringify(back.slice(-60))}`);
