// 通用版冲突检测：主工作区（并发会话正在写的那份）与 origin/main 的路径重叠面。
// 与 check-main-worktree-conflict.mjs 的区别：那个是针对 BUG-AA 固定清单的断言，
// 这个是**通用**的 —— 列出「待快进提交会碰到的文件」∩「主工作区已脏的文件」，
// 用来回答「这轮推的东西会不会被并发会话覆盖掉」。
import { execFileSync } from 'node:child_process';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

// 仓库根从脚本自身位置推导，不写死某台机器的绝对路径。
// 原来这里是 'C:/workspace/openpocket'，在别的 checkout（Linux 宿主、CI、
// 另一个 worktree）上直接 `fatal: cannot change to ...` 崩掉——一个用来
// 「回答我这轮会不会被并发会话覆盖」的工具，在最需要它的时候反而没有输出。
const ROOT = resolve(fileURLToPath(new URL('..', import.meta.url)));
const g = (cmd) => execFileSync('git', ['-C', ROOT, ...cmd.split(' ')], { encoding: 'utf8', maxBuffer: 1 << 28 });

const behind = g('rev-list --count main..origin/main').trim();
const ahead = g('rev-list --count origin/main..main').trim();
const incoming = g('diff --name-only main..origin/main').split('\n').filter(Boolean);
const dirty = g('status --porcelain').split('\n').filter(Boolean).map((l) => l.slice(3).trim().replace(/^"|"$/g, ''));

const overlap = incoming.filter((f) => dirty.includes(f));

console.log(`主工作区落后 origin/main ${behind} 个提交${ahead !== '0' ? `（本地领先 ${ahead}）` : ''}`);
console.log(`主工作区脏文件 ${dirty.length} 个，待快进提交涉及 ${incoming.length} 个文件`);
console.log(`\n重叠（会挡住快进 / 有被覆盖风险）${overlap.length} 个：`);
overlap.forEach((f) => console.log('  ' + f));
if (!overlap.length) console.log('  （无）');

// 本轮我改过的、且并发会话可能也在改的产品文件
const MINE = [
  'backend/internal/marketplace/marketplace.go',
  'frontend/src/features/study/StudyHubView.vue',
  'frontend/src/features/email/EmailAccountAddView.vue',
  'frontend/src/locales/ja-JP.json',
  'frontend/src/locales/ko-KR.json',
  'frontend/src/locales/de-DE.json',
  'frontend/src/locales/fr-FR.json',
  'frontend/src/locales/es-ES.json',
  'frontend/src/locales/pt-BR.json',
  'frontend/src/locales/zh-TW.json',
];
const risky = MINE.filter((f) => dirty.includes(f));
console.log(`\n本轮已推送的修复中，主工作区仍是旧版的：${risky.length} 项`);
risky.forEach((f) => console.log('  ' + f));
if (risky.length) {
  console.log('\n⚠️  这些若被并发会话先提交，会把修复 revert 上去。');
  console.log('    git merge --ff-only 会被拒绝（好事）；不要用 -f 绕过。');
}
