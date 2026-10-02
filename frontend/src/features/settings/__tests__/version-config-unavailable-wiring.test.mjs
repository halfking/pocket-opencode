// version-config-unavailable-wiring.test.mjs
//
// 锁住「版本配置缺失」这条**端到端接线**：Go 端翻成 503 + error 码，
// App 端认这个码并说出一条**指向真正原因**的话。
//
// 为什么这道护栏不能省：这次的缺陷从来不是「某一行写错了」，而是
// **两端各自都合理，合起来骗人**。loadVersionConfig 找不到配置文件时
// 静默回落 1.2.0（只打一行 Warning），而 App 侧拿到 200 就会照着这个
// 假版本号回答「你已是最新」。改一端不够：
//   · 只改 Go 端 → App 收到 503，弹「检查更新失败，请稍后重试」，
//     而重试一万次也是同样结果，用户会一直等一个不会来的重试；
//   · 只改 App 端 → 没有码可认，分支永远进不去。
//
// 判据指向的是**接线**（谁产出码、谁消费码），不是某个纯函数。
// 只测 loadVersionConfig 会漏掉「码两端对不上」这一类。
//
// 负控在本文件末尾：把任一端改回旧形态，判据必须转红。

import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.join(HERE, '..', '..', '..')
const VERSION_TS = path.join(SRC, 'utils', 'version.ts')
const SETTINGS_VUE = path.join(SRC, 'features', 'settings', 'SettingsView.vue')
const SERVER_GO = path.join(SRC, '..', '..', 'backend', 'internal', 'server', 'server.go')

const versionTs = fs.readFileSync(VERSION_TS, 'utf8')
const settingsVue = fs.readFileSync(SETTINGS_VUE, 'utf8')
const serverGo = fs.readFileSync(SERVER_GO, 'utf8')

/** 取 `function <name>(...) { … }` 的函数体（大括号配平），null = 没找到。 */
export function fnBody(src, decl) {
  const start = src.indexOf(decl)
  if (start < 0) return null
  const open = src.indexOf('{', start)
  if (open < 0) return null
  let depth = 0
  for (let i = open; i < src.length; i++) {
    if (src[i] === '{') depth++
    else if (src[i] === '}') {
      depth--
      if (depth === 0) return src.slice(open, i + 1)
    }
  }
  return null
}

export const ERROR_CODE = 'version_config_not_found'

describe('服务端：缺配置必须冒泡成 503 + 可识别的 error 码', () => {
  it('loadVersionConfig 不再回落默认值', () => {
    const body = fnBody(serverGo, 'func (s *Server) loadVersionConfig() (*VersionInfo, error) {')
    assert.ok(body, '判据锚点失效：找不到 loadVersionConfig')
    assert.match(
      body,
      /return nil, fmt\.Errorf\("%w: tried %s \(last error: %v\)", ErrVersionConfigNotFound/,
      'loadVersionConfig 又变回落默认值了 —— App 会拿到一个与实际发版无关的假版本号',
    )
  })

  it('handleCheckUpdate 把 ErrVersionConfigNotFound 翻成 503（不是 500）', () => {
    const body = fnBody(serverGo, 'func (s *Server) handleCheckUpdate(w http.ResponseWriter, r *http.Request) {')
    assert.ok(body, '判据锚点失效：找不到 handleCheckUpdate')
    assert.match(
      body,
      /errors\.Is\(err, ErrVersionConfigNotFound\)/,
      '调用方不再区分「配置没配对」与「配置坏了」，两者会退化成同一个 500',
    )
    assert.match(
      body,
      /w\.WriteHeader\(http\.StatusServiceUnavailable\)/,
      '配置缺失应是 503：这是部署问题，不是服务端内部错误',
    )
  })

  it('503 的响应体带 error 码与试过的路径', () => {
    assert.match(
      serverGo,
      new RegExp(`"error":\\s+"${ERROR_CODE}"`),
      `503 响应体不带 error: ${ERROR_CODE} —— App 侧无从识别`,
    )
    assert.match(serverGo, /"detail":\s+err\.Error\(\)/, '响应体不回带路径，排查者仍然只能猜')
  })
})

describe('App 端：认出这个码，并说一条指向真正原因的话', () => {
  it('checkUpdate 把该码转成专用错误类，而不是通用失败', () => {
    const body = fnBody(versionTs, 'export async function checkUpdate()')
    assert.ok(body, '判据锚点失效：找不到 checkUpdate')
    assert.match(
      body,
      new RegExp(`code === VERSION_CONFIG_UNAVAILABLE`),
      'checkUpdate 不再按 error 码分流 —— 专用错误类永远抛不出来',
    )
    assert.match(
      body,
      /throw new VersionConfigUnavailableError\(/,
      '命中该码时没有抛专用错误',
    )
  })

  it('常量与 Go 端同字面量（两端码对不上是本护栏的主要目标）', () => {
    assert.match(
      versionTs,
      new RegExp(`VERSION_CONFIG_UNAVAILABLE = '${ERROR_CODE}'`),
      `前端常量与后端 error 码不一致：必须是 ${ERROR_CODE}`,
    )
  })

  it('SettingsView 消费该错误类，且用的是新文案 key', () => {
    const body = fnBody(settingsVue, 'async function checkForUpdates()')
    assert.ok(body, '判据锚点失效：找不到 checkForUpdates')
    assert.match(
      body,
      /error instanceof VersionConfigUnavailableError/,
      'SettingsView 不认这个错误类 —— 仍然弹「稍后重试」，而重试不会有用',
    )
    assert.match(
      body,
      /t\('settings\.versionConfigUnavailable'/,
      '没有用指向真正原因的文案 key',
    )
    // 通用失败分支必须还在：这条判据只针对 503 这一种，不许把别的也吞掉。
    assert.match(
      body,
      /t\('settings\.checkUpdateFailed'\)/,
      '通用失败分支被删了 —— 非 503 的失败将没有提示',
    )
  })

  it('9 份语言文件都有这个 key（i18n 卡口的补充，这里点名单这一条）', () => {
    const locDir = path.join(SRC, 'locales')
    const files = fs.readdirSync(locDir).filter((f) => f.endsWith('.json'))
    assert.ok(files.length >= 9, `语言文件只有 ${files.length} 份，判据前提不成立`)
    for (const f of files) {
      const obj = JSON.parse(fs.readFileSync(path.join(locDir, f), 'utf8'))
      assert.ok(
        obj.settings && typeof obj.settings.versionConfigUnavailable === 'string',
        `${f} 缺 settings.versionConfigUnavailable`,
      )
    }
  })
})

describe('判据自检：负控必须转红', () => {
  it('把 Go 端改回静默回落 → 服务端两条判据报出违规', () => {
    const mutated = serverGo.replace(
      /return nil, fmt\.Errorf\("%w: tried %s \(last error: %v\)", ErrVersionConfigNotFound, strings\.Join\(tried, ", "\), err\)/,
      'return &VersionInfo{Version: "1.2.0", BuildNumber: 2}, nil',
    )
    assert.notEqual(mutated, serverGo, '前提不成立：负控锚点没命中，负控会静默 no-op')

    const body = fnBody(mutated, 'func (s *Server) loadVersionConfig() (*VersionInfo, error) {')
    assert.doesNotMatch(
      body,
      /ErrVersionConfigNotFound/,
      '判据在注入回落之后仍然通过 —— 负控无效',
    )
  })

  it('把 App 端改回通用失败 → 消费点判据报出违规', () => {
    const mutated = versionTs.replace(/if \(code === VERSION_CONFIG_UNAVAILABLE\) \{/, 'if (false) {')
    assert.notEqual(mutated, versionTs, '前提不成立：负控锚点没命中')

    const body = fnBody(mutated, 'export async function checkUpdate()')
    assert.doesNotMatch(
      body,
      /code === VERSION_CONFIG_UNAVAILABLE/,
      '判据在取消分流之后仍然通过 —— 负控无效',
    )
  })
})
