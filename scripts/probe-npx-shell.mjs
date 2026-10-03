// probe-npx-shell.mjs — 坐实 BUG-V3：Windows 上 spawnSync("npx", ...) 不带 shell 会返回 status=null。
import { spawnSync } from 'node:child_process';

function run(label, opts) {
  const r = spawnSync('npx', ['--version'], { encoding: 'utf8', timeout: 60000, ...opts });
  console.log(`${label.padEnd(34)} status=${JSON.stringify(r.status)}  error=${r.error ? r.error.code : 'none'}  stdout=${JSON.stringify((r.stdout || '').trim().slice(0, 20))}`);
}

console.log('platform =', process.platform);
run('npx (无 shell)   ← 当前 build-mobile', {});
run('npx (shell:true) ← vite build 那步', { shell: true });
console.log('\n结论：Windows 上 .cmd 必须走 shell:true；build-mobile.mjs 的 cap sync 步没带，所以必然 exit=null。');
