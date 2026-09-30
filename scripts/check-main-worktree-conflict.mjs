// 检查主工作区（并发会话正在改的那份）里，我推送到 origin/main 的修复**是否还在**。
// 如果不在，说明并发会话的本地改动会把已推送的修复覆盖掉 —— 这是必须报的风险。
import fs from 'node:fs';
import { execSync } from 'node:child_process';

const ROOT = 'C:/workspace/openpocket';
const WANT = {
  'ja-JP': '新しいカード',
  'ko-KR': '새 카드',
  'de-DE': 'Neue Karte',
  'fr-FR': 'Nouvelle carte',
  'es-ES': 'Nueva tarjeta',
  'pt-BR': 'Novo cartão',
  'zh-TW': '新增卡片',
};

console.log('=== 主工作区本地 locale 的 flashcards.list.create ===');
let missing = 0;
for (const [l, want] of Object.entries(WANT)) {
  const p = `${ROOT}/frontend/src/locales/${l}.json`;
  let got;
  try { got = JSON.parse(fs.readFileSync(p, 'utf8'))?.flashcards?.list?.create; }
  catch (e) { console.log(`${l.padEnd(6)} 读取失败 ${e.message}`); missing++; continue; }
  const ok = got === want;
  if (!ok) missing++;
  console.log(`${ok ? 'OK  ' : 'MISS'} ${l.padEnd(6)} ${JSON.stringify(got)}${ok ? '' : `  （origin/main 是 ${JSON.stringify(want)}）`}`);
}

console.log('\n=== 主工作区本地 StudyHubView ===');
const sv = fs.readFileSync(`${ROOT}/frontend/src/features/study/StudyHubView.vue`, 'utf8');
const hasHook = sv.includes('study-deck-create-submit');
const hasLegacy = sv.includes('goCreateDeck');
console.log(`${hasHook ? 'OK  ' : 'MISS'} 含 BUG-AA 修复钩子 : ${hasHook}`);
console.log(`${hasLegacy ? 'WARN' : 'OK  '} 仍含旧 goCreateDeck : ${hasLegacy}`);
if (!hasHook) missing++;

console.log('\n=== origin/main 上这两处是什么状态（对照）===');
const o = (p) => execSync(`git -C ${ROOT} show origin/main:${p}`, { encoding: 'utf8', maxBuffer: 1 << 28 });
const oj = JSON.parse(o('frontend/src/locales/ja-JP.json')).flashcards.list.create;
const osv = o('frontend/src/features/study/StudyHubView.vue');
console.log(`origin/main ja-JP flashcards.list.create = ${JSON.stringify(oj)}`);
console.log(`origin/main StudyHubView 含修复钩子 = ${osv.includes('study-deck-create-submit')}`);

console.log(`\n=== 汇总 ===\n主工作区缺失我已推送修复的项: ${missing}`);
console.log(missing > 0
  ? '⚠️  并发会话的本地改动会覆盖 origin/main 上的修复；快进会被 git 拒绝（这是好事，不要强行绕过）'
  : 'OK  主工作区已含全部修复');
