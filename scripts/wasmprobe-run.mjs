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
const nativeJson = path.join(probeDir, '.rulesprobe-native.json');
const wasmJson = path.join(probeDir, '.rulesprobe-wasm.json');
const keep = process.argv.includes('--keep');

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

// 1) 原生基线
process.stdout.write('[wasmprobe] 1/4 native windows/amd64 baseline ... ');
let nativeOut;
try {
  nativeOut = run('go', ['run', './cmd/wasmprobe/'], { cwd: backend, env: { ...process.env, GOOS: 'windows', GOARCH: 'amd64' } });
} catch (e) {
  fail('go run (native) failed', e.stderr || e.message);
}
if (!nativeOut.includes('"name"')) fail('native probe produced no JSON', nativeOut);
console.log('ok');

// 2) 编 wasm
process.stdout.write('[wasmprobe] 2/4 building js/wasm ... ');
try {
  run('go', ['build', '-o', wasmPath, './cmd/wasmprobe/'], { cwd: backend, env: { ...process.env, GOOS: 'js', GOARCH: 'wasm' } });
} catch (e) {
  fail('GOOS=js GOARCH=wasm build failed', e.stderr || e.message);
}
if (!existsSync(wasmPath)) fail('wasm artifact missing after a successful build');
const bytes = readFileSync(wasmPath).length;
console.log(`ok (${(bytes / 1048576).toFixed(2)} MB)`);

// 3) 在 Node 宿主里跑 wasm
process.stdout.write('[wasmprobe] 3/4 running under Node host ... ');
const goroot = process.env.GOROOT?.trim() || run('go', ['env', 'GOROOT']).trim();
const execNode = path.join(goroot, 'lib', 'wasm', 'wasm_exec_node.js');
if (!existsSync(execNode)) {
  fail(`wasm_exec_node.js not found at ${execNode} (Go >= 1.21 ships it; check GOROOT)`);
}
let wasmOut;
try {
  wasmOut = run(process.execPath, [execNode, wasmPath]);
} catch (e) {
  fail('wasm run under node failed', e.stderr || e.message);
}
if (!wasmOut.includes('"name"')) fail('wasm probe produced no JSON', wasmOut);
console.log('ok');

// 4) 逐字节比对
process.stdout.write('[wasmprobe] 4/4 comparing native vs wasm ... ');
const norm = (s) => s.replace(/\r\n/g, '\n').trim();
if (norm(nativeOut) !== norm(wasmOut)) {
  writeFileSync(nativeJson, nativeOut);
  writeFileSync(wasmJson, wasmOut);
  fail('native and wasm outputs DIFFER (dumps written next to the probe for diffing)');
}
console.log(`ok (IDENTICAL, ${norm(nativeOut).length} chars)`);

if (!keep) {
  rmSync(wasmPath, { force: true });
  for (const f of [nativeJson, wasmJson]) rmSync(f, { force: true });
}

console.log(`
[wasmprobe] PASS
  §7cc 结论复核通过：Go 纯逻辑（internal/email/rules）编到 js/wasm 后
  与 windows/amd64 原生产出逐字节一致的输出。
  代价：${(bytes / 1048576).toFixed(2)} MB 的 Go runtime，-s -w 只压掉约 1.7%。
  这条链路只对**无 socket** 的纯逻辑成立（socket 在 js/wasm 下是 ENOSYS）。
`);
