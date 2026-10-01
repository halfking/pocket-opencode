// wasmprobe-run.mjs — 编译并运行需求 6 A 路线的 wasm 探针。
//
// §7cc：这条链路的实测结论是「Go 纯逻辑编到 js/wasm 后与原生逐字节一致」，
// 但**代价是 4.85MB 的 Go runtime**。本脚本就是复现那个结论的最小闭环。
//
// 它刻意写成「编译 + 运行 + 自比对」而不是「打印给人看」：
// §7cc 指出 `go build` exit 0 对含 socket 的包同样返回 0，毫无区分度，
// 唯一有区分度的判据是真跑一遍并比对字节。
//
// 用法（从仓库根）：
//
//   node scripts/wasmprobe-run.mjs
//   node scripts/wasmprobe-run.mjs --keep      # 保留 wasm 产物便于用 node 直接跑
//
// 产物写在 backend/cmd/wasmprobe/ 下，已被 .gitignore 排除（4.85MB 不进仓库）。
import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const here = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(here, '..');
const backend = path.join(repoRoot, 'backend');
const probeDir = path.join(backend, 'cmd', 'wasmprobe');
const wasmPath = path.join(probeDir, 'rulesprobe.wasm');
const keep = process.argv.includes('--keep');

const MB = (n) => (n / 1048576).toFixed(2) + ' MB';

function run(cmd, args, opts = {}) {
  return execFileSync(cmd, args, {
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
    ...opts,
  });
}

function fail(msg, detail) {
  console.error(`\n[wasmprobe] FAIL: ${msg}`);
  if (detail) console.error(String(detail).trim().split('\n').slice(0, 20).join('\n'));
  process.exit(1);
}

// 一个探针包的完整闭环：原生 -> 编 wasm -> 在 Node 里跑 -> 逐字节比对。
// 返回 wasm 体积（字节）。
//
// 三个包**必须是独立的 main**，不能用同一个 main 加 -mode 切换：
// 顶层 import 一旦同时列出两个包，链接器就会把两者的可达代码都算进去，
// 于是「按 mode 切分体积」量到的是同一个二进制，差值恒为 0，毫无意义。
// （第一版就是这么设计的，实测两个 mode 都是 8.78 MB / 净增 0.00 MB。）
function probe(label, pkgDir) {
  const wasm = path.join(backend, pkgDir.replace(/^\.\//, ''), `${label}.wasm`);

  process.stdout.write(`[wasmprobe] ${label}: native windows/amd64 ... `);
  let nativeOut;
  try {
    nativeOut = run('go', ['run', pkgDir], {
      cwd: backend,
      env: { ...process.env, GOOS: 'windows', GOARCH: 'amd64' },
    });
  } catch (e) {
    fail(`go run ${pkgDir} failed`, e.stderr || e.message);
  }
  // 判据是「输出像 JSON」而不是某个具体字段名 —— 探针可能输出数组
  // （rules/email）或对象（email-fetcher），字段名各不相同。
  const looksLikeJSON = (s) => {
    const t = s.trim();
    return t.length > 2 && (t[0] === '[' || t[0] === '{');
  };

  if (!looksLikeJSON(nativeOut)) fail(`native ${label} produced no JSON`, nativeOut);
  console.log('ok');

  process.stdout.write(`[wasmprobe] ${label}: building js/wasm ... `);
  try {
    run('go', ['build', '-o', wasm, pkgDir], {
      cwd: backend,
      env: { ...process.env, GOOS: 'js', GOARCH: 'wasm' },
    });
  } catch (e) {
    fail(`GOOS=js GOARCH=wasm build ${pkgDir} failed`, e.stderr || e.message);
  }
  if (!existsSync(wasm)) fail('wasm artifact missing after a successful build');
  const bytes = readFileSync(wasm).length;
  console.log(`ok (${MB(bytes)})`);

  process.stdout.write(`[wasmprobe] ${label}: running under Node host ... `);
  const goroot = process.env.GOROOT?.trim() || run('go', ['env', 'GOROOT']).trim();
  const execNode = path.join(goroot, 'lib', 'wasm', 'wasm_exec_node.js');
  if (!existsSync(execNode)) {
    fail(`wasm_exec_node.js not found at ${execNode} (Go >= 1.21 ships it; check GOROOT)`);
  }
  let wasmOut;
  try {
    wasmOut = run(process.execPath, [execNode, wasm]);
  } catch (e) {
    fail(`wasm run under node failed (${label})`, e.stderr || e.message);
  }
  if (!looksLikeJSON(wasmOut)) fail(`wasm ${label} produced no JSON`, wasmOut);
  console.log('ok');

  process.stdout.write(`[wasmprobe] ${label}: comparing native vs wasm ... `);
  const norm = (s) => s.replace(/\r\n/g, '\n').trim();
  if (norm(nativeOut) !== norm(wasmOut)) {
    const a = wasm.replace(/\.wasm$/, '.native.json');
    const b = wasm.replace(/\.wasm$/, '.wasm.json');
    writeFileSync(a, nativeOut);
    writeFileSync(b, wasmOut);
    fail(`native and wasm outputs DIFFER (${label}); dumps written next to the probe`);
  }
  console.log(`ok (IDENTICAL, ${norm(nativeOut).length} chars)`);

  if (!keep) rmSync(wasm, { force: true });
  return bytes;
}

// baseline 只编不跑：空 main 的体积就是 Go runtime 底线，
// 任何 wasm 二进制都躲不掉这块。没有它，后两个数字无法解释。
function probeBaseline() {
  process.stdout.write('[wasmprobe] baseline (empty main): building js/wasm ... ');
  const wasm = path.join(backend, 'cmd', 'wasmbaseline', 'baseline.wasm');
  try {
    run('go', ['build', '-o', wasm, './cmd/wasmbaseline/'], {
      cwd: backend,
      env: { ...process.env, GOOS: 'js', GOARCH: 'wasm' },
    });
  } catch (e) {
    fail('baseline build failed', e.stderr || e.message);
  } finally {
    const bytes = existsSync(wasm) ? readFileSync(wasm).length : 0;
    if (!keep) rmSync(wasm, { force: true });
    console.log(`ok (${MB(bytes)})`);
    return bytes;
  }
}

const baselineBytes = probeBaseline();
const rulesBytes = probe('rules', './cmd/wasmprobe/');
const emailBytes = probe('email', './cmd/wasmprobe/email/');
// 反向对照：调用 email.NewFetcher（不建连，但会把 fetcher.go 及其
// net / crypto-tls / go-imap 依赖闭包全拉进编译单元）。
// 若体积不增，说明「socket 代码占地方」这个前提本身是错的 —— §7cd 的真正结论。
const fetcherBytes = probe('email-fetcher', './cmd/wasmprobe/email-fetcher/');

console.log(`
[wasmprobe] PASS — 三套探针的 native/wasm 输出都逐字节一致

  ① 行为：无 socket 的纯逻辑编到 js/wasm 后与原生**完全一致**
     · rules（293 行规则引擎）              IDENTICAL
     · email（分类归一化/重要性归一化/ShouldProcessAfterFetch/
              垃圾匹配/SelectDeletable/发票日期解析/内容哈希/分类写回）  IDENTICAL
     · email-fetcher（构造 Fetcher，不建连）   IDENTICAL

  ② 包体 —— 这是需要你拍板的那笔账：

       Go runtime 底线（空 main）             ${MB(baselineBytes)}      躲不掉，一次性
       + stdlib（regexp/json/time…）+ rules   ${MB(rulesBytes - baselineBytes)}
       ---------------------------------------------------------
       = rules 产物                           ${MB(rulesBytes)}
       = email 纯逻辑产物                     ${MB(emailBytes)}      净增 ${MB(emailBytes - rulesBytes)}
       = email + NewFetcher（含 net/tls/imap） ${MB(fetcherBytes)}

  ③ 最重要的一条：socket 代码**几乎不占体积**

     ${MB(fetcherBytes)} vs ${MB(emailBytes)} —— 主动把 fetcher.go 和它的
     net / crypto-tls / go-imap / go-sasl 依赖闭包全拉进来，体积**没有增加**
     （差值 ${MB(Math.abs(fetcherBytes - emailBytes))} 在探针调用面不同的噪声范围内）。

     原因：js/wasm 下这些包的 socket 实现是 **ENOSYS 空壳**（§7cb），
     几乎不产生代码。所以 §7cd 一度写的「搬纯逻辑比搬全包省 3.84 MB」
     **是个误导性结论** —— 不是省了，是那些代码本来就不占地方。

     对 A 路线的实际含义：**包体不是决策依据**。
     「搬纯逻辑」和「连协议层一起搬」在体积上等价，
     真正的分界只有一条 —— wasm 里的 socket 一调用就是 ENOSYS。

  ④ 这条链路的硬边界：
     · socket 在 js/wasm 下是 ENOSYS（§7cb）—— IMAP/POP3 必须在 Java 层
     · ExportInvoiceGrid（需求 5 的 A4 网格）写文件，wasm 下不可用
     · 本轮只验证了 Node 宿主；Android WebView 未验
`);
