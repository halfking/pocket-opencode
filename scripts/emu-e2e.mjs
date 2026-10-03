// Maestro-style driver for the OpenCode Pocket Android E2E runs.
//
// Target selection (env var SER, default emulator-5554):
//   PowerShell:  $env:SER="4c308e2e"; node scripts/emu-e2e.mjs shot tag
//   bash:        SER=4c308e2e node scripts/emu-e2e.mjs shot tag
// Evidence dir: logs/.current-emu-e2e (or EVIDENCE_DIR to override).
//
// Usage from PowerShell:
//   node scripts/emu-e2e.mjs shot <name>          capture png + uiautomator xml
//   node scripts/emu-e2e.mjs tap <x> <y> [name] [settleMs]   tap, settle, capture
//   node scripts/emu-e2e.mjs nodes                list tappable nodes with index+centre
//   node scripts/emu-e2e.mjs tapidx <i> [name] [settleMs]    tap node by index
//   node scripts/emu-e2e.mjs tapnode "<t>" [name]  find node by text/desc, tap centre
//   node scripts/emu-e2e.mjs field <i> "<value>"   tap node, then type
//   node scripts/emu-e2e.mjs type "<text>"         type into focused field
//   node scripts/emu-e2e.mjs key <keycode>         keyevent
//   node scripts/emu-e2e.mjs dump                 print visible text tree
//   node scripts/emu-e2e.mjs find "<text>"         print bounds of a node whose text/desc matches
//   node scripts/emu-e2e.mjs ime                  print whether the soft keyboard is shown
//   node scripts/emu-e2e.mjs logcat <tag>          cold-start the app and capture logcat
//   node scripts/emu-e2e.mjs launch | stop | clear
import { execFileSync } from "node:child_process";
import { readFileSync, writeFileSync, existsSync, mkdirSync } from "node:fs";
import path from "node:path";

const ADB = "C:\\Users\\86133\\AppData\\Local\\Android\\platform-tools\\adb.exe";
const SER = process.env.SER || "emulator-5554";
const PKG = "com.kaixuan.opencode.pocket";
const ACT = `${PKG}/.MainActivity`;

const EVIDENCE = (
  process.env.EVIDENCE_DIR ||
  readFileSync("C:/workspace/openpocket/logs/.current-emu-e2e", "utf8").trim()
);
if (!existsSync(EVIDENCE)) mkdirSync(EVIDENCE, { recursive: true });

function adb(args, opts = {}) {
  return execFileSync(ADB, ["-s", SER, ...args], {
    encoding: "utf8",
    maxBuffer: 64 * 1024 * 1024,
    ...opts,
  });
}

function adbBin(args) {
  return execFileSync(ADB, ["-s", SER, ...args], {
    encoding: "buffer",
    maxBuffer: 128 * 1024 * 1024,
  });
}

const wait = (ms) => new Promise((r) => setTimeout(r, ms));

function stamp() {
  return new Date().toISOString().replace(/[:.]/g, "-").slice(0, 19);
}

function capture(tag) {
  const safe = String(tag).replace(/[^A-Za-z0-9._-]/g, "_");
  const base = path.join(EVIDENCE, safe);
  // uiautomator dump + screenshot
  try {
    adb(["shell", "uiautomator", "dump", "/sdcard/_ui.xml"]);
    const xml = adb(["shell", "cat", "/sdcard/_ui.xml"]);
    writeFileSync(`${base}.xml`, xml, "utf8");
  } catch (e) {
    writeFileSync(`${base}.xml.err`, String(e.message || e), "utf8");
  }
  try {
    const png = adbBin(["exec-out", "screencap", "-p"]);
    writeFileSync(`${base}.png`, png);
  } catch (e) {
    writeFileSync(`${base}.png.err`, String(e.message || e), "utf8");
  }
  return base;
}

function visibleText(xml) {
  const out = [];
  const re = /(?:text|content-desc)="([^"]*)"/g;
  let m;
  while ((m = re.exec(xml))) {
    const v = m[1].trim();
    if (v) out.push(v);
  }
  return out;
}

function findNodes(xml, needle) {
  const res = [];
  const re = /<node[^>]*>/g;
  let m;
  while ((m = re.exec(xml))) {
    const tag = m[0];
    const t = (tag.match(/text="([^"]*)"/) || [])[1] || "";
    const d = (tag.match(/content-desc="([^"]*)"/) || [])[1] || "";
    if (t.includes(needle) || d.includes(needle)) {
      const b = (tag.match(/bounds="\[(\d+),(\d+)\]\[(\d+),(\d+)\]"/) || []);
      if (b.length === 5) {
        const x = Math.round((+b[1] + +b[3]) / 2);
        const y = Math.round((+b[2] + +b[4]) / 2);
        res.push({ text: t, desc: d, cx: x, cy: y, bounds: b.slice(1).join(",") });
      }
    }
  }
  return res;
}

const [, , cmd, ...rest] = process.argv;

(async () => {
  try {
    if (cmd === "shot") {
      const base = capture(rest[0] || `shot-${stamp()}`);
      console.log(base);
    } else if (cmd === "tap") {
      const x = +rest[0], y = +rest[1];
      adb(["shell", "input", "touchscreen", "tap", String(x), String(y)]);
      await wait(+(rest[3] || 1400));
      const base = capture(rest[2] || `tap-${x}x${y}`);
      console.log(base);
    } else if (cmd === "tapnode") {
      // tapnode "<needle>" [tag] — locate by text/desc then tap its centre
      const needle = rest[0];
      adb(["shell", "uiautomator", "dump", "/sdcard/_ui.xml"]);
      const xml = adb(["shell", "cat", "/sdcard/_ui.xml"]);
      const hits = findNodes(xml, needle);
      if (!hits.length) {
        console.log(`NO_MATCH ${needle}`);
        console.log("VISIBLE:", JSON.stringify(visibleText(xml).slice(0, 60)));
        process.exit(2);
      }
      const h = hits[0];
      adb(["shell", "input", "touchscreen", "tap", String(h.cx), String(h.cy)]);
      await wait(1500);
      const base = capture(rest[1] || `tapnode-${needle}`);
      console.log(JSON.stringify({ tapped: h, base }));
    } else if (cmd === "type") {
      const s = rest.join(" ");
      adb(["shell", "input", "text", s.replace(/ /g, "%s")]);
      await wait(600);
      console.log(capture("typed"));
    } else if (cmd === "key") {
      adb(["shell", "input", "keyevent", rest[0]]);
      await wait(900);
      console.log(capture("key"));
    } else if (cmd === "dump") {
      adb(["shell", "uiautomator", "dump", "/sdcard/_ui.xml"]);
      const xml = adb(["shell", "cat", "/sdcard/_ui.xml"]);
      console.log(JSON.stringify(visibleText(xml), null, 1));
    } else if (cmd === "nodes") {
      // nodes — list every node with a text/desc, with a stable index + tap centre.
      // Avoids passing non-ASCII needles through the shell.
      adb(["shell", "uiautomator", "dump", "/sdcard/_ui.xml"]);
      const xml = adb(["shell", "cat", "/sdcard/_ui.xml"]);
      const list = [];
      const re = /<node[^>]*>/g;
      let m, i = 0;
      while ((m = re.exec(xml))) {
        const tag = m[0];
        const t = (tag.match(/text="([^"]*)"/) || [])[1] || "";
        const d = (tag.match(/content-desc="([^"]*)"/) || [])[1] || "";
        const clickable = /clickable="true"/.test(tag);
        if (!t && !d && !clickable) continue;
        const b = (tag.match(/bounds="\[(\d+),(\d+)\]\[(\d+),(\d+)\]"/) || []);
        let cx = null, cy = null;
        if (b.length === 5) {
          cx = Math.round((+b[1] + +b[3]) / 2);
          cy = Math.round((+b[2] + +b[4]) / 2);
        }
        list.push({ i: i++, text: t, desc: d, clickable, cx, cy });
      }
      console.log(JSON.stringify(list, null, 1));
    } else if (cmd === "tapidx") {
      // tapidx <index> [tag] [settleMs]
      const idx = +rest[0];
      adb(["shell", "uiautomator", "dump", "/sdcard/_ui.xml"]);
      const xml = adb(["shell", "cat", "/sdcard/_ui.xml"]);
      const list = [];
      const re = /<node[^>]*>/g;
      let m, i = 0;
      while ((m = re.exec(xml))) {
        const tag = m[0];
        const t = (tag.match(/text="([^"]*)"/) || [])[1] || "";
        const d = (tag.match(/content-desc="([^"]*)"/) || [])[1] || "";
        const clickable = /clickable="true"/.test(tag);
        if (!t && !d && !clickable) continue;
        const b = (tag.match(/bounds="\[(\d+),(\d+)\]\[(\d+),(\d+)\]"/) || []);
        let cx = null, cy = null;
        if (b.length === 5) {
          cx = Math.round((+b[1] + +b[3]) / 2);
          cy = Math.round((+b[2] + +b[4]) / 2);
        }
        list.push({ i: i++, text: t, desc: d, clickable, cx, cy });
      }
      const hit = list[idx];
      if (!hit || hit.cx === null) {
        console.log("NO_SUCH_INDEX", idx, "len=", list.length);
        process.exit(2);
      }
      adb(["shell", "input", "touchscreen", "tap", String(hit.cx), String(hit.cy)]);
      await wait(+(rest[2] || 1600));
      const base = capture(rest[1] || `tapidx-${idx}`);
      console.log(JSON.stringify({ tapped: hit, base }));
    } else if (cmd === "field") {
      // field <index> — tap a node then type text; text passed as-is (ASCII safe)
      const idx = +rest[0];
      const value = rest.slice(1).join(" ");
      adb(["shell", "uiautomator", "dump", "/sdcard/_ui.xml"]);
      const xml = adb(["shell", "cat", "/sdcard/_ui.xml"]);
      const nodes = [...xml.matchAll(/<node[^>]*>/g)].map((m) => m[0]);
      const hit = nodes[idx];
      if (!hit) { console.log("NO_SUCH_INDEX", idx); process.exit(2); }
      const b = (hit.match(/bounds="\[(\d+),(\d+)\]\[(\d+),(\d+)\]"/) || []);
      if (b.length !== 5) { console.log("NO_BOUNDS", hit); process.exit(2); }
      const cx = Math.round((+b[1] + +b[3]) / 2);
      const cy = Math.round((+b[2] + +b[4]) / 2);
      adb(["shell", "input", "touchscreen", "tap", String(cx), String(cy)]);
      await wait(700);
      adb(["shell", "input", "text", value.replace(/ /g, "%s")]);
      await wait(500);
      console.log(capture(`field-${idx}`));
    } else if (cmd === "find") {
      adb(["shell", "uiautomator", "dump", "/sdcard/_ui.xml"]);

      const xml = adb(["shell", "cat", "/sdcard/_ui.xml"]);
      console.log(JSON.stringify(findNodes(xml, rest[0]), null, 1));
    } else if (cmd === "launch") {
      adb(["shell", "am", "start", "-n", ACT]);
      await wait(+(rest[0] || 4000));
      console.log(capture("launch"));
    } else if (cmd === "stop") {
      adb(["shell", "am", "force-stop", PKG]);
      console.log("stopped");
    } else if (cmd === "clear") {
      adb(["shell", "pm", "clear", PKG]);
      console.log("cleared");
    } else if (cmd === "ime") {
      // Soft-keyboard state. Must be false before tapping anything near the
      // bottom of the screen: the IME overlays that region and swallows taps,
      // and KEYCODE_ESCAPE does NOT close it (KEYCODE_BACK does).
      const out = adb(["shell", "dumpsys", "input_method"]);
      const m = out.match(/mInputShown=(\w+)/);
      console.log("mInputShown=" + (m ? m[1] : "unknown"));
    } else if (cmd === "logcat") {
      adb(["logcat", "-c"]);
      adb(["shell", "am", "start", "-n", ACT]);
      await wait(9000);
      const txt = adb(["logcat", "-d", "-v", "time"]);
      const p = path.join(EVIDENCE, `${rest[0] || "logcat"}.txt`);
      writeFileSync(p, txt, "utf8");
      const errs = txt.split("\n").filter((l) => /chromium.*(Uncaught|TypeError|ReferenceError)|FATAL|AndroidRuntime/i.test(l));
      console.log(`wrote ${p} (${txt.length} bytes)`);
      console.log("SUSPECTS:", errs.length);
      console.log(errs.slice(0, 20).join("\n"));
    } else {
      console.log("unknown cmd");
      process.exit(1);
    }
  } catch (e) {
    console.log("ERROR:", e.message || e);
    process.exit(1);
  }
})();
