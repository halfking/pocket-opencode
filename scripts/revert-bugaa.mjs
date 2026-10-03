// 一次性：在「修复后 / 修复前」之间切换 StudyHubView，用于**证伪**真机验证脚本。
// 判据必须能区分通/不通 —— 回退后 verify-bugaa-realdevice.mjs 必须失败。
//
// 双向 + 状态校验 + 显式方向，理由同 revert-bugz.mjs：
// 就地改文件却没有还原路径的脚本，误跑一次就会把「已回退的代码」留在工作区。
// 换行符用 \r?\n 兼容，工作区是 CRLF（core.autocrlf=true）。
import { execSync } from 'node:child_process';
import fs from 'node:fs';

const P = 'frontend/src/features/study/StudyHubView.vue';
const mode = process.argv[2];
if (mode !== 'on' && mode !== 'off') {
  console.error('用法: node scripts/revert-bugaa.mjs on|off');
  process.exit(2);
}

const FIXED_MARK = 'data-testid="study-deck-create-submit"';
const BUGGY_MARK = 'goCreateDeck';

let s = fs.readFileSync(P, 'utf8');
const isFixed = s.includes(FIXED_MARK) && !s.includes(BUGGY_MARK);
const isBuggy = s.includes(BUGGY_MARK) && !s.includes(FIXED_MARK);

if (mode === 'on') {
  if (!isFixed) { console.error(`FATAL: 期望处于修复态，实际 isFixed=${isFixed} isBuggy=${isBuggy}，拒绝盲替换`); process.exit(3); }
  // 直接从父提交取修复前版本
  s = execSync('git show c34bbd6~1:' + P, { encoding: 'utf8', maxBuffer: 1 << 28 });
  if (!s.includes(BUGGY_MARK)) { console.error('FATAL: 基线里没有 goCreateDeck，基线不对'); process.exit(3); }
} else {
  if (!isBuggy) { console.error(`FATAL: 期望处于修复前态，实际 isFixed=${isFixed} isBuggy=${isBuggy}，拒绝盲替换`); process.exit(3); }
  s = execSync('git show c34bbd6:' + P, { encoding: 'utf8', maxBuffer: 1 << 28 });
  if (!s.includes(FIXED_MARK)) { console.error('FATAL: 基线里没有修复钩子，基线不对'); process.exit(3); }
}

// 写盘前断言：状态必须确实翻转，且另一侧标记必须消失
const nowFixed = s.includes(FIXED_MARK) && !s.includes(BUGGY_MARK);
const nowBuggy = s.includes(BUGGY_MARK) && !s.includes(FIXED_MARK);
if (mode === 'on' && !nowBuggy) { console.error('FATAL: 未能翻转到修复前态，未写盘'); process.exit(3); }
if (mode === 'off' && !nowFixed) { console.error('FATAL: 未能翻转到修复态，未写盘'); process.exit(3); }
if (/[\uFFFD\uFEFF]/.test(s)) { console.error('FATAL: 内容含 U+FFFD/U+FEFF，未写盘'); process.exit(3); }

fs.writeFileSync(P, s, 'utf8');
console.log(`${mode === 'on' ? '已回退到修复前' : '已恢复修复'}（${mode === 'on' ? 'BUG-AA 复现版' : 'BUG-AA 修复版'}）`);
try {
  console.log(`该文件 git diff：${execSync(`git diff --numstat -- ${P}`, { encoding: 'utf8' }).trim() || '(无)'}`);
  if (mode === 'on') console.log('⚠️  必须跑完证伪后执行：node scripts/revert-bugaa.mjs off');
} catch (e) { /* 忽略 */ }
