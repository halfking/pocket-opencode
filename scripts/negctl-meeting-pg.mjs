// scripts/negctl-meeting-pg.mjs
// 负控：临时打断 meeting PG 的写入路径，验证判据真的会红。
//
// 用法：node scripts/negctl-meeting-pg.mjs on|off
//   on  —— 把 CreateScoped 的 INSERT 换成 no-op（模拟"创建返回 201 但没落库"）
//   off —— 原样还原
//
// 为什么要做：测试全绿不能证明判据有效。如果这套判据在"数据压根没进库"
// 的情况下也会绿，那它对 BUG-AW 就是一个假阳性守卫。
import fs from 'node:fs';

const p = 'C:/workspace/openpocket/wt3/backend/internal/meeting/pg_store.go';
const backup = p + '.negctl.bak';

const GOOD = `\t\tm, err := scanMeeting(row)\r
\t\tif err == nil {\r
\t\t\treturn m, nil\r
\t\t}`;

const BROKEN = `\t\t_ = row // NEGCTL: INSERT 被短路，模拟"创建返回 201 但没落库"\r
\t\tif true {\r
\t\t\treturn &Meeting{ID: nextMeetingID(), OwnerID: ownerID, WorkspaceID: workspaceID, Title: req.Title, Status: "recording", CreatedAt: time.Now(), UpdatedAt: time.Now()}, nil\r
\t\t}\r
\t\tm, err := scanMeeting(row)\r
\t\tif err == nil {\r
\t\t\treturn m, nil\r
\t\t}`;

const mode = process.argv[2];
const s = fs.readFileSync(p, 'utf8');

if (mode === 'on') {
  if (s.includes('NEGCTL')) {
    console.log('already broken (idempotent)');
    process.exit(0);
  }
  if (!s.includes(GOOD)) throw new Error('anchor not found; is pg_store.go already modified?');
  fs.writeFileSync(backup, Buffer.from(s, 'utf8'));
  fs.writeFileSync(p, Buffer.from(s.replace(GOOD, BROKEN), 'utf8'));
  console.log('NEGCTL ON: CreateScoped 不再落库');
} else if (mode === 'off') {
  if (!fs.existsSync(backup)) {
    console.log('no backup found; nothing to restore');
    process.exit(0);
  }
  fs.writeFileSync(p, fs.readFileSync(backup));
  fs.unlinkSync(backup);
  console.log('NEGCTL OFF: 已从备份还原');
} else {
  throw new Error('usage: node scripts/negctl-meeting-pg.mjs on|off');
}
