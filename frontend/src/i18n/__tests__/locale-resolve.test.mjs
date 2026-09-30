/**
 * 语言解析回归测试。
 *
 * 缺陷背景（真机 Redmi 14R 5G / Android 14 实测）：
 *   - 设备真实语言 `getprop persist.sys.locale` = zh-CN；
 *   - 但 WebView 的 navigator.language / Intl.* 全部是 'en-US'，
 *     设备语言只出现在 navigator.languages 的后续项；
 *   - 旧实现只读 navigator.language，于是中文系统启动成英文；
 *   - 且 app_locale 从不参与初始语言计算，<html lang> 与 i18n runtime
 *     各写各的，出现「lang=zh-CN 但界面英文」的错配。
 *
 * Run: node --test src/i18n/__tests__/locale-resolve.test.mjs
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { matchLocale, resolveInitialLocale } from '../locale-resolve.ts'

describe('locale candidate matching', () => {
  it('精确匹配优先', () => {
    assert.equal(matchLocale(['zh-CN']), 'zh-CN')
    assert.equal(matchLocale(['ja-JP']), 'ja-JP')
  })

  it('按语言前缀回退到受支持的区域包', () => {
    assert.equal(matchLocale(['en-GB']), 'en-US')
    assert.equal(matchLocale(['zh-Hans-CN']), 'zh-CN')
    assert.equal(matchLocale(['ja']), 'ja-JP')
  })

  it('按候选顺序取第一个受支持项', () => {
    assert.equal(matchLocale(['en-US', 'zh-CN']), 'en-US')
    assert.equal(matchLocale(['xx-YY', 'ko-KR']), 'ko-KR')
  })

  it('无受支持项时返回 null', () => {
    assert.equal(matchLocale(['xx-YY', 'zz']), null)
    assert.equal(matchLocale([]), null)
  })
})

describe('initial locale resolution', () => {
  it('用户显式选择优先于设备语言', () => {
    assert.equal(
      resolveInitialLocale({ persisted: 'zh-CN', candidates: ['en-US', 'zh-CN'] }),
      'zh-CN',
    )
  })

  it('无显式选择时按设备候选解析', () => {
    assert.equal(resolveInitialLocale({ persisted: null, candidates: ['de-DE'] }), 'de-DE')
  })

  it('Android WebView 的 zh-CN 候选不再被丢弃', () => {
    // 旧实现只看 navigator.language='en-US'，这里的 zh-CN 会被忽略
    assert.equal(
      resolveInitialLocale({ persisted: null, candidates: ['en-US', 'zh-CN'] }),
      'en-US',
      'en-US 排在候选首位时仍应取 en-US',
    )
    assert.equal(
      resolveInitialLocale({ persisted: null, candidates: ['zh-CN', 'en-US'] }),
      'zh-CN',
    )
  })

  it('持久化值非法时降级到设备语言而不是卡死', () => {
    assert.equal(
      resolveInitialLocale({ persisted: 'kl-KL', candidates: ['fr-FR'] }),
      'fr-FR',
    )
  })

  it('既无持久化也无候选时兜底 en-US', () => {
    assert.equal(resolveInitialLocale({ persisted: null, candidates: [] }), 'en-US')
  })
})

describe('远端上报语言必须规范化', () => {
  // 缺陷：config-sync/prefs.ts 曾在无 app_locale 时上报裸语言码 'zh'。
  // 它既不是受支持 locale，也不是合法 BCP-47 标签，远端同步写回后
  // <html lang> 与 i18n runtime 会再次错配。
  it('裸语言码 zh 被规范化为 zh-CN 而不是原样落盘', () => {
    assert.equal(matchLocale(['zh']), 'zh-CN')
    assert.notEqual(matchLocale(['zh']), 'zh')
  })

  it('任何上报值都要么规范化成功、要么被完全忽略', () => {
    for (const raw of ['zh', 'en', 'ja', 'pt', 'zh-CN', 'en-GB', 'zh-Hant-TW']) {
      const hit = matchLocale([raw])
      assert.ok(hit === null || /^[a-z]{2}-[A-Z]{2}$/.test(hit), `${raw} → ${hit}`)
    }
  })

  it('完全无关的语言码被拒绝，不写入 <html lang>', () => {
    assert.equal(matchLocale(['xx', 'xx-YY', 'und', '']), null)
  })
})
