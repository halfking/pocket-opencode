// 卡口：运行时数据与密钥不允许被 git 跟踪。
//
// 为什么需要它：.gitignore 里那段注释早就写明了意图——「data/ 下装的是每个
// 用户自己的内容，不是源码，所以整个目录忽略，而不是只忽略主密钥」。
// 但实现是**逐个列出已知子目录**（data/email-bodies/、data/email-invoices/…），
// 于是每多一个子目录就再漏一次。2026-10-03 实测：data/email-bodies-raw/
// （POP3 原始 MIME 落盘）没被列到，`git add -A` 的 dry-run 里有 45 个真实
// 邮件正文会被加进暂存区。email_master.key 那条规则当时仍然拦得住，所以密钥
// 没泄露，但正文泄露这条路是通的。
//
// 这个脚本查的是**「有没有被跟踪」**，不是「.gitignore 怎么写」。
// 两者的区别很实际：.gitignore 只挡住 `git add -A`，挡不住
// `git add -f`、挡不住别人改了规则、也挡不住历史上已经入库的文件。
// 跟踪状态是事实，忽略规则只是意图。
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

// ROOT 从脚本自身位置推导。**不要**硬编码绝对路径：仓库里有过判据一直在判
// 另一棵源码树的前科（6e89480a「闪卡判据一直在判另一棵源码树（硬编码 wt3 路径）」），
// 硬编码的卡口在别的 worktree 里会静默给出与本仓库无关的绿灯。
const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..');

// 这些前缀下的东西是运行时数据：sqlite、正文、发票、密钥。
const DATA_PREFIXES = ['data/', 'backend/data/'];
// 与路径无关的硬命中：密钥文件名。放在任何目录都是泄露。
const SECRET_BASENAMES = new Set(['email_master.key']);

function isRuntimeData(p) {
  const norm = p.replace(/\\/g, '/');
  if (SECRET_BASENAMES.has(norm.split('/').pop())) return true;
  return DATA_PREFIXES.some((pre) => norm.startsWith(pre));
}

function selftest() {
  const cases = [
    // [路径, 期望被判定为「是运行时数据」]
    ['data/email-bodies/em-1.bin', true],
    ['data/email-bodies-raw/em-pop3-acct-1.bin', true],
    ['data/chat_agents.sqlite', true],
    ['data/email_master.key', true],
    ['backend/data/email_master.key', true],
    ['data/anything-not-yet-invented/deep/nested.bin', true],
    // 反向：这些必须判为「否」，否则规则过宽、天天误报、最后没人看
    ['backend/internal/email/pipeline.go', false],
    ['frontend/src/api/email.ts', false],
    ['scripts/check-runtime-data-tracked.mjs', false],
    ['docs/handoff/2026-10-03-round26.md', false],
    ['backend/internal/database/schema.sql', false],
    ['docs/adr/metadata.md', false],
  ];
  let bad = 0;
  for (const [p, want] of cases) {
    const got = isRuntimeData(p);
    if (got !== want) {
      console.error(`  ✗ ${p} 期望=${want} 实际=${got}`);
      bad++;
    }
  }
  console.log(`  selftest: ${cases.length - bad}/${cases.length} 通过`);
  if (bad) {
    console.error('selftest 失败 ⇒ 判据本身写错了，不要相信它的绿灯。');
    process.exit(1);
  }
  return true;
}

if (process.argv.includes('--selftest')) {
  console.log('selftest：判据本身的判别力（不碰 git）');
  selftest();
  process.exit(0);
}

const out = execFileSync('git', ['-C', ROOT, 'ls-files', '-z', '--', 'data', 'backend/data'], {
  encoding: 'buffer',
  maxBuffer: 1 << 28,
});
const tracked = out.toString('utf8').split('\0').filter(Boolean);

if (!tracked.length) {
  console.log('✓ data/ 与 backend/data/ 下没有任何被跟踪的文件');
  process.exit(0);
}

console.error(`✗ 有 ${tracked.length} 个运行时数据文件被 git 跟踪：`);
for (const p of tracked) console.error(`    ${p}`);
console.error('');
console.error('这些是运行时数据（邮件正文 / 发票 / sqlite / 密钥），不是源码。');
console.error('处理：git rm --cached <路径> 把它移出索引，并确认 .gitignore 覆盖了它。');
console.error('注意 `git rm --cached` 不会删磁盘上的文件。若含密钥，还要轮换该密钥。');
process.exit(1);
