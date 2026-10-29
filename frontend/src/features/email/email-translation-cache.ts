import { captureEmailBodyContext, type BodyContext } from './email-body-cache'

function keyFor(id: string, context: BodyContext): string {
  return `pocket:email_translation:v2:${encodeURIComponent(context.key)}:${encodeURIComponent(id)}`
}

/** Unscoped historical translation keys are preserved but never read or claimed. */
export function restoreEmailTranslations(id: string, context = captureEmailBodyContext()): Record<string, string> {
  context.assertCurrent()
  if (!id || !context.key) return {}
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem(keyFor(id, context)) ?? '{}')
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {}
    return Object.fromEntries(Object.entries(parsed).filter(([, value]) => typeof value === 'string'))
  } catch { return {} }
}

export async function translateEmailForOwner(
  id: string, language: string, translate: () => Promise<string>, context: BodyContext,
): Promise<string> {
  context.assertCurrent()
  const text = await translate()
  // Validate outside the cache-error fallback: an expired response must not reach the UI.
  context.assertCurrent()
  if (id && context.key) {
    try {
      const merged = { ...restoreEmailTranslations(id, context), [language]: text }
      localStorage.setItem(keyFor(id, context), JSON.stringify(merged))
    } catch { /* Cache quota/storage failure only loses the optional saved translation. */ }
  }
  return text
}
