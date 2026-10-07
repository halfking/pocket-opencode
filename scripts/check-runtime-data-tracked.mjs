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

// ★ 参数化版本：承重检查要能「拿掉某一条规则再判一遍」，否则只能靠人肉数
//   哪几条用例属于哪条规则 —— 而人肉数数出来的正是 §206 形状四那个错。
function isRuntimeDataWith(p, prefixes, secrets) {
  const norm = p.replace(/\\/g, '/');
  if (secrets.has(norm.split('/').pop())) return true;
  return prefixes.some((pre) => norm.startsWith(pre));
}
function isRuntimeData(p) {
  return isRuntimeDataWith(p, DATA_PREFIXES, SECRET_BASENAMES);
}

function selftest() {
  const cases = [
    // [路径, 期望被判定为「是运行时数据」]
    ['data/email-bodies/em-1.bin', true],
    ['data/email-bodies-raw/em-pop3-acct-1.bin', true],
    ['data/chat_agents.sqlite', true],
    // ⚠ 这两条原来写的是 `data/email_master.key` 与 `backend/data/email_master.key`，
    //   而它们**同时**被前缀规则与 SECRET_BASENAMES 判绿 ⇒ 把任一条规则拿掉它们都
    //   还是绿的，对**哪条规则都没约束力**（docs/design §206 形状四）。
    //   改成不含密钥名的路径后，它们各自只被前缀规则判绿，约束力才落到前缀上。
    ['data/voiceprint.db', true],
    ['backend/data/meeting.sqlite', true],
    // ★ 这两条是**与路径无关**的那一半，正是它们才需要 SECRET_BASENAMES。
    //   原来一个都没有 ⇒ 实测 M21（把 SECRET_BASENAMES 整条删掉）自检仍 12/12 全绿。
    ['deploy/email_master.key', true],
    ['config/secrets/email_master.key', true],
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
  // ★ 条数下限闸（形状照抄 check-smart-quotes.mjs / check-pg-schema-scope.mjs /
  //   check-hide-app-header.mjs，docs/design §103 与 §154 记的是同一个假绿）。
  //   没有它：cases 被抽空时打出 `selftest: 0/0 通过`，bad=0 ⇒ 不 exit(1)
  //   ⇒ return true ⇒ 调用方 `process.exit(0)` —— 与真通过**完全同形**。
  //   分子分母印的是同一个数，它压根不是「通过数 / 声明数」的比值。
  //   ★ 必须排在下面那行打印**之前**：否则 `0/0 通过` 已经进了日志，
  //     退出码补上也救不回被污染的读数。
  //   阈值 11 = 当前 14 条的约八成，沿用同族约定，不由审计者拍板。
  //   **只抓「用例被成批删空 / 腰斩」，不抓「少了一条」「某条被改成恒真」。**
  //   下限只能手工改这个常量，不接受命令行参数。
  const MIN_SELFTEST_CASES = 11;
  if (cases.length < MIN_SELFTEST_CASES) {
    console.error(`  ✗ selftest 只跑了 ${cases.length}/${MIN_SELFTEST_CASES} 例 —— cases 数组被改过。`);
    console.error('    「0/0 通过」不是通过：守卫空转时的读数和它要抓的病一模一样。');
    process.exit(2);
  }
  // ★★★ 承重组保护（docs/design §206 形状四 + 形状五「补了用例没补保护」）。
  //   「每条规则都有**专属**用例」这件事怎么量？不靠人肉数 ——
  //   直接把那条规则拿掉，看有哪几条用例会翻（want 与 judge 结果不再相等）。
  //   ⚠ 这条保护在 §206 登记时**没做**，只补了用例；实测形态就是
  //   「用例看起来齐全、删掉一条规则却仍然全绿」。
  const LOAD_BEARING = [
    ['SECRET_BASENAMES', (p) => isRuntimeDataWith(p, DATA_PREFIXES, new Set()), 2],
    ["DATA_PREFIXES 'data/'", (p) => isRuntimeDataWith(p, ['backend/data/'], SECRET_BASENAMES), 2],
    ["DATA_PREFIXES 'backend/data/'", (p) => isRuntimeDataWith(p, ['data/'], SECRET_BASENAMES), 1],
  ]
  for (const [name, judge, min] of LOAD_BEARING) {
    const flipped = cases.filter(([p, want]) => judge(p) !== want).length;
    if (flipped < min) {
      console.error(`  ✗ 规则 ${name} 只有 ${flipped} 条用例因它而翻（下限 ${min}）—— 这些用例被别的规则顶替了。`);
      bad++;
    } else {
      console.log(`  承重 ${name}: ${flipped} 条用例（下限 ${min}）`);
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

// ★ 原来这里用的是 pathspec `-- data backend/data`，把枚举范围限死在那两个目录。
//   后果：`isRuntimeData` 在**真实路径上从未被调用** —— 它只出现在自检里。
//   于是源码第 26 行写着的「密钥文件名与路径无关，放在任何目录都是泄露」这条承诺，
//   **从来没有生效过**。实测 M21：把 SECRET_BASENAMES 整条删掉，自检仍 12/12 全绿。
//   而这不是纸面风险：POCKET_DATA_DIR 是**任意绝对路径**（backend/internal/config/config.go），
//   密钥写在 <dataDir>/email_master.key ⇒ 一旦配到 data/ 之外，
//   .gitignore 里那两条精确规则与这道门的 pathspec 会**同时看不见它**。
// ⇒ 现在枚举全仓，再用同一个 isRuntimeData 过滤 ⇒ 声明的能力真的被用上。
//   顺带把「扫到没命中」与「压根没扫到」分开了：现在报的是扫过的总数。
const out = execFileSync('git', ['-C', ROOT, 'ls-files', '-z'], {
  encoding: 'buffer',
  maxBuffer: 1 << 28,
});
const tracked = out.toString('utf8').split('\0').filter(Boolean);
const leaks = tracked.filter(isRuntimeData);

if (!leaks.length) {
  console.log(`✓ 扫了 ${tracked.length} 个被跟踪文件：没有运行时数据，也没有密钥文件名`);
  process.exit(0);
}

console.error(`✗ 有 ${leaks.length} 个运行时数据/密钥文件被 git 跟踪：`);
for (const p of leaks) console.error(`    ${p}`);
console.error('');
console.error('这些是运行时数据（邮件正文 / 发票 / sqlite / 密钥），不是源码。');
console.error('处理：git rm --cached <路径> 把它移出索引，并确认 .gitignore 覆盖了它。');
console.error('注意 `git rm --cached` 不会删磁盘上的文件。若含密钥，还要轮换该密钥。');
process.exit(1);
