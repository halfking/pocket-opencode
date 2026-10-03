#!/usr/bin/env node
// 核验 **APK 里实际打进去的 bundle** 是否包含预期改动。
//
// 为什么需要它：`build-mobile.mjs` 里的 `cap sync` 会偶发 `exit=null` 被信号杀掉，
// 此时 vite build 与 gradle **都报成功**，但 bundle 根本没换 —— 我据此差点做了
// 一次**无效的证伪**。凡是「构建成功」都不能当成「内容已更新」。
//
// 用法：
//   node scripts/verify-apk-bundle.mjs <搜索串1> [搜索串2 ...]
//  搜索串在 android/app/src/main/assets/public/assets/*.js 里查找。
//  全部找到 → exit 0；任一缺失 → exit 1 并明确报出缺哪个。
//
// 反例用法（必须缺失时）：
//   node scripts/verify-apk-bundle.mjs --must-not-exist <串>
import fs from 'node:fs';
import path from 'node:path';

const ASSETS = path.resolve('android/app/src/main/assets/public/assets');
const args = process.argv.slice(2);
if (args.length === 0) {
  console.error('用法: node scripts/verify-apk-bundle.mjs [--must-not-exist] <搜索串...>');
  process.exit(2);
}
const mustNot = args.includes('--must-not-exist');
const needles = args.filter((a) => a !== '--must-not-exist');

if (!fs.existsSync(ASSETS)) {
  console.error(`FATAL: 找不到 ${ASSETS}`);
  process.exit(2);
}

const files = fs.readdirSync(ASSETS).filter((f) => f.endsWith('.js'));
const blob = files.map((f) => ({ f, s: fs.readFileSync(path.join(ASSETS, f), 'utf8') }));
console.log(`扫描 ${files.length} 个 chunk`);

let bad = 0;
for (const n of needles) {
  const hits = blob.filter((b) => b.s.includes(n));
  if (mustNot) {
    if (hits.length === 0) console.log(`OK   不应存在且确实不存在: ${JSON.stringify(n)}`);
    else { console.log(`FAIL 不该存在却出现在: ${hits.map((h) => h.f).join(', ')}  ${JSON.stringify(n)}`); bad++; }
  } else {
    if (hits.length > 0) console.log(`OK   存在: ${JSON.stringify(n)}  -> ${hits.map((h) => h.f).join(', ')}`);
    else { console.log(`FAIL 缺失: ${JSON.stringify(n)}  —— bundle 不是你以为的那一版`); bad++; }
  }
}

console.log(`\n=== 汇总 ===\n期望 ${mustNot ? '不存在' : '存在'}：${needles.length} 项，异常 ${bad} 项`);
process.exit(bad === 0 ? 0 : 1);
