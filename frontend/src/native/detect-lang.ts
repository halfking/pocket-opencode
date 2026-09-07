export function detectLang(text: string): string {
  const cjk = (text.match(/[\u4e00-\u9fff\u3400-\u4dbf]/g) || []).length
  const latin = (text.match(/[a-zA-Z]/g) || []).length
  if (cjk >= 2 && latin >= 4) return 'mixed'
  if (cjk > latin) return 'zh'
  if (latin > 0) return 'en'
  return 'zh'
}

/** 听见式中英互译：中→英，英→中，混合保留对照。 */
export function translateTargetLang(sourceLang: string): 'zh' | 'en' {
  if (sourceLang === 'en') return 'zh'
  return 'en'
}
