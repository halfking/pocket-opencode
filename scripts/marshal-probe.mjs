// marshal-probe.mjs — 测 wasm 堆 ↔ 宿主数据结构的边界成本（§7ce）。
//
// 四条路径跑**同一个** classifyAll（email 包的真实函数）：
//
//   native        Go 原生，无边界               基线
//   wasm-bulk     一个 JSON 字符串过边界        一次跨界
//   wasm-struct   逐字段 syscall/js 访问         N×M 次跨界
//   wasm-permail  每封一次 JSON 往返             2N 次跨界  ← A 路线的真实模型
//
// ## 判据：不用减法，直接读 wasm 内部的分段计时
//
// 第一版用「wasm 总耗时 − native 总耗时」算边界成本，量出 `elapsed_ns: 0` ——
// 因为 classifyAll 只做字符串归一化，120 封装不满 1ms，计时器分辨率下就是 0。
// **一旦计算耗时落到分辨率以下，减法变成「0 − 0」，边界成本被彻底抹掉。**
//
// 现在 read / calc / write 三段在 wasm 内部各自用 time.Now() 直接测，
// 边界成本是**绝对值**而不是差值。native 只用来做「结果一致」的校验。
//
// ## 为什么 permail 才是 A 路线的真实模型
//
// AI 分类的 HTTP **不能**在 wasm 里发（js/wasm 没有 fetch，net 也不可用），
// 必须由 JS 侧发起。于是真实架构是
//   JS(sql.js 取行) → wasm(判是否要分类) → JS(fetch LLM) → wasm(解析结果)
// 每封邮件**至少两次跨界**。bulk 形态假设数据一次性进出，那个假设在这里破了；
// 拿 bulk 的数字论证 A 路线会系统性低估边界成本。
import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync, rmSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const here = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(here, '..');
const backend = path.join(repoRoot, 'backend');
const driverPath = path.join(here, 'marshal-driver.mjs');
const PKG = './cmd/wasmprobe/marshal/';
const ROUNDS = 7; // 取最小值：边界抖动的噪声远大于中位数

// `input` 只能喂给 `stdio[0] === 'pipe'` 的子进程。写死 `['ignore',...]`
// 会把 stdin 直接丢掉，driver 读到空串后报 "Unexpected end of JSON input" ——
// 而这个报错发生在数据到达之前，看起来像 driver 的 bug。
const run = (cmd, args, opts = {}) =>
  execFileSync(cmd, args, {
    encoding: 'utf8',
    stdio: [opts.input !== undefined ? 'pipe' : 'ignore', 'pipe', 'pipe'],
    ...opts,
  });

const ms = (ns) => (ns / 1e6).toFixed(3);
const pct = (x) => (x * 100).toFixed(1) + '%';

// ---- 1) native：只取「结果摘要」用于三方一致性校验 ----
const nativeRun = JSON.parse(run('go', ['run', PKG], {
  cwd: backend, env: { ...process.env, GOOS: 'windows', GOARCH: 'amd64' },
}));
const nativeDigest = JSON.stringify(nativeRun.result_digest);
const COUNT = nativeRun.count;
const inputBytes = nativeRun.input_bytes;

// ---- 2) 语料：必须由 native 侧用同一段 Go 代码生成，避免两份语料漂移 ----
const corpusJSON = run('go', ['run', PKG, '-emit-corpus'], {
  cwd: backend, env: { ...process.env, GOOS: 'windows', GOARCH: 'amd64' },
});
const corpusArr = JSON.parse(corpusJSON);
const perMail = corpusArr.map((r) => JSON.stringify(r));

// ---- 3) 编 wasm ----
const wasmPath = path.join(backend, 'cmd', 'wasmprobe', 'marshal', 'marshal.wasm');
run('go', ['build', '-o', wasmPath, PKG], {
  cwd: backend, env: { ...process.env, GOOS: 'js', GOARCH: 'wasm' },
});
const wasmBytes = readFileSync(wasmPath).length;

const goroot = process.env.GOROOT?.trim() || run('go', ['env', 'GOROOT']).trim();
const execNode = path.join(goroot, 'lib', 'wasm', 'wasm_exec_node.js');
if (!existsSync(execNode)) {
  console.error(`wasm_exec_node.js not found at ${execNode}`);
  process.exit(1);
}

// Go/wasm 的 main 是一次性的：每个形态必须起独立子进程重新实例化。
// 传 GOROOT 而不是 wasm_exec_node.js 路径 —— driver 只 require
// wasm_exec.js（纯定义），见 marshal-driver.mjs 里的说明。
//
// 失败时必须带上 mode：2026-10-02 那次 permail 静默失败，裸
// `JSON.parse('')` 报的 "Unexpected end of JSON input" 完全看不出是哪个形态，
// 而 bulk/struct 都是好的 —— 少了这个信息就只能靠逐个手试。
function once(mode, payload) {
  const out = run(process.execPath, [driverPath, goroot, wasmPath, mode], {
    input: JSON.stringify(payload), maxBuffer: 64 * 1024 * 1024,
  });
  let parsed;
  try {
    parsed = JSON.parse(out);
  } catch (err) {
    throw new Error(`[marshal] mode=${mode}: driver stdout is not JSON (${out.length}B): ${err.message}`);
  }
  return parsed;
}

const best = {};
for (const [mode, payload] of [
  ['bulk', { input: corpusJSON }],
  ['struct', { rows: corpusArr }],
  ['permail', { permail: perMail }],
]) {
  let b = null;
  for (let i = 0; i < ROUNDS; i++) {
    const r = once(mode, payload);
    // 条数必须等于语料条数。这条断言是**必需**的，不是锦上添花：
    // struct 形态曾经用 `new Array(rows)` 把 rows 包成长度 1 的新数组，
    // 于是 count 恒为 1、算的是 1 封邮件的耗时 —— 而它算出来的 result_digest
    // 同样只含 1 条，于是下面那套「四条路径结果一致」的校验**照样通过**。
    // 一致性校验证明的是「各路径算法相同」，不是「处理了正确数量的数据」。
    if (r.count !== COUNT) {
      throw new Error(`[marshal] mode=${mode}: count=${r.count}, want ${COUNT} —— 该形态实际处理的数据量不对，耗时不可比`);
    }
    if (!b || r.elapsed_ns < b.elapsed_ns) b = r;
  }
  best[mode] = b;
}
rmSync(wasmPath, { force: true });

// ---- 4) 判据：结果必须三方一致，否则耗时对比毫无意义 ----
const digests = {
  native: nativeDigest,
  bulk: best.bulk.result_digest,
  struct: best.struct.result_digest,
  permail: best.permail.result_digest,
};
const ref = digests.native;
const mismatched = Object.entries(digests).filter(([k, v]) => v !== ref);
if (mismatched.length) {
  console.error('\n[marshal] FAIL: 结果摘要不一致，耗时对比没有意义');
  for (const [k, v] of Object.entries(digests)) console.error(`  ${k}: ${v}`);
  process.exit(1);
}

const rows = (label, r, perMailUnit) => {
  const read = r.read_ns, calc = r.calc_ns, write = r.write_ns;
  const boundary = read + write;
  const total = r.elapsed_ns;
  console.log(
    `  ${label.padEnd(13)} 读 ${ms(read).padStart(8)}  算 ${ms(calc).padStart(8)}  写 ${ms(write).padStart(8)}` +
    `   合计 ${ms(total).padStart(8)} ms   边界占比 ${pct(boundary / total).padStart(7)}` +
    (perMailUnit ? `   每封边界 ${(boundary / COUNT / 1e6).toFixed(4)} ms` : '')
  );
};

console.log(`
[marshal] ${COUNT} 封（对齐生产实测行数）/ ${ROUNDS} 轮取最小值
         输入 ${(inputBytes / 1024).toFixed(1)} KB / wasm ${(wasmBytes / 1048576).toFixed(2)} MB
         native 计算基线 ${ms(nativeRun.elapsed_ns)} ms（低于计时分辨率，**不能用来做减法**）

  ${'形态'.padEnd(13)} ${'读(JS→wasm)'.padStart(8)}  ${'算(纯计算)'.padStart(8)}  ${'写(wasm→JS)'.padStart(8)}   ${'合计'.padStart(9)}   边界占比
  ${'-'.repeat(96)}`);
rows('wasm-bulk', best.bulk);
rows('wasm-struct', best.struct);
rows('wasm-permail', best.permail, true);
console.log(`
  四条路径（native + 三形态）结果摘要完全一致：yes
`);
