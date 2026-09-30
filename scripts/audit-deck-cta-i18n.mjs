#!/usr/bin/env node
// audit-deck-cta-i18n —— 锁住「文案语义与实际行为不符」这一类闪卡缺陷。
//
// ## 为什么需要它
//
// BUG-K 修过一次「按钮写『新建卡组』实际跳新建卡片页」，但只改对了 zh-CN / en-US，
// **其余 7 种语言仍是旧文案的直译**（デッキを作成 / 덱 만들기 / Stapel erstellen …）。
// 而 BUG-AA 又在 StudyHubView 里发现了**同一类问题的第二个实例**（9/9 全错）。
//
// 根因是「只盯着一种语言验证」。所以这里的判据必须**与语言无关**。
//
// ## 三条判据
//
//   A. `flashcards.list.create`（跳 /flashcards/new，建**卡片**）与
//      `flashcards.deck.create`（真正建**卡组**）在**每一种语言里都必须不同**。
//      两者相同 = 用户无法分辨这两个 CTA = BUG-K/BUG-AA 复发。
//      这条判据不需要知道任何一种语言的「卡组」怎么写。
//
//   B. 任何把导航指向 `/flashcards/new` 的地方，**不得**使用键名含 deck 的文案键。
//      （`/flashcards/new` 是新建**卡片**页。）
//
//   C. 报告（不判失败）：值与 en-US **逐字节相同**且含 ASCII 字母 → 疑似未翻译。
//      这是 BUG-AA 顺带发现的另一个既有缺陷（study.decks.* 整块 7 语言是英文），
//      修它要逐条审译文，不该混进本次提交，所以只报不拦。
//
// ## 判据自证（--meta）
// 对 A / B / C 各自注入一个「坏样本」，确认检测器确实会报；
// 同时确认干净样本不误报。判据失效 → 退出码 2，而不是安静放行。
//
// 用法：
//   node scripts/audit-deck-cta-i18n.mjs                    扫工作区
//   node scripts/audit-deck-cta-i18n.mjs --meta             只跑判据自证
//   node scripts/audit-deck-cta-i18n.mjs --ref origin/main  扫某个 git ref
//
// --ref 的用途是**证伪**：判据必须能在「修复前」的状态上报出失败，
// 否则它只是一段没被证伪过的漂亮代码。

import { execSync } from 'node:child_process';
import fs from 'node:fs';

const LANGS = ['zh-CN', 'en-US', 'zh-TW', 'ja-JP', 'ko-KR', 'de-DE', 'fr-FR', 'es-ES', 'pt-BR'];
const NEW_CARD_ROUTE = '/flashcards/new';
const K_CARD = 'flashcards.list.create';   // 建卡片
const K_DECK = 'flashcards.deck.create';   // 建卡组

const get = (o, path) => path.split('.').reduce((a, k) => (a == null ? a : a[k]), o);

// ── 判据 A2：人工审定的黄金译文表（**真正承重的那条**）──
//
// 为什么需要 A2：判据 A 只查「两个值字面不同」，在 origin/main 上实测**只报出 1/7**
// （zh-TW 两个字面恰好相同）。其余 6 种语言字面不同但语义相同 ——
// ja-JP「デッキを作成」vs「新しいデッキ」字面有别，说的却都是「建卡组」。
// **判据 A 抓不了语义等价。** 若只靠 A 就会漏掉 6/7 的真实缺陷。
//
// 所以这里把**人工审定的正确译文钉死**：list.create 必须是「新建卡片」的译法，
// deck.create 必须是「新建卡组」的译法。译文取自仓库既有的
// flashcards.deck.addCard / flashcards.edit.title 用词，保持项目内部一致。
// 这条判据对**全部 9 种语言**都有区分能力，且不依赖任何「字面不同」的巧合。
const GOLDEN = {
  'zh-CN': { card: '新建卡片', deck: '新建卡组' },
  'en-US': { card: 'New card', deck: 'New deck' },
  'zh-TW': { card: '新增卡片', deck: '新增卡組' },
  'ja-JP': { card: '新しいカード', deck: '新しいデッキ' },
  'ko-KR': { card: '새 카드', deck: '새 덱' },
  'de-DE': { card: 'Neue Karte', deck: 'Neuer Stapel' },
  'fr-FR': { card: 'Nouvelle carte', deck: 'Nouveau paquet' },
  'es-ES': { card: 'Nueva tarjeta', deck: 'Nuevo mazo' },
  'pt-BR': { card: 'Novo cartão', deck: 'Novo baralho' },
};

function checkA2(locales) {
  const out = [];
  for (const [lang, want] of Object.entries(GOLDEN)) {
    const j = locales[lang];
    if (!j) { out.push({ fail: `${lang}: locale 缺失` }); continue; }
    const card = get(j, K_CARD);
    const deck = get(j, K_DECK);
    if (card !== want.card) {
      out.push({ fail: `${lang}: ${K_CARD} 应为「新建卡片」的译法 ${JSON.stringify(want.card)}，实际 ${JSON.stringify(card)}` +
        (card === want.deck ? '（与建卡组文案相同 = 语义仍是「建卡组」，BUG-K/BUG-AA）' : '') });
    }
    if (deck !== want.deck) {
      out.push({ fail: `${lang}: ${K_DECK} 应为「新建卡组」的译法 ${JSON.stringify(want.deck)}，实际 ${JSON.stringify(deck)}` });
    }
  }
  return out;
}

// ── 判据 A ──
function checkA(locales) {
  const out = [];
  for (const l of LANGS) {
    const j = locales[l];
    if (!j) { out.push({ fail: `${l}: locale 缺失` }); continue; }
    const card = get(j, K_CARD);
    const deck = get(j, K_DECK);
    if (!card) out.push({ fail: `${l}: 缺 ${K_CARD}` });
    if (!deck) out.push({ fail: `${l}: 缺 ${K_DECK}` });
    if (card && deck && card === deck) {
      out.push({ fail: `${l}: ${K_CARD} 与 ${K_DECK} 完全相同（${JSON.stringify(card)}）—— 两个 CTA 无法分辨，BUG-K/BUG-AA 复发` });
    }
  }
  return out;
}

// ── 判据 B ──
//
// 难点：不能按**键名**判定。`flashcards.deck.addCard`（=「添加卡片」）名字里带 deck，
// 语义却是**建卡片**动作，而且它导航到 /flashcards/new 是**正确的**。
// 按键名判会把它误报（第一版就这么翻车了）。
//
// 正确判据：**元素粒度 + 文案值比对**。
//   1. 找出所有可点击元素（button / a）及其 @click；
//   2. 解析出它最终导航到哪（内联表达式，或同名函数体里是否 push 该路由）；
//   3. 若目标是新建**卡片**页，取它自己身上的 t() 键，按 zh-CN（基准语言）解析成文案；
//   4. 若该文案 **等于「建卡组」CTA 的文案** → 失败。
//
// 值比对是语言无关的：不需要知道任何一种语言里「卡组」怎么写。
function checkB(sourceFiles, localeZhCN) {
  const out = [];
  const deckCtaZh = get(localeZhCN, K_DECK);
  if (!deckCtaZh) return [{ fail: `zh-CN 缺 ${K_DECK}，判据 B 失效` }];

  for (const { path, text } of sourceFiles) {
    // 函数体：function name(...) { ... }
    const fnBodies = {};
    for (const m of text.matchAll(/function\s+([A-Za-z_$][\w$]*)\s*\([^)]*\)\s*\{/g)) {
      const start = m.index + m[0].length;
      let depth = 1, i = start;
      while (i < text.length && depth > 0) {
        if (text[i] === '{') depth++;
        else if (text[i] === '}') depth--;
        i++;
      }
      fnBodies[m[1]] = text.slice(start, i);
    }

    // 可点击元素块
    for (const m of text.matchAll(/<(button|a)\b[^>]*>[\s\S]*?<\/\1>/g)) {
      const block = m[0];
      const click = block.match(/@click\s*=\s*"([^"]+)"/);
      if (!click) continue;
      const expr = click[1].trim();

      let targetNewCard = false;
      if (expr.includes(NEW_CARD_ROUTE)) targetNewCard = true;
      else {
        const id = expr.match(/^([A-Za-z_$][\w$]*)$/);
        if (id && fnBodies[id[1]] && fnBodies[id[1]].includes(NEW_CARD_ROUTE)) targetNewCard = true;
      }
      if (!targetNewCard) continue;

      // 这个元素自己的文案键
      for (const t of block.matchAll(/\bt\(\s*'([^']+)'/g)) {
        const val = get(localeZhCN, t[1]);
        if (val !== undefined && val === deckCtaZh) {
          out.push({ fail: `${path}: 指向 ${NEW_CARD_ROUTE}（新建**卡片**页）的按钮，文案却是「建卡组」的文案 ${JSON.stringify(val)}（键 ${t[1]}）` });
        }
      }
    }
  }
  return out;
}

// ── 判据 C（只报不拦）──
function checkC(locales) {
  const base = locales['en-US'] || {};
  const en = get(base, 'study.decks') || {};
  const rows = [];
  for (const l of LANGS) {
    if (l === 'en-US') continue;
    const s = get(locales[l] || {}, 'study.decks') || {};
    const same = Object.keys(en).filter((k) => s[k] === en[k] && /[A-Za-z]/.test(String(en[k])));
    if (same.length) rows.push({ lang: l, keys: same });
  }
  return rows;
}

// ── 判据自证 ──
function meta() {
  // A：把 list.create 改成和 deck.create 一样，检测器必须报
  const badLocales = {};
  for (const l of LANGS) {
    const j = JSON.parse(JSON.stringify(cleanFixture(l)));
    j.flashcards.list.create = j.flashcards.deck.create; // 注入：两者相同
    badLocales[l] = j;
  }
  const cleanLocales = {};
  for (const l of LANGS) cleanLocales[l] = cleanFixture(l);

  const aBad = checkA(badLocales);
  const aGood = checkA(cleanLocales);

  // A2 的自证：注入「语义错但字面不同」这一类 —— 正是 A 抓不到、而 A2 必须抓到的情况。
  const goodA2 = {};
  for (const l of LANGS) goodA2[l] = { flashcards: { list: { create: GOLDEN[l].card }, deck: { create: GOLDEN[l].deck } } };
  const badA2 = JSON.parse(JSON.stringify(goodA2));
  // ja-JP：把「新建卡片」偷偷换成另一个字面不同但语义是「建卡组」的写法
  badA2['ja-JP'].flashcards.list.create = 'デッキを作成';
  badA2['pt-BR'].flashcards.list.create = 'Criar baralho';
  const a2Good = checkA2(goodA2);
  const a2Bad = checkA2(badA2);

  // B 的自证必须覆盖「键名带 deck 但语义是建卡片」这个**易误报**的用例，
  // 否则判据又会在真实代码上炸一堆假警报。
  const zh = { flashcards: { list: { create: '新建卡片' }, deck: { create: '新建卡组', addCard: '添加卡片' } } };
  const fn = (name, body) => `function ${name}() { ${body} }`;

  const bBad = checkB([
    // 坏：跳新建卡片页，文案是「新建卡组」
    { path: 'Bad.vue', text: `<button @click="goCreate">{{ t('flashcards.deck.create') }}</button>` + fn('goCreate', `router.push('${NEW_CARD_ROUTE}')`) },
  ], zh);
  const bGood = checkB([
    // 好1：跳新建卡片页，文案是「新建卡片」
    { path: 'G1.vue', text: `<button @click="goCreate">{{ t('flashcards.list.create') }}</button>` + fn('goCreate', `router.push('${NEW_CARD_ROUTE}')`) },
    // 好2：**键名带 deck 但语义是建卡片**，且导航到新建卡片页是正确的
    { path: 'G2.vue', text: `<button @click="goAdd">{{ t('flashcards.deck.addCard') }}</button>` + fn('goAdd', `router.push('${NEW_CARD_ROUTE}?deckId=x')`) },
    // 好3：跳的不是新建卡片页
    { path: 'G3.vue', text: `<button @click="goDeck">{{ t('flashcards.deck.create') }}</button>` + fn('goDeck', `router.push('/flashcards')`) },
    // 好4：内联表达式直接导航
    { path: 'G4.vue', text: `<button @click="router.push('${NEW_CARD_ROUTE}')">{{ t('flashcards.list.create') }}</button>` },
  ], zh);

  const checks = [
    ['A 注入 list==deck 后报错', aBad.length === LANGS.length, `报了 ${aBad.length}/${LANGS.length}`],
    ['A 干净样本不误报', aGood.length === 0, aGood.map((x) => x.fail).join('; ') || '0 条'],
    ['A2 注入「字面不同但语义错」后报错', a2Bad.length === 2, `报了 ${a2Bad.length}/2`],
    ['A2 黄金表干净样本不误报', a2Good.length === 0, a2Good.map((x) => x.fail).join('; ') || '0 条'],
    ['B 注入「跳卡片页却写建卡组」后报错', bBad.length === 1, `${bBad.length} 条`],
    ['B 干净样本不误报（含键名带 deck 的合法用例）', bGood.length === 0, bGood.map((x) => x.fail).join('; ') || '0 条'],
  ];
  let ok = true;
  for (const [name, pass, detail] of checks) {
    console.log(`  ${pass ? 'ok  ' : 'FAIL'} ${name}  — ${detail}`);
    if (!pass) ok = false;
  }
  if (!ok) { console.error('\n判据自证 FAILED：检测器失去区分能力，本轮结论不可信。'); process.exit(2); }
  // 计数必须动态取，不能写死 —— 写死就会出现「加了一项检查但还是写 4/4」这种
  // 报告比事实乐观的情况，正是本项目反复吃过亏的地方。
  console.log(`判据自证通过（${checks.length}/${checks.length}）\n`);
}

function cleanFixture(lang) {
  return { flashcards: { list: { create: `card-${lang}` }, deck: { create: `deck-${lang}` } } };
}

// ── 主流程 ──
function main() {
  if (process.argv.includes('--meta')) { meta(); return; }

  const ri = process.argv.indexOf('--ref');
  const ref = ri !== -1 ? process.argv[ri + 1] : null;
  const read = ref
    ? (p) => execSync(`git show ${ref}:${p}`, { encoding: 'utf8', maxBuffer: 1 << 28 })
    : (p) => fs.readFileSync(p, 'utf8');

  if (ref) console.log(`扫描目标：${ref}（证伪模式）\n`);

  const locales = {};
  for (const l of LANGS) {
    let raw;
    try { raw = read(`frontend/src/locales/${l}.json`); }
    catch (e) { console.error(`FATAL: ${ref || '工作区'} 缺 ${l}.json，判据失效`); process.exit(2); }
    locales[l] = JSON.parse(raw);
  }

  const files = execSync(`git ls-files frontend/src`, { encoding: 'utf8' })
    .split('\n').filter((f) => /\.(vue|ts)$/.test(f));
  const sourceFiles = files.map((path) => ({ path, text: read(path) }));

  const a = checkA(locales);
  const a2 = checkA2(locales);
  const b = checkB(sourceFiles, locales['zh-CN']);
  const c = checkC(locales);

  console.log('=== 判据 A（弱，仅查字面不同）：建卡片 CTA ≠ 建卡组 CTA ===');
  if (a.length === 0) console.log(`  PASS 9/9 语言字面不同 —— 注意：**这条抓不了语义等价**，在 origin/main 上只报出 1/7`);
  else a.forEach((x) => console.log(`  FAIL ${x.fail}`));

  console.log('\n=== 判据 A2（承重）：对照人工审定的黄金译文表 ===');
  if (a2.length === 0) {
    for (const l of LANGS) {
      console.log(`  PASS ${l.padEnd(6)} ${JSON.stringify(get(locales[l], K_CARD))}  =  「新建卡片」译法`);
    }
  } else a2.forEach((x) => console.log(`  FAIL ${x.fail}`));

  console.log('\n=== 判据 B：指向新建卡片页的地方不得用 deck 文案键 ===');
  if (b.length === 0) console.log('  PASS 无违规');
  else b.forEach((x) => console.log(`  FAIL ${x.fail}`));

  console.log('\n=== 判据 C（只报告，不判失败）：疑似未翻译 ===');
  if (c.length === 0) console.log('  PASS 无');
  else {
    for (const r of c) console.log(`  WARN ${r.lang}: study.decks.{${r.keys.join(', ')}} 与 en-US 逐字节相同（既有缺陷，本轮未修）`);
  }

  const bad = a.length + a2.length + b.length;
  console.log(`\n=== 汇总 ===\n硬失败 ${bad} 项（A=${a.length} A2=${a2.length} B=${b.length}），报告 ${c.length} 项`);
  process.exit(bad === 0 ? 0 : 1);
}

main();
