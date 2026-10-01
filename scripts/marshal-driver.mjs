// marshal-driver.mjs — 在独立子进程里实例化一次 Go/wasm 并跑一个形态。
//
// ## 为什么必须每个形态起一个子进程
//
// Go/wasm 的 `main()` 是**一次性**的：进程内 `go.run()` 返回后，Go 运行时
// 已经退出，不能再调第二次。所以想测 bulk 和 struct 两条路径，
// 就得各自实例化一次。放在同一个 Node 进程里连续跑两次会得到
// 「第二次根本没执行」的空结果 —— 那种假绿比报错更难发现。
//
// 用法（由 scripts/marshal-probe.mjs 调用，不要手工跑）：
//
//   node marshal-driver.mjs <GOROOT> <marshal.wasm> <bulk|struct|permail>
//
// 数据从 stdin 读 JSON；结果由 wasm 侧自己打到本进程 stdout（见文件末尾的说明）。
//
// ## 为什么不能 require('wasm_exec_node.js')
//
// 它是一个**立即执行**的命令行包装器：模块被 require 的瞬间就
// `WebAssembly.instantiate(fs.readFileSync(process.argv[2]))`。
// 本文件的 argv 是 `[node, driver.mjs, <goroot>, <wasm>, <mode>]`，
// 于是 argv[2] 指向的是 wasm_exec_node.js 自己 —— 读它的源码去当 wasm，
// 报 `expected magic word 00 61 73 6d, found 2f 2f 20 43`（`// C` 版权注释）。
//
// 正确做法：自己铺 wasm_exec.js 需要的 globalThis，然后只 require
// `wasm_exec.js` —— 它只定义 `Go` 类，不自动运行。
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import path from 'node:path';

const require = createRequire(import.meta.url);
const [goroot, wasmPath, mode] = process.argv.slice(2);

if (!goroot || !wasmPath || !mode) {
  console.error('usage: marshal-driver.mjs <GOROOT> <marshal.wasm> <bulk|struct|permail>');
  process.exit(2);
}

globalThis.require = require;
globalThis.fs = require('fs');
globalThis.path = require('path');
globalThis.TextEncoder = require('util').TextEncoder;
globalThis.TextDecoder = require('util').TextDecoder;
globalThis.performance ??= require('performance');
globalThis.crypto ??= require('crypto');
require(path.join(goroot, 'lib', 'wasm', 'wasm_exec.js'));

// stdin 是 payload：bulk 收 {input: "<json>"}，struct 收 {rows: [...]}，
// permail 收 {permail: ["<json>", ...]}（每封一个独立字符串，模拟每封一次往返）。
//
// **必须异步读完整个 stdin**：`readFileSync(0, 'utf8')` 从管道读时只拿到
// 当前已在缓冲区里的那部分 —— 实测 19036 字节的 payload 只读到 1919 字符。
// 症状极具误导性：wasm 侧 `json.Unmarshal` 失败 → 提前 `return` → stdout 全空，
// 父进程报 "Unexpected end of JSON input"，看起来像 wasm 没跑。
const stdinChunks = [];
for await (const chunk of process.stdin) stdinChunks.push(chunk);
const payload = JSON.parse(Buffer.concat(stdinChunks).toString('utf8'));
if (mode === 'bulk') {
  globalThis.marshalInput = payload.input;
} else if (mode === 'struct') {
  globalThis.marshalRows = payload.rows;
} else if (mode === 'permail') {
  globalThis.marshalPerMail = payload.permail;
} else {
  console.error(`unknown mode: ${mode}`);
  process.exit(2);
}

// 结果由 wasm 侧的 `fmt.Println` 发出。js/wasm 的 `os.Stdout` 在
// wasm_exec.js 里被接到 **`console.log`**（它的 fs.writeSync polyfill 是
// `console.log(outputBuf.substring(0, nl))`）。
//
// **必须劫持 console.log 并改用 fs.writeSync 同步转发**，两个原因：
//  1. 同步性：Node 的 `console.log` 对 pipe 是**异步**的，进程自然退出时
//     缓冲可能没刷完，父进程收到空 stdout（实测 exit=0、stderr 全空、stdout 0 字节）。
//     `fs.writeSync(1, ...)` 是同步的，立刻落盘。
//  2. 可控：wasm_exec.js 在模块作用域里引用 `console.log`，
//     劫持 `globalThis.console.log` 即可接管它的输出。
const origLog = console.log;
console.log = (...args) => {
  globalThis.fs.writeSync(1, args.join(' ') + '\n');
};

const go = new Go();

// 这两样**必须设**，否则 wasm 里的 main 静默不执行。
//   argv → 传给 Go 的 os.Args
//   env  → 传给 Go 的 os.Getenv；TMPDIR 缺失时 runtime 初始化会出问题
go.argv = [];
go.env = { ...process.env, TMPDIR: process.env.TMPDIR ?? process.env.TEMP ?? '.' };

const { instance } = await WebAssembly.instantiate(readFileSync(wasmPath), go.importObject);

let exitCode = null;
go.exit = (code) => { exitCode = code; };
await go.run(instance);

console.log = origLog;

if (exitCode !== null && exitCode !== 0) {
  process.stderr.write(`wasm exited with code ${exitCode}\n`);
  process.exit(1);
}

// stdout 就是结果 JSON —— 已在上面同步写出，这里无需再转发。

