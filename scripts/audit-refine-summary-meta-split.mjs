#!/usr/bin/env node
// audit-refine-summary-meta-split —— 钉住 §385 的三个结构性事实，
// 并让待拍板①「useSessionLiveRecord 不传 meta」这个登记项**可被自动发现已修复**。
//
// 为什么需要它
// ------------
// §380.3 把「让 useSessionLiveRecord.ts:17 也传 meta」排成待拍板**第一位**，
// 理由是「它是唯一一档会造成净损害的」。§385 核实后发现：那个「净损害」来自
// **精校路径**（llmMeetingRefine）的读数，而 ① 落在**即时总结路径**（useLiveSummary）
// ——两条路径的提示词构造器不同、判据不同。
//
// ⇒ 而「① 还没修」这件事**只以散文形式记在设计文档里**。
// 一旦有人把它修掉，待拍板清单不会更新、§380.3 的排序理由也不会撤回，
// 下一个人会把一条**已经不成立的**理由当成第一优先级。
//   「登记表是会腐烂的」——本仓已经吃过一次（§119 文案与实际不符，§159 才改口）。
//
// 四条断言
// --------
//   A1 `metaTermHint` 的**生产调用点恰好一个**，且落在 `buildRefinePrompt` 里
//      ⇒ 「术语表只存在于精校路径」这件事不成立时立刻报红。
//   A2 `buildSummaryPrompt` 的签名**没有** meta 形参
//      ⇒ 谁给它加了 meta，这道闸立刻知道（那时 §385 与本闸都要改写）。
//   A3 `useSessionLiveRecord` 调 `useLiveSummary` 时**不传** opts
//      ⇒ A3 转绿 = 待拍板① 已被修复，本闸改报 **STALE** 并建议撤登记。
//   A4 `MeetingDetailView` 调 `useLiveSummary` 时**传** meta
//      ⇒ 这是「两条路径不一致」的对照臂；两条一起钉，才不会退化成同一条实验。
//   A5（§387）会话录音送进**精校**的 `Title` 现状是 `ses_edac2` 这样的 ID 前缀，
//      且 `stores/session.ts` 的 `title.value` 恰好只有一处赋值（`open()` 不回填）
//      ⇒ 两者任一被修，本闸都改报 STALE 登记项。
//      与 A3 不同源：A3 问的是**总结**路径收不收 meta，A5 问的是**精校**路径收到的是什么。
//      同属「会话录音到底把什么证据送给了模型」，故并进本闸而非另开一道。
//
// 判据自证（--meta）：四个变异逐个注入，每个都必须报红；
// 外加一条阴性对照（干净源码必须全绿）。闸失去区分能力 ⇒ rc=2。
//
// 用法：
//   node scripts/audit-refine-summary-meta-split.mjs          # 审计
//   node scripts/audit-refine-summary-meta-split.mjs --meta   # 判据自证
//
// 退出码：0 = 一致；1 = 不一致；2 = 判据自证失败（闸坏了，不是代码坏了）。

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = process.env.META_SPLIT_ROOT ? path.resolve(process.env.META_SPLIT_ROOT) : path.resolve(SCRIPT_DIR, '..');

const FILES = {
  go: 'backend/internal/server/server_meeting.go',
  session: 'frontend/src/features/sessions/useSessionLiveRecord.ts',
  detail: 'frontend/src/features/meetings/MeetingDetailView.vue',
  sessionStore: 'frontend/src/stores/session.ts',
  conversation: 'frontend/src/features/sessions/SessionConversationView.vue',
};

/** 剥掉注释，避免「注释里提到的调用点」被当成真调用点（§49 已踩过同一条）。 */
function stripComments(src, isVue) {
  let s = src;
  if (isVue) {
    s = s.replace(/<!--[\s\S]*?-->/g, '');
    s = s.replace(/^\s*\/\/.*$/gm, '');
    s = s.replace(/\/\*[\s\S]*?\*\//g, '');
  } else {
    s = s.replace(/\/\*[\s\S]*?\*\//g, '');
    s = s.replace(/^\s*\/\/.*$/gm, '');
  }
  return s;
}

function load(root) {
  const out = {};
  for (const [k, rel] of Object.entries(FILES)) {
    out[k] = fs.readFileSync(path.join(root, rel), 'utf8');
  }
  return out;
}

/**
 * 括号配平的调用点扫描器。
 *
 * ⚠⚠️ 首版用正则 `useLiveSummary\(([^)]*)\)` —— 两次都错：
 *   ① `useLiveSummary(meetingId, recorder.segments)` 里那个逗号是**两个必填参数的分隔**，
 *      不是第三个参数 ⇒ `/,/.test(args)` 恒真 ⇒ 把「没传 opts」误报成「已传」；
 *   ② `MeetingDetailView` 的调用跨多行且内含 `computed(() => ({…}))`，
 *      `[^)]*` 在 `computed(` 的 `)` 处就断了 ⇒ 实测 0 个调用点。
 * ⇒ 必须**按括号配平**取完整实参表，再数**顶层逗号**。
 */
function extractCalls(src, fname) {
  const out = [];
  const needle = fname + '(';
  let idx = 0;
  for (;;) {
    const i = src.indexOf(needle, idx);
    if (i < 0) break;
    const open = i + fname.length; // 指向 '('
    let depth = 0;
    let j = open;
    for (; j < src.length; j++) {
      const c = src[j];
      if (c === '(') depth++;
      else if (c === ')') {
        depth--;
        if (depth === 0) break;
      }
    }
    if (depth !== 0) break; // 不配平就停，别拿半截当读数
    const inner = src.slice(open + 1, j);
    let d = 0;
    let commas = 0;
    for (const c of inner) {
      if (c === '(' || c === '[' || c === '{') d++;
      else if (c === ')' || c === ']' || c === '}') d--;
      else if (c === ',' && d === 0) commas++;
    }
    const lineNo = src.slice(0, i).split('\n').length;
    const lineText = src.slice(src.lastIndexOf('\n', i) + 1, src.indexOf('\n', i));
    out.push({
      line: lineNo,
      args: inner.trim(),
      argCount: inner.trim() === '' ? 0 : commas + 1,
      lineText: lineText.trim(),
      prefix: src.slice(src.lastIndexOf('\n', i) + 1, i),
      start: i,
      end: j + 1,
    });
    idx = j + 1;
  }
  return out;
}

/**
 * 去掉定义与 import —— 它们不是调用点。
 *
 * ⚠ 首版按**整行开头**过滤（`/^\s*const\b/` 之类），结果把
 *   `const summary = useLiveSummary(...)` 这种**真调用**一起滤掉了（实测 0 个调用点），
 *   而 Go 的 `func metaTermHint(` 定义又没被识别（首版只认 JS 的 `function`）。
 * ⇒ 必须看**调用点之前那段文本**，而不是整行开头。
 */
function onlyCallSites(calls) {
  return calls.filter((c) => {
    if (/^\s*import\b/.test(c.lineText)) return false;
    if (/\bfunction\s*$/.test(c.prefix)) return false;
    if (/\bfunc\s+$/.test(c.prefix)) return false;
    return true;
  });
}

/** `useLiveSummary` 的调用点：`argCount >= 3` 才算「传了 opts」。 */
function liveSummaryCalls(src) {
  return onlyCallSites(extractCalls(src, 'useLiveSummary'));
}

/** 数 Go 里 `metaTermHint(` 的调用点，跳过定义那一行。 */
function termHintCallSites(goSrc) {
  return onlyCallSites(extractCalls(goSrc, 'metaTermHint')).map((c) => ({
    line: c.line,
    text: c.lineText,
  }));
}

function audit(root = REPO_ROOT) {
  const findings = [];
  const notes = [];
  const src = load(root);
  const goSrc = stripComments(src.go, false);
  const sessionSrc = stripComments(src.session, false);
  const detailSrc = stripComments(src.detail, true);
  const storeSrc = stripComments(src.sessionStore, false);
  const convSrc = stripComments(src.conversation, false);

  // ---- A1：metaTermHint 生产调用点恰好一个，且在 buildRefinePrompt 里
  const sites = termHintCallSites(goSrc);
  if (sites.length !== 1) {
    findings.push({
      kind: 'A1 调用点数',
      msg: `metaTermHint 的生产调用点期望 1 个，实测 ${sites.length} 个：` +
        sites.map((s) => `L${s.line}`).join(', '),
    });
  } else {
    // 该调用点必须落在 buildRefinePrompt 的函数体内
    const idx = sites[0].line;
    const fnStart = goSrc.split('\n').findIndex((l, i) => i + 1 <= idx && /^func\s+buildRefinePrompt\s*\(/.test(l));
    const nextFn = goSrc.split('\n').findIndex((l, i) => i + 1 > idx && /^func\s/.test(l));
    if (fnStart < 0 || (nextFn >= 0 && nextFn <= idx)) {
      findings.push({ kind: 'A1 归属', msg: `metaTermHint 唯一的调用点（L${idx}）不在 buildRefinePrompt 里` });
    }
  }

  // ---- A2：buildSummaryPrompt 签名里没有 meta 形参
  const sig = goSrc.match(/func\s+buildSummaryPrompt\s*\(([^)]*)\)/);
  if (!sig) {
    findings.push({ kind: 'A2', msg: '找不到 buildSummaryPrompt 的签名' });
  } else if (/meta/i.test(sig[1])) {
    findings.push({
      kind: 'A2 签名变了',
      msg: `buildSummaryPrompt 现在收 meta 了：(${sig[1]}) —— §385 与本闸都必须改写`,
    });
  }

  // ---- A3 / A4：两条 useLiveSummary 调用点的不一致
  const sCalls = liveSummaryCalls(sessionSrc);
  const dCalls = liveSummaryCalls(detailSrc);
  if (sCalls.length !== 1) {
    findings.push({ kind: 'A3 调用点数', msg: `useSessionLiveRecord 里 useLiveSummary 调用点期望 1 个，实测 ${sCalls.length} 个` });
  } else if (sCalls[0].argCount >= 3) {
    // ★ 转绿 = 登记项过期，这不是一个「失败」，而是一条必须处理的提示
    notes.push({
      kind: 'STALE 登记项',
      msg: `useSessionLiveRecord.ts:${sCalls[0].line} 现在**已经传** meta 给 useLiveSummary 了 ` +
        `（${sCalls[0].args}）⇒ 待拍板① 的「不传 meta」缺口已被修复。` +
        `请撤下该登记项，并复核 §380.3 的排序理由是否仍然成立。`,
    });
  }
  if (dCalls.length !== 1) {
    findings.push({ kind: 'A4 调用点数', msg: `MeetingDetailView 里 useLiveSummary 调用点期望 1 个，实测 ${dCalls.length} 个` });
  } else if (dCalls[0].argCount < 3) {
    findings.push({
      kind: 'A4 对照臂消失',
      msg: `MeetingDetailView.vue:${dCalls[0].line} 不再传 meta ⇒ 「两条路径不一致」的对照臂没了，` +
        `A3 的判定失去参照`,
    });
  }

  // ---- 一致性：A3/A4 必须真的不一致，否则本闸钉的是同一条实验
  if (sCalls.length === 1 && dCalls.length === 1 && (sCalls[0].argCount >= 3) === (dCalls[0].argCount >= 3)) {
    findings.push({
      kind: 'A3/A4 同构',
      msg: '两条路径的传参形态相同 ⇒ §385 钉的「不一致」已不存在',
    });
  }

  // ---- A5（§387）：会话录音送进精校的「标题」现状是 `ses_xxxx`
  // ⚠ 这一条与 A3 不同源：A3 钉的是**总结**路径收不收 meta；
  //   A5 钉的是**精校**路径收到的 meta 里的 Title 到底是什么。
  //   两者同属一问：「会话录音这条路到底把什么证据送给了模型」，故并进本闸而非另开一道。
  //
  // ⚠⚠ 判定必须**只在 `sessionTitle` 这一个 computed 内部**做。
  //   首版用全文件 `/sessionID[^)\n]*\.slice\(0,\s*\d+\s*\)/` ⇒ 命中**两处**：
  //   ① `sessionTitle` 的兜底（缺陷）
  //   ② 第 464 行一处**正当的显示用法**（`会话 ${sessionID.value.slice(0, 8)}`）
  //   ⇒ 只改掉 ① 时断言仍为真 ⇒ M5 变异「没反应」，闸正确地报了 rc=2。
  //   （这一条是「变异设计错」的第五型：**变异改对了地方，断言却量得太粗**。）
  const titleComputed = onlyCallSites(extractCalls(convSrc, 'computed')).find(
    (c) => /sessionTitle\s*=\s*$/.test(c.prefix),
  );
  if (!titleComputed) {
    findings.push({ kind: 'A5', msg: 'SessionConversationView 里找不到 `const sessionTitle = computed(` 块' });
  }
  const hexFallback = titleComputed ? /sessionID[^\n]*?\.slice\(\s*0\s*,\s*\d+\s*\)/.test(titleComputed.args) : false;
  if (!hexFallback) {
    notes.push({
      kind: 'STALE 登记项',
      msg: 'SessionConversationView 的 `sessionTitle` 已不再回退到 `sessionID.slice(0, N)` ⇒ ' +
        '§387 的「会议标题常态是 ses_xxxx」已不成立。' +
        '请撤下该登记项，并**顺带确认接进来的是真标题而不是 `New session - <时间戳>` 这类占位符**。',
    });
  }
  const storeAssignments = (storeSrc.match(/\btitle\.value\s*=/g) || []).length;
  if (storeAssignments > 1) {
    notes.push({
      kind: 'STALE 登记项',
      msg: `stores/session.ts 现在有 ${storeAssignments} 处给 title.value 赋值（原先恰好 1 处）⇒ ` +
        '§387.1②「open() 不回填标题」已不成立，结论需按新链路重核。',
    });
  }
  if (!hexFallback && storeAssignments <= 1) {
    // 两处都已修但互相矛盾 —— 说明只是换了写法，登记项仍需人工确认
    notes.push({
      kind: 'STALE 登记项',
      msg: 'A5 两处读数不一致（兜底已改但 store 仍不回填）⇒ 请人工确认 §387 的结论是否还成立。',
    });
  }

  // ---- A6（§388）：`createMeeting` 的 title 实参**不得裸用** `sessionTitle()`
  //   这是 §387 那条缺陷的**修后不变量**，与实现写法无关。
  //
  // ⚠⚠ 首版判成「title 实参里出现 `sessionTitle` 就算错」⇒ **在正确代码上恒红**：
  //   修好后实参是 `resolveRecordingTitle(sessionId(), sessionTitle())`，
  //   守卫函数**就在实参里**，可实参仍然**提到**了那个名字 ⇒ 判据报「修复不在」。
  //   ★ 这是形态门第五种翻车（形态门恒红的翻法：太松 / 太紧 / 太粗 / 变异空操作 / **判据贴名字**）。
  //   ⇒ 改为判**形状**：只有「裸的 sessionTitle() 调用（后面可再 `||` 串东西）」才红；
  //     任何把它**过一道函数**的写法都放行。
  const createCalls = onlyCallSites(extractCalls(sessionSrc, 'createMeeting'));
  if (createCalls.length !== 1) {
    findings.push({ kind: 'A6 调用点数', msg: `useSessionLiveRecord 里 createMeeting 调用点期望 1 个，实测 ${createCalls.length} 个` });
  } else {
    const titleExpr = createCalls[0].args.match(/title:([\s\S]*?),\s*sessionId:/);
    if (!titleExpr) {
      findings.push({
        kind: 'A6 无法解析',
        msg: `createMeeting 的实参里找不到 'title: … , sessionId:' 的形状，无法判定：` +
          createCalls[0].args.replace(/\s+/g, ' ').slice(0, 120),
      });
    } else {
      const e = titleExpr[1].trim();
      const bare = /^(?:sessionTitle\s*\(\s*\)|\w+\.sessionTitle\s*\(\s*\))(?:\s*\|\|[\s\S]*)?$/.test(e);
      if (bare) {
        findings.push({
          kind: 'A6 ID 片段可流入',
          msg: `createMeeting 的 title 实参**裸用** sessionTitle() ⇒ §388 的修复不在，` +
            `会议标题可能仍是 ses_xxxx。实参：${e}`,
        });
      }
    }
  }

  return { findings, notes, sites, sCalls, dCalls, storeAssignments, hexFallback };
}

// ---------------------------------------------------------------- --meta

const MUTATIONS = [
  {
    name: 'M1 给 buildSummaryPrompt 加上 meta 形参（摘要路径开始用术语表）',
    apply: (f) => {
      f.go = f.go.replace('func buildSummaryPrompt(transcript, prev string) string {',
        'func buildSummaryPrompt(transcript, prev string, meta meetingMetaIn) string {');
    },
    expect: 'A2 签名变了',
  },
  {
    name: 'M2 给 useSessionLiveRecord 补上 opts.meta（把待拍板① 直接修掉）',
    apply: (f) => {
      f.session = f.session.replace('useLiveSummary(meetingId, recorder.segments)',
        'useLiveSummary(meetingId, recorder.segments, { meta: computed(() => ({ title: sessionTitle() })) })');
    },
    expect: 'A3 转绿 ⇒ 报 STALE 登记项（不是「失败」，是必须处理的提示）',
  },
  {
    // ⚠⚠️ 首版这一条写成「插入 /* dropped */ 0 && {」—— 而闸在审计前会**剥注释**，
    //   那段 `/* … */` 当场被清掉 ⇒ 变异等于没做 ⇒ 实测「完全无反应」。
    //   ⭐ 而**闸报对了**：它没有假装通过，而是 rc=2 说「我没有区分能力」。
    //   那是变异设计错，不是闸没牙（四类归因里的第三类）。
    //   现按括号配平把整个调用替换掉，不再依赖字符串插入。
    name: 'M3 把 MeetingDetailView 的第三个实参整个删掉（对照臂消失）',
    apply: (f) => {
      const calls = onlyCallSites(extractCalls(f.detail, 'useLiveSummary'));
      if (calls.length !== 1) throw new Error(`M3 前提不成立：找到 ${calls.length} 个调用点`);
      const c = calls[0];
      f.detail = f.detail.slice(0, c.start) + 'useLiveSummary(meetingId, segments)' + f.detail.slice(c.end);
    },
    expect: 'A4 对照臂消失 / A3/A4 同构',
  },
  {
    name: 'M4 把 metaTermHint 复制进摘要分支（术语表悄悄铺开到第二条路径）',
    apply: (f) => {
      f.go = f.go.replace('func buildSummaryPrompt(transcript, prev string) string {',
        'func buildSummaryPrompt(transcript, prev string) string {\n\t_ = metaTermHint(meetingMetaIn{})');
    },
    expect: 'A1 调用点数 = 2',
  },
  {
    // M5：把 §387 的缺陷「修掉」——兜底不再用 ID 前缀。正确反应是 STALE 登记项。
    name: 'M5 把 sessionTitle 的 ID 前缀兜底换成真标题（修掉 §387）',
    apply: (f) => {
      f.conversation = f.conversation.replace(/return\s+sessionID\.value\.slice\(0,\s*\d+\)/,
        "return store.title || '会话录音'");
    },
    expect: 'A5 报 STALE 登记项（§387 的 ses_xxxx 结论已过期）',
  },
  {
    // M6：让 stores/session.ts 在 open() 里回填标题（另一种修法）
    name: 'M6 让 stores/session.ts 多一处 title.value 赋值（open() 回填）',
    apply: (f) => {
      f.sessionStore = f.sessionStore.replace('if (initialTitle) title.value = initialTitle',
        "if (initialTitle) title.value = initialTitle\n    title.value = (await api.sessionTitle(sid)) || ''");
    },
    expect: 'A5 报 STALE 登记项（open() 不回填这条已不成立）',
  },
  {
    // M7：把 §388 的修复回退掉 —— 判据必须重新变红。
    //
    // ⚠⚠ 首版用硬编码正则去匹配实现里的表达式：
    //    `/title: isSessionIdFragment\(rawTitle\)\s*\?…/`
    //    而我随后把签名从 `(rawTitle)` 改成 `(sessionId(), rawTitle)`（vue-tsc 要求）
    //    ⇒ 正则失配 ⇒ 变异**静默变成空操作** ⇒ 闸报「无反应」并 rc=2。
    //    ⇒ 这不是闸没牙，是**变异自己随实现一起过期了**（§385.5 第四型的又一次）。
    //    ⇒ 修法：按括号配平取出 `createMeeting(…)` 的整个实参表再重写，
    //      只锚定下一个键 `sessionId:`，实现怎么改都不会失配。
    name: 'M7 回退 §388：createMeeting 的 title 直接用 sessionTitle()',
    apply: (f) => {
      const calls = onlyCallSites(extractCalls(f.session, 'createMeeting'));
      if (calls.length !== 1) throw new Error(`M7 前提不成立：找到 ${calls.length} 个 createMeeting 调用点`);
      const c = calls[0];
      const open = c.start + 'createMeeting'.length;
      const inner = f.session.slice(open, c.end);
      const rewritten = inner.replace(/title:[\s\S]*?sessionId:/, "title: sessionTitle() || '会话录音', sessionId:");
      if (rewritten === inner) throw new Error('M7 没能注入：title 与 sessionId 之间没匹配上');
      f.session = f.session.slice(0, open) + rewritten + f.session.slice(c.end);
    },
    expect: 'A6 ID 片段可流入',
  },
];

function meta() {
  const pristine = load(REPO_ROOT);
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'meta-split-'));
  const write = (files) => {
    for (const [k, rel] of Object.entries(FILES)) {
      const p = path.join(tmp, rel);
      fs.mkdirSync(path.dirname(p), { recursive: true });
      fs.writeFileSync(p, files[k]);
    }
  };

  let alive = 0;
  let broken = 0;
  try {
    // 阴性对照：干净源码必须全绿
    write(pristine);
    const clean = audit(tmp);
    const cleanBad = clean.findings.length;
    console.log(`META  N0 干净源码零误报\n      ${cleanBad === 0 ? '✅ 未报红' : `❌ 报了 ${cleanBad} 处：${clean.findings.map((f) => f.msg).join(' | ')}`}`);
    if (cleanBad !== 0) broken++;

    for (const m of MUTATIONS) {
      const f = { ...pristine };
      m.apply(f);
      write(f);
      const r = audit(tmp);
      // M2 的正确反应是 **note**，不是 finding —— 两者都算「闸有反应」
      const reacted = r.findings.length > 0 || r.notes.length > 0;
      console.log(`META  ${m.name}\n      ${reacted ? '✅ 有反应' : '❌ 完全无反应 —— 闸没有牙'}  预期：${m.expect}`);
      if (reacted) {
        alive++;
        for (const x of r.findings) console.log(`      findings：${x.kind}: ${x.msg}`);
        for (const x of r.notes) console.log(`      notes：${x.kind}: ${x.msg}`);
      } else broken++;
    }
  } finally {
    fs.rmSync(tmp, { recursive: true, force: true });
  }

  console.log(`\n=== 判据自证 ===\n${alive} 个变异有反应，${broken} 个无反应`);
  if (broken > 0) {
    console.log('闸失去区分能力 —— 这是**闸**坏了，不是代码坏了。');
    process.exit(2);
  }
  console.log('闸有牙，且不恒红。');
}

// ---------------------------------------------------------------- main

if (process.argv[2] === '--meta') {
  meta();
} else {
  const { findings, notes, sites } = audit();
  console.log(`扫描根 ${REPO_ROOT}`);
  console.log(`metaTermHint 生产调用点：${sites.length} 个 ${sites.map((s) => `L${s.line}`).join(', ')}`);
  for (const n of notes) console.log(`\n${n.kind}\n  ${n.msg}`);
  console.log('\n=== 汇总 ===');
  if (findings.length === 0) {
    console.log('PASS  §385 的四条结构性事实全部成立');
    process.exit(0);
  }
  for (const f of findings) console.log(`FAIL  ${f.kind}: ${f.msg}`);
  console.log(`\n共 ${findings.length} 处不一致`);
  process.exit(1);
}