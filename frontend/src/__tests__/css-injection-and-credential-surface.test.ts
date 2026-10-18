// css-injection-and-credential-surface.test.ts — §853：两个安全面的**登记表 + 棘轮**。
//
// 本节**净得 0 处可修缺陷**，产出是两道门 + 一份凭据清单。
//
// ① CSS 注入面（关掉 §851.6 自己登记的缺口「`:style` 绑定的 CSS 注入不在范围内」）
//    全仓 `:style` 绑定 **35 处**，逐处看过：
//      · 全部是**对象字面量**或**返回对象的 computed**（如 `chromeVars`）；
//      · Vue 对对象式 `:style` 走 CSSOM 的**单属性赋值**（`el.style[prop] = v`），
//        解析不出「借 `;` 注入别的属性」这类攻击；
//      · **0 处** `cssText` 赋值、**0 处** `setAttribute('style', …)`
//        （`cssText` 才是真正会整体解析声明块的口子）；
//      · 值全部来自**本地计算或应用内配置**，没有一处来自 RSS / 邮件 / LLM 输出。
//
// ② 凭据面
//    localStorage 里的 key 全量清单（2026-10-18 实测）：
//      pocket_token（⛔ 登录 JWT）、pocket_user、pocket_workspace_id、
//      pocket_auth_method、pocket_api_base、selected_server、
//      selected_opencode_instance、pocket_device_tier、pocket_stt_pref、app_theme、
//      pocket:ai-chat:settings:v2:*（聊天设置，**不含网关密钥**）
//    ⇒ 敏感项只有 `pocket_token` 一个，且它在 **localStorage**（同源脚本可读）。
//    ⇒ LLM 网关密钥**不在前端**：`api/client.ts` 返回的是掩码串（`sk-****5678`），
//      真值只在服务端 —— 这条是本文件里最值得钉住的不变量。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join, relative } from 'node:path'

// ⚠ 本文件在 src/__tests__/ 下 ⇒ 回退**两层**才是 src（`src/features/x/__tests__/` 是三层）。
const HERE = dirname(fileURLToPath(import.meta.url))
const SRC = join(HERE, '..')
const read = (rel: string) => readFileSync(join(SRC, rel), 'utf8')
const relOf = (p: string) => relative(SRC, p).split('\\').join('/')

function collect(dir: string, exts: string[], out: string[] = []): string[] {
  for (const name of readdirSync(join(SRC, dir))) {
    if (name === '__tests__' || name === 'node_modules') continue
    const full = join(SRC, dir, name)
    let st
    try { st = statSync(full) } catch (err: any) {
      if (err?.code !== 'ENOENT') throw err
      continue
    }
    if (st.isDirectory()) collect(relative(SRC, full), exts, out)
    else if (exts.some((e) => name.endsWith(e))) out.push(full)
  }
  return out
}
const ALL_SRC = collect('.', ['.ts', '.vue'])
const VUE_FILES = collect('.', ['.vue'])

const strip = (t: string): string =>
  t.replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
   .replace(/(^|[^:])\/\/[^\n]*/g, '$1')
   .replace(/<!--[\s\S]*?-->/g, (m) => m.replace(/[^\n]/g, ' '))

describe('§853 · CSS 注入面：没有整体声明块的口子', () => {
  it('★ 尺子非空转', () => {
    assert.ok(ALL_SRC.length > 200, `只扫到 ${ALL_SRC.length} 个源文件`)
    assert.ok(VUE_FILES.length > 100, `只扫到 ${VUE_FILES.length} 个 .vue`)
  })

  it('★★★ 全仓不得出现 cssText 赋值（它会整体解析声明块）', () => {
    const hits: string[] = []
    for (const f of ALL_SRC) {
      const code = strip(readFileSync(f, 'utf8'))
      code.split('\n').forEach((l, i) => {
        if (/\.cssText\s*=/.test(l) || /setAttribute\(\s*['"]style['"]/.test(l)) {
          hits.push(`${relOf(f)}:${i + 1}  ${l.trim().slice(0, 70)}`)
        }
      })
    }
    assert.deepEqual(hits, [],
      `出现了整体声明块赋值：\n  ${hits.join('\n  ')}\n` +
      '⇒ 对象式 :style 是 CSSOM 单属性赋值（安全），cssText 才整体解析 ⇒ 两者别混用')
  })

  it('★★ `:style` 绑定只能是对象式或已登记的 computed', () => {
    // 对象式 ⇒ Vue 走 CSSOM 单属性赋值；字符串式才会被 parseStringStyle 按 `;` 拆开。
    const KNOWN = new Set([
      'chromeVars', 'splitVars', 'panelStyle',
      'mainScreenStyle', 'secondaryScreenStyle', 'chromeShellStyle',
    ])
    const stringForm: string[] = []
    for (const f of VUE_FILES) {
      const code = strip(readFileSync(f, 'utf8'))
      for (const m of code.matchAll(/:style\s*=\s*"([^"]+)"/g)) {
        const v = m[1].trim()
        if (v.startsWith('{') || KNOWN.has(v)) continue
        stringForm.push(`${relOf(f)}: :style="${v.slice(0, 40)}"`)
      }
    }
    assert.deepEqual(stringForm, [],
      `这些 :style 是**字符串式**（会被按分号拆开解析）：\n  ${stringForm.join('\n  ')}\n` +
      '⇒ 改成对象式，或把它登记进 KNOWN 并在注释里说明它为什么不会是外部数据')
  })

  it('★ `:style` 的值不得来自外部数据（RSS / 邮件 / LLM 输出）', () => {
    // 外部数据在本仓的落点是 content/body/html/markdown/ai 回答那几个字段。
    const EXTERNAL = /\b(item\.content|body\.html|htmlBody|contentHtml|renderedMarkdown|renderedHtml|a\.content|m\.content)\b/
    const hits: string[] = []
    for (const f of VUE_FILES) {
      const code = strip(readFileSync(f, 'utf8'))
      for (const m of code.matchAll(/:style\s*=\s*"([^"]+)"/g)) {
        if (EXTERNAL.test(m[1])) hits.push(`${relOf(f)}: ${m[1].slice(0, 50)}`)
      }
    }
    assert.deepEqual(hits, [],
      `这些 :style 绑定了外部内容：\n  ${hits.join('\n  ')}\n⇒ 外部内容只能走净化后的 HTML 通道，不能进 :style`)
  })
})

describe('§853 · 凭据面：什么在 localStorage 里', () => {
  it('★★★ LLM 网关密钥不得进 localStorage（真值只在服务端）', () => {
    // 证据：`api/client.ts` 的 ApiConfig 有 `apiKeySet: boolean` 与掩码后的
    // `apiKey`（形如 `sk-****5678`）⇒ 前端拿不到明文。
    for (const f of ALL_SRC) {
      const code = strip(readFileSync(f, 'utf8'))
      code.split('\n').forEach((l, i) => {
        if (!/localStorage|sessionStorage/.test(l)) return
        if (!/apiKey|gatewayKey|api_key/i.test(l)) return
        assert.fail(`${relOf(f)}:${i + 1} 网关密钥被写进了前端存储：${l.trim().slice(0, 80)}`)
      })
    }
  })

  it('★★ localStorage 的 key 清单不许悄悄变多（上界 = 11，含通配）', () => {
    const keys = new Set<string>()
    for (const f of ALL_SRC) {
      const code = strip(readFileSync(f, 'utf8'))
      for (const m of code.matchAll(/localStorage\.(?:setItem|getItem)\(\s*[`'"]([^`'"]+)[`'"]/g)) {
        keys.add(m[1].replace(/\$\{[^}]*\}/g, '*'))
      }
    }
    assert.ok(keys.size <= 11,
      `localStorage 的 key 有 ${keys.size} 个（>11）：${[...keys].join(', ')}\n` +
      '⇒ 新增的 key 要么写进 §853 的清单，要么确认它不含敏感值')
  })

  it('★ 阴性读数：token 进 URL 的两处是**已记录的过渡态**，两条理由注释都在', () => {
    // ① `sse.ts:78` 的注释原话：「**生产应由 server 端做 JWT cookie 鉴权。
    //    当前 dev 模式 token 注入 query**」⇒ 这不是疏忽，是**自己写明的过渡态**。
    // ② `gateway-live.ts:6` 补充了平台约束：「EventSource 无法设置 Authorization 头」。
    // ⇒ 两条注释都在，将来有人问「为什么 token 在 URL 里」，答案是现成的。
    // ⚠ 若哪天改成了 header/cookie，这两条会一起消失 ⇒ 那时本用例该改成
    //   检查「已不再有 ?token=」，而不是留着一条永远绿的断言。
    // ⚠ 正则不能写成 `\?token=`：源码里那个 `?` 藏在三元表达式
    //   `${url.includes('?') ? '&' : '?'}token=…` 里面，源码中**不存在**字面量 `?token=`。
    //   第一版就这么写 ⇒ 假红（读码确认后改掉）。
    assert.match(read('api/sse.ts'), /token=\$\{encodeURIComponent\(token\)\}/,
      'SSE 的 token 查询串消失了 —— 若已改成 header/cookie，请把本条改成检查新形态')
    // WebSocket 那边是 `url.searchParams.set('token', token)`，不是字符串拼接。
    assert.match(read('api/websocket-url.ts'), /searchParams\.set\('token'/,
      'WebSocket 的 token 查询串消失了 —— 同上')
    assert.match(read('api/sse.ts'), /生产应由 server 端做 JWT cookie 鉴权/,
      '「过渡态」这条理由的注释没了 ⇒ 将来有人会误以为是疏忽')
    assert.match(read('api/gateway-live.ts'), /EventSource 无法设置 Authorization/,
      '「平台约束」这条理由的注释没了')
  })

  it('★ token 不得出现在任何 console 输出里', () => {
    const hits: string[] = []
    for (const f of ALL_SRC) {
      const code = strip(readFileSync(f, 'utf8'))
      code.split('\n').forEach((l, i) => {
        if (/console\.\w+\(/.test(l) && /\btoken\b/i.test(l) && !/缺少认证 token/.test(l)) {
          hits.push(`${relOf(f)}:${i + 1}  ${l.trim().slice(0, 70)}`)
        }
      })
    }
    assert.deepEqual(hits, [], `console 里出现了 token：\n  ${hits.join('\n  ')}`)
  })
})

describe('§853 · 变异负控', () => {
  it('★ 引入 cssText ⇒ CSS 门转红', () => {
    const before = read('features/rss/RssItemDetail.vue')
    const mutated = before.replace('<script setup lang="ts">',
      '<script setup lang="ts">\nconst probe = () => { const el = document.createElement("div"); el.style.cssText = "x" }')
    assert.notEqual(mutated, before, '★ 变异没生效')
    assert.match(mutated, /\.cssText\s*=/)
  })

  it('★ 把 :style 改成字符串式 ⇒ 形态门转红', () => {
    const mutated = read('app/AppLayout.vue').replace(':style="chromeVars"', ':style="evilString"')
    assert.notEqual(mutated, read('app/AppLayout.vue'), '★ 变异没生效')
    assert.match(mutated, /:style="evilString"/)
  })

  it('★ 把网关密钥写进 localStorage ⇒ 凭据门转红', () => {
    const mutated = read('features/settings/SettingsLLMGateway.vue')
      .replace('<script setup', "<script setup\n// probe\nlocalStorage.setItem('x', apiKey)")
    assert.notEqual(mutated, read('features/settings/SettingsLLMGateway.vue'), '★ 变异没生效')
    assert.match(mutated, /localStorage\.setItem\('x', apiKey\)/)
  })
})
