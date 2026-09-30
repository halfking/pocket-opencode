// 断言 APK 内打包的 capacitor.config.json 真的带了指定 androidScheme。
// 目的：防止「gradle BUILD SUCCESSFUL 但没重新打包 assets」这种假绿。
import { readFileSync, existsSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const apk = process.argv[2];
const expect = process.argv[3] || 'https';

if (!apk || !existsSync(apk)) {
  console.error(`FAIL: apk 不存在: ${apk}`);
  process.exit(2);
}

const work = join(tmpdir(), `apkassert-${Date.now()}`);
execFileSync('powershell', [
  '-NoProfile', '-Command',
  `Add-Type -AssemblyName System.IO.Compression.FileSystem; ` +
  `[System.IO.Compression.ZipFile]::ExtractToDirectory('${apk}', '${work}')`,
], { stdio: 'inherit' });

const cfgPath = join(work, 'assets', 'capacitor.config.json');
if (!existsSync(cfgPath)) {
  console.error('FAIL: APK 内找不到 assets/capacitor.config.json');
  process.exit(3);
}
const cfg = JSON.parse(readFileSync(cfgPath, 'utf8'));
const got = cfg?.server?.androidScheme;
console.log(`APK 内 androidScheme = ${JSON.stringify(got)}（期望 ${expect}）`);
console.log(`APK 内 cleartext = ${JSON.stringify(cfg?.server?.cleartext)}`);
if (got !== expect) {
  console.error('FAIL: APK 内方案与期望不符 —— 装机后跑验证毫无意义');
  process.exit(4);
}
console.log('PASS: APK 产物确实带了目标 scheme');
