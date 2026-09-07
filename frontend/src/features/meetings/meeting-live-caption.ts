export interface CaptionResult {
  text: string
  isFinal: boolean
}

export interface SpeechRecLike {
  continuous: boolean
  interimResults: boolean
  lang: string
  onresult: ((ev: {
    resultIndex: number
    results: ArrayLike<{ isFinal: boolean; 0?: { transcript?: string } }>
  }) => void) | null
  onerror: ((ev: { error?: string }) => void) | null
  onend: (() => void) | null
  start: () => void
  stop: () => void
}

type RecCtor = new () => SpeechRecLike

export function pickSpeechRecognition(win: {
  SpeechRecognition?: RecCtor
  webkitSpeechRecognition?: RecCtor
} | null | undefined): RecCtor | null {
  if (!win) return null
  return win.SpeechRecognition || win.webkitSpeechRecognition || null
}

export function applyCaptionResult(
  result: CaptionResult,
  sink: { setInterim: (text: string) => void; commit: (text: string) => void },
): void {
  const text = result.text.replace(/\s+/g, ' ').trim()
  if (!text) {
    sink.setInterim('')
    return
  }
  if (result.isFinal) {
    sink.setInterim('')
    sink.commit(text)
    return
  }
  sink.setInterim(text)
}

export function createLiveCaption(opts: {
  recognition: SpeechRecLike
  lang?: string
  onResult: (result: CaptionResult) => void
  onError?: (message: string) => void
}): { start: () => boolean; stop: () => void } {
  const rec = opts.recognition
  let wanted = false
  rec.continuous = true
  rec.interimResults = true
  rec.lang = opts.lang || 'zh-CN'
  rec.onresult = (ev) => {
    for (let i = ev.resultIndex; i < ev.results.length; i++) {
      const row = ev.results[i]
      const text = String(row?.[0]?.transcript || '').trim()
      if (!text) continue
      opts.onResult({ text, isFinal: Boolean(row?.isFinal) })
    }
  }
  rec.onerror = (ev) => {
    if (ev.error === 'no-speech' || ev.error === 'aborted') return
    opts.onError?.(ev.error || 'caption-error')
  }
  rec.onend = () => {
    if (!wanted) return
    try { rec.start() } catch { /* already started */ }
  }
  return {
    start() {
      wanted = true
      try {
        rec.start()
        return true
      } catch {
        wanted = false
        return false
      }
    },
    stop() {
      wanted = false
      try { rec.stop() } catch { /* ok */ }
    },
  }
}
