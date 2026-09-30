#!/usr/bin/env node
/**
 * 补齐 settings.language（语言切换入口文案）。
 *
 * 背景：LanguageSwitcher 组件长期是死代码，应用内没有语言入口；
 * Android WebView 的 navigator.language / Intl 恒为 en-US，中文系统
 * (getprop persist.sys.locale=zh-CN) 上应用启动即英文且无处可改。
 * 现在把开关接到设置页，需要各语言包都有对应词条。
 */
import { readFileSync, writeFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const DIR = join(dirname(fileURLToPath(import.meta.url)), '..', 'src', 'locales')

const ADDITIONS = {
  'zh-CN': { language: '界面语言' },
  'zh-TW': { language: '介面語言' },
  'en-US': { language: 'Language' },
  'ja-JP': { language: '表示言語' },
  'ko-KR': { language: '인터페이스 언어' },
  'de-DE': { language: 'Sprache' },
  'fr-FR': { language: "Langue de l'interface" },
  'es-ES': { language: 'Idioma de la interfaz' },
  'pt-BR': { language: 'Idioma da interface' },
}

for (const [locale, add] of Object.entries(ADDITIONS)) {
  const file = join(DIR, `${locale}.json`)
  const raw = readFileSync(file, 'utf8')
  const eol = raw.includes('\r\n') ? '\r\n' : '\n'
  const data = JSON.parse(raw)
  data.settings = { ...data.settings, ...add }
  writeFileSync(file, JSON.stringify(data, null, 2).replace(/\n/g, eol) + eol, 'utf8')
  console.log(`${locale}: +${Object.keys(add).length}`)
}
console.log('done')
