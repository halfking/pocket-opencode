import fs from 'node:fs'; import path from 'node:path';
const DIR = path.join(process.cwd(), 'src', 'locales');
const flat = (o, p = '') => Object.entries(o).flatMap(([k, v]) =>
  v && typeof v === 'object' && !Array.isArray(v) ? flat(v, p ? `${p}.${k}` : k) : [p ? `${p}.${k}` : k]);
const base = JSON.parse(fs.readFileSync(path.join(DIR, 'en-US.json'), 'utf8'));
const bk = new Set(flat(base));
for (const f of fs.readdirSync(DIR).filter(n => n.endsWith('.json'))) {
  const j = JSON.parse(fs.readFileSync(path.join(DIR, f), 'utf8'));
  const miss = flat(j).filter(k => !bk.has(k));
  const lack = [...bk].filter(k => !new Set(flat(j)).has(k));
  console.log(`${f.padEnd(12)} total=${String(flat(j).length).padStart(4)}  缺基准key=${String(lack.length).padStart(3)}  多余key=${String(miss.length).padStart(3)}`);
}
console.log('en-US total keys =', bk.size);
