export type RecordingPhase = 'idle' | 'recording' | 'stopping'

export function formatRecordingClock(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000))
  const mm = String(Math.floor(total / 60)).padStart(2, '0')
  const ss = String(total % 60).padStart(2, '0')
  return `${mm}:${ss}`
}

export function nextRecordingState(
  phase: RecordingPhase,
  event: 'toggle' | 'drafted',
): RecordingPhase {
  if (event === 'drafted') return 'idle'
  if (phase === 'idle') return 'recording'
  if (phase === 'recording') return 'stopping'
  return 'stopping'
}

export function appendTranscript(
  committed: string,
  _display: string,
  partial: string,
  finalChunk = '',
): { committed: string; display: string } {
  const nextCommitted = finalChunk ? `${committed}${finalChunk}` : committed
  return {
    committed: nextCommitted,
    display: `${nextCommitted}${partial}`,
  }
}
