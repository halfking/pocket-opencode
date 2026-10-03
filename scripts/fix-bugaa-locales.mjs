// BUG-AA 修复：flashcards.list.create 在 7 种语言里仍是「建卡组」的直译，
// 但这个按钮（FlashcardListView.goCreate）实际跳 /flashcards/new = **新建卡片页**。
//
// 关键：**不能用 JSON.parse/stringify 重写整个文件** —— 那会重排格式、产生几百行假 diff。
// 这里只做定点字符串替换：定位 flashcards.list.create 这个键所在的行，替换它的值。
// 替换前先确认该行确实是这个键（避免同名键在别处被误伤），替换后重新 JSON.parse 校验。
import fs from 'node:fs';
import { execSync } from 'node:child_process';

// 「卡片」用词全部取自仓库**已有的** flashcards.deck.addCard / flashcards.edit.title，
// 保证与项目自身译法一致，不凭空造词。
const FIX = {
  'zh-TW': { from: '新增卡組', to: '新增卡片' },
  'ja-JP': { from: 'デッキを作成', to: '新しいカード' },
  'ko-KR': { from: '덱 만들기', to: '새 카드' },
  'de-DE': { from: 'Stapel erstellen', to: 'Neue Karte' },
  'fr-FR': { from: 'Créer un paquet', to: 'Nouvelle carte' },
  'es-ES': { from: 'Crear mazo', to: 'Nueva tarjeta' },
  'pt-BR': { from: 'Criar baralho', to: 'Novo cartão' },
};

let changed = 0;
let problems = 0;

for (const [lang, { from, to }] of Object.entries(FIX)) {
  const p = `frontend/src/locales/${lang}.json`;
  let s = fs.readFileSync(p, 'utf8');

  // 定位 flashcards -> list -> create 这一段，取出其中第一个 "create" 键的值所在行
  const anchor = s.indexOf('"flashcards"');
  if (anchor === -1) { console.error(`FAIL ${lang}: 找不到 flashcards 段`); problems++; continue; }
  const listAnchor = s.indexOf('"list"', anchor);
  if (listAnchor === -1) { console.error(`FAIL ${lang}: 找不到 flashcards.list 段`); problems++; continue; }
  const deckAnchor = s.indexOf('"deck"', listAnchor);
  if (deckAnchor === -1) { console.error(`FAIL ${lang}: 找不到 flashcards.deck 段`); problems++; continue; }

  const seg = s.slice(listAnchor, deckAnchor);
  const m = seg.match(/("create"\s*:\s*")((?:[^"\\]|\\.)*)(")/);
  if (!m) { console.error(`FAIL ${lang}: flashcards.list 段内没有 "create" 键`); problems++; continue; }

  const cur = m[2];
  if (cur === to) {
    // 幂等：已经修好了（上一轮写过），直接跳过，不算失败
    console.log(`SKIP ${lang.padEnd(6)} 已是 ${JSON.stringify(to)}`);
    changed++;
    continue;
  }
  if (cur !== from) {
    console.error(`FAIL ${lang}: 期望旧值 ${JSON.stringify(from)}，实际 ${JSON.stringify(cur)} —— 不盲目替换`);
    problems++;
    continue;
  }

  const at = listAnchor + m.index;
  s = s.slice(0, at) + m[1] + to + m[3] + s.slice(at + m[0].length);

  // 写盘前校验：仍是合法 JSON，且新值确实落位
  let j;
  try { j = JSON.parse(s); } catch (e) { console.error(`FAIL ${lang}: 替换后 JSON 非法 ${e.message}`); problems++; continue; }
  const got = j?.flashcards?.list?.create;
  if (got !== to) { console.error(`FAIL ${lang}: 替换后取到 ${JSON.stringify(got)}`); problems++; continue; }
  const deckCreate = j?.flashcards?.deck?.create;
  // 对照：flashcards.deck.create 必须与 origin/main 基线**完全相同**
  // （那是「新建卡组」，语义正确，不能被这次改动碰到）。
  // 判据取自基线而不是硬编码词表 —— 上一版硬编码只覆盖了 3 种语言写法，
  // 结果把 6 个语言文件误判为「误伤」。**判据要能覆盖全量取值。**
  let base;
  try {
    base = JSON.parse(execSync(`git show origin/main:${p}`, { encoding: 'utf8', maxBuffer: 1 << 28 })).flashcards.deck.create;
  } catch (e) {
    console.error(`FAIL ${lang}: 读 origin/main 基线失败 ${e.message}`);
    problems++;
    continue;
  }
  if (deckCreate !== base) {
    console.error(`FAIL ${lang}: flashcards.deck.create 被误伤 基线=${JSON.stringify(base)} 现状=${JSON.stringify(deckCreate)}`);
    problems++;
    continue;
  }

  fs.writeFileSync(p, s, 'utf8');
  console.log(`OK   ${lang.padEnd(6)} flashcards.list.create  ${JSON.stringify(from)} -> ${JSON.stringify(to)}   (deck.create 保持 ${JSON.stringify(deckCreate)})`);
  changed++;
}

console.log(`\n=== 汇总 ===\n改动 ${changed}/${Object.keys(FIX).length} 个语言文件，失败 ${problems} 个`);
process.exit(problems === 0 && changed === Object.keys(FIX).length ? 0 : 1);
