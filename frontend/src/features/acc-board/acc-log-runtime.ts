/**
 * Process-owned ACC log streams, following aiStreamRuntime's subscribe/replay model.
 * A view detaches; only terminal/cancel/revocation, network failure or idle GC closes IO.
 * Credentials and cursors are captured per authority + workspace + run, never re-read
 * by an in-flight request. Nothing is persisted or shared across authorities.
 */
import { ACC_TIMEOUT_MS, normalizeOrchestrationEvent, type AccOrchestrationEvent } from '../../api/accApi.ts'
import { fetchWithTimeout } from '../../api/fetch-timeout.ts'

export interface AccLogAuthority {
  session: object
  workspaceId: string
  base: string
  token: string
  assertCurrent(): void
}

type Source = 'live' | 'mock'
type Reason = 'terminal' | 'cancelled' | 'invalidated' | 'idle' | 'unavailable'
export interface AccLogSnapshot {
  events: readonly AccOrchestrationEvent[]
  lastSeq: number
  mode: 'connecting' | 'sse' | 'poll' | 'mock' | 'ended' | 'unavailable'
  notice: string
  reason?: Reason
}

interface Options {
  maxRuns: number
  maxEvents: number
  maxReplayChars: number
  maxFrameChars: number
  maxPollBytes: number
  detachGraceMs: number
  networkIdleMs: number
  authorityCheckMs: number
  pollMs: number
  mock?: {
    start(runId: string): void
    stop(runId: string): void
    read(runId: string, after: number): Promise<{ events: AccOrchestrationEvent[] }>
  }
}

type Listener = (snapshot: AccLogSnapshot) => void
type Timer = ReturnType<typeof setTimeout>
interface Entry {
  key: string
  runId: string
  source: Source
  owner: AccLogAuthority
  events: AccOrchestrationEvent[]
  replayChars: number
  lastSeq: number
  mode: AccLogSnapshot['mode']
  notice: string
  reason?: Reason
  subs: Set<Listener>
  ctrl?: AbortController
  reader?: ReadableStreamDefaultReader<Uint8Array>
  pollTimer?: Timer
  networkTimer?: Timer
  idleTimer?: Timer
  checkTimer?: ReturnType<typeof setInterval>
  pollFails: number
}

export class AccLogRuntime {
  private authority: AccLogAuthority | null = null
  private authorityListeners = new Set<(owner: object | null) => void>()
  private entries = new Map<string, Entry>()
  private options: Options

  constructor(options: Partial<Options> = {}) {
    this.options = {
      maxRuns: 8, maxEvents: 500, maxReplayChars: 256 * 1024,
      maxFrameChars: 64 * 1024, maxPollBytes: 1024 * 1024,
      detachGraceMs: 30_000, networkIdleMs: 45_000,
      authorityCheckMs: 1000, pollMs: 3000, ...options,
    }
  }

  captureAuthority(): object | null { return this.authority }

  /** Includes revocation originating inside transport/watchdog paths, not just settings. */
  onAuthorityChange(listener: (owner: object | null) => void): () => void {
    this.authorityListeners.add(listener)
    listener(this.authority)
    return () => { this.authorityListeners.delete(listener) }
  }

  isCurrentAuthority(owner: object | null): boolean {
    if (!owner || owner !== this.authority) return false
    try { this.authority!.assertCurrent(); return true }
    catch { this.setAuthority(null); return false }
  }

  setAuthority(next: AccLogAuthority | null): void {
    const old = this.authority
    if (old === next || (old && next && old.session === next.session &&
      old.workspaceId === next.workspaceId && old.base === next.base && old.token === next.token)) return
    const stale = [...this.entries.values()]
    this.entries.clear()
    this.authority = next
    for (const entry of stale) {
      clearTimeout(entry.idleTimer)
      this.close(entry, 'invalidated')
      entry.events = []
      entry.lastSeq = entry.replayChars = 0
      this.notify(entry, this.snapshot(entry)) // Clear visible private rows synchronously.
      entry.subs.clear()
    }
    const current = this.authority
    for (const listener of this.authorityListeners) {
      if (this.authority !== current) break // A listener may synchronously revoke again.
      try { listener(current) } catch (error) {
        console.warn('[acc-log-runtime] authority observer failed', error)
      }
    }
  }

  subscribe(runId: string, listener: Listener, source: Source = 'live'): () => void {
    const owner = this.authority
    if (!owner || !runId) {
      listener({ events: [], lastSeq: 0, mode: 'unavailable', reason: 'invalidated',
        notice: '请在当前工作区完成登录后查看日志' })
      return () => {}
    }
    const key = JSON.stringify([source, runId])
    let entry = this.entries.get(key)
    if (!entry) {
      // Bound the whole process as well as each run. Never evict an attached view.
      if (this.entries.size >= this.options.maxRuns) {
        const idle = [...this.entries.values()].find(value => value.subs.size === 0)
        if (idle) this.evict(idle)
        else {
          listener({ events: [], lastSeq: 0, mode: 'unavailable', reason: 'unavailable',
            notice: '同时查看的日志过多，请关闭一个后重试' })
          return () => {}
        }
      }
      entry = { key, runId, source, owner, events: [], replayChars: 0, lastSeq: 0,
        mode: source === 'mock' ? 'mock' : 'connecting', notice: '', subs: new Set(), pollFails: 0 }
      this.entries.set(key, entry)
      const current = entry
      entry.checkTimer = setInterval(() => { this.admitted(current) }, this.options.authorityCheckMs)
    }
    if (!this.admitted(entry)) return this.subscribe(runId, listener, source)
    clearTimeout(entry.idleTimer)
    entry.idleTimer = undefined
    entry.subs.add(listener)
    this.notifyOne(listener, this.snapshot(entry))
    if (entry.subs.size === 1 && !entry.ctrl && !entry.pollTimer && !entry.reason) {
      if (source === 'mock') this.startMock(entry)
      else void this.stream(entry)
    }
    const subscribed = entry
    return () => {
      subscribed.subs.delete(listener)
      if (subscribed.subs.size === 0 && this.entries.get(key) === subscribed && !subscribed.idleTimer) {
        // Traffic/heartbeats do not extend this deadline. Re-entry during grace reuses IO.
        subscribed.idleTimer = setTimeout(() => this.evict(subscribed), this.options.detachGraceMs)
      }
    }
  }

  /** Call only after the user's cancel request succeeds; capture owner before awaiting it. */
  cancel(runId: string, owner: object | null): boolean {
    if (!owner || owner !== this.authority) return false
    let cancelled = false
    for (const entry of this.entries.values()) {
      if (entry.runId === runId && !entry.reason && this.admitted(entry)) {
        this.close(entry, 'cancelled')
        this.publish(entry)
        cancelled = true
      }
    }
    return cancelled
  }

  dispose(): void { this.setAuthority(null) }

  private admitted(entry: Entry): boolean {
    return this.entries.get(entry.key) === entry && this.isCurrentAuthority(entry.owner)
  }

  private snapshot(entry: Entry): AccLogSnapshot {
    return { events: entry.events.slice(), lastSeq: entry.lastSeq, mode: entry.mode,
      notice: entry.notice, reason: entry.reason }
  }

  private publish(entry: Entry): void {
    if (!this.admitted(entry)) return
    const snapshot = this.snapshot(entry)
    for (const sub of entry.subs) {
      if (!this.admitted(entry)) break
      this.notifyOne(sub, snapshot)
    }
  }

  private notify(entry: Entry, snapshot: AccLogSnapshot): void {
    for (const sub of entry.subs) this.notifyOne(sub, snapshot)
  }

  private notifyOne(listener: Listener, snapshot: AccLogSnapshot): void {
    try { listener(snapshot) } catch (error) {
      console.warn('[acc-log-runtime] subscriber failed', error)
    }
  }

  private closeTransport(entry: Entry): void {
    clearTimeout(entry.networkTimer)
    entry.networkTimer = undefined
    const ctrl = entry.ctrl
    const reader = entry.reader
    entry.ctrl = entry.reader = undefined
    ctrl?.abort()
    // fetchWithTimeout releases its signal listener after headers. Cancel the owned
    // reader too: aborting only the controller would leave a pending read/socket alive.
    if (reader) void this.releaseBody(reader)
  }

  private async releaseBody(body: Pick<ReadableStream<Uint8Array>, 'cancel'>): Promise<void> {
    try { await body.cancel() } catch (error) {
      console.warn('[acc-log-runtime] closed body cleanup failed', error)
    }
  }

  private close(entry: Entry, reason: Reason): void {
    entry.reason = reason
    entry.mode = reason === 'unavailable' || reason === 'invalidated' ? 'unavailable' : 'ended'
    entry.notice = reason === 'cancelled' ? '已请求终止' : ''
    clearTimeout(entry.pollTimer)
    clearInterval(entry.checkTimer)
    entry.pollTimer = entry.checkTimer = undefined
    this.closeTransport(entry)
    if (entry.source === 'mock') this.options.mock?.stop(entry.runId)
  }

  private evict(entry: Entry): void {
    if (this.entries.get(entry.key) !== entry) return
    this.close(entry, 'idle')
    clearTimeout(entry.idleTimer)
    entry.events = []
    entry.lastSeq = entry.replayChars = 0
    entry.subs.clear()
    this.entries.delete(entry.key)
  }

  private armNetworkBudget(entry: Entry, ctrl: AbortController): void {
    clearTimeout(entry.networkTimer)
    entry.networkTimer = setTimeout(() => {
      if (!this.admitted(entry) || entry.ctrl !== ctrl || entry.reason) return
      if (entry.mode === 'poll') {
        this.closeTransport(entry)
        this.pollFailed(entry)
      } else this.startPolling(entry, '实时流暂时中断，已切换为轮询')
    }, entry.mode === 'poll' ? Math.min(this.options.networkIdleMs, ACC_TIMEOUT_MS) : this.options.networkIdleMs)
  }

  private url(entry: Entry, stream: boolean): string {
    const after = entry.lastSeq > 0 ? `?after=${entry.lastSeq}` : ''
    return `${entry.owner.base}/api/v2/orchestration/runs/${encodeURIComponent(entry.runId)}/events${stream ? '/stream' : ''}${after}`
  }

  private headers(entry: Entry, accept: string): Record<string, string> {
    return { Accept: accept, ...(entry.owner.token ? { Authorization: `Bearer ${entry.owner.token}` } : {}) }
  }

  private async stream(entry: Entry): Promise<void> {
    const ctrl = new AbortController()
    entry.ctrl = ctrl
    let reader: ReadableStreamDefaultReader<Uint8Array> | undefined
    try {
      this.armNetworkBudget(entry, ctrl)
      const response = await fetchWithTimeout(this.url(entry, true), {
        headers: this.headers(entry, 'text/event-stream'), signal: ctrl.signal,
      }, { timeoutMs: ACC_TIMEOUT_MS, label: 'acc-run-events-stream' })
      if (!this.admitted(entry) || entry.ctrl !== ctrl || entry.reason) {
        if (response.body) await this.releaseBody(response.body)
        return
      }
      if (response.status === 401 || response.status === 403) {
        if (this.admitted(entry) && entry.ctrl === ctrl) this.setAuthority(null)
        if (response.body) await this.releaseBody(response.body)
        return
      }
      if (!response.ok || !response.body || !response.headers.get('content-type')?.includes('text/event-stream')) {
        if (response.body) await this.releaseBody(response.body)
        this.startPolling(entry, '实时流不可用，已切换为轮询')
        return
      }
      reader = response.body.getReader()
      entry.reader = reader
      entry.mode = 'sse'
      entry.notice = ''
      this.publish(entry)
      const decoder = new TextDecoder()
      let line = '', frame = '', chars = 0, skipLf = false
      for (;;) {
        const chunk = await reader.read()
        if (!this.admitted(entry) || entry.ctrl !== ctrl || entry.reason) return
        if (chunk.done) break
        this.armNetworkBudget(entry, ctrl)
        // Parse incrementally, including CRLF split across chunks. Never accumulate
        // an unbounded unterminated frame, even if a server sends a huge chunk.
        for (const char of decoder.decode(chunk.value, { stream: true })) {
          if (skipLf && char === '\n') { skipLf = false; continue }
          skipLf = char === '\r'
          if (char === '\r' || char === '\n') {
            if (!line) {
              this.frame(entry, frame)
              frame = ''; chars = 0
            } else { frame += `${line}\n`; line = '' }
          } else { line += char }
          chars += char.length
          if (chars > this.options.maxFrameChars) throw new Error('ACC event frame exceeds buffer budget')
          if (entry.owner !== this.authority || entry.ctrl !== ctrl || entry.reason) return
        }
      }
      this.startPolling(entry, '实时流已结束，切换为轮询以补齐剩余事件')
    } catch (error) {
      if (!this.admitted(entry) || entry.ctrl !== ctrl || entry.reason) return
      console.warn('[acc-log-runtime] stream interrupted', error)
      this.startPolling(entry, '实时流暂时中断，已切换为轮询')
    } finally {
      if (reader) reader.releaseLock()
    }
  }

  private frame(entry: Entry, frame: string): void {
    let id = '', eventType = ''
    const data: string[] = []
    for (const line of frame.split('\n')) {
      const colon = line.indexOf(':')
      if (colon <= 0) continue
      const field = line.slice(0, colon)
      const value = line.slice(colon + 1).replace(/^ /, '')
      if (field === 'id') id = value
      else if (field === 'event') eventType = value
      else if (field === 'data') data.push(value)
    }
    if (!data.length) return
    let raw: unknown
    try { raw = JSON.parse(data.join('\n')) } catch {
      console.warn('[acc-log-runtime] ignored malformed event frame')
      return
    }
    if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return
    const event = normalizeOrchestrationEvent(raw as Record<string, unknown>)
    if (!event.eventType) event.eventType = eventType || 'message'
    if (id && Number.isSafeInteger(Number(id)) && Number(id) > 0) event.seq = Number(id)
    this.accept(entry, event)
  }

  private accept(entry: Entry, event: AccOrchestrationEvent): void {
    if (!this.admitted(entry) || entry.reason || (event.runId && event.runId !== entry.runId)) return
    if (event.eventType === 'projection.gap') {
      // A gap notification is not a delivered event/cursor. Replay from the last
      // actual event using /events; advancing to the synthetic id would lose data.
      if (entry.mode === 'sse') this.startPolling(entry, '事件流出现缺口，正在按序号补齐')
      return
    }
    if ((!event.eventType && !event.eventId) || !Number.isSafeInteger(event.seq) || event.seq <= entry.lastSeq) return
    if (event.eventId && entry.events.some(value => value.eventId === event.eventId)) {
      entry.lastSeq = event.seq
      return
    }
    const chars = JSON.stringify(event).length
    if (chars > this.options.maxReplayChars) {
      this.close(entry, 'unavailable')
      entry.notice = '单条事件超过日志缓冲上限'
      this.publish(entry)
      return
    }
    entry.lastSeq = event.seq
    entry.events.push(event)
    entry.replayChars += chars
    while (entry.events.length > this.options.maxEvents || entry.replayChars > this.options.maxReplayChars) {
      entry.replayChars -= JSON.stringify(entry.events.shift()!).length
    }
    if (event.eventType === 'run.terminal') this.close(entry, 'terminal')
    this.publish(entry)
  }

  private startPolling(entry: Entry, notice: string): void {
    if (!this.admitted(entry) || entry.reason) return
    this.closeTransport(entry)
    entry.mode = 'poll'
    entry.notice = notice
    this.publish(entry)
    void this.poll(entry)
  }

  private startMock(entry: Entry): void {
    try {
      if (!this.options.mock) throw new Error('ACC demo source is unavailable')
      this.options.mock.start(entry.runId)
      void this.poll(entry)
    } catch (error) {
      console.warn('[acc-log-runtime] demo source failed', error)
      this.close(entry, 'unavailable')
      this.publish(entry)
    }
  }

  private async poll(entry: Entry): Promise<void> {
    if (!this.admitted(entry) || entry.reason) return
    const ctrl = new AbortController()
    entry.ctrl = ctrl
    let reader: ReadableStreamDefaultReader<Uint8Array> | undefined
    try {
      let events: AccOrchestrationEvent[]
      if (entry.source === 'mock') {
        events = (await this.options.mock!.read(entry.runId, entry.lastSeq)).events
      } else {
        this.armNetworkBudget(entry, ctrl)
        const response = await fetchWithTimeout(this.url(entry, false), {
          headers: this.headers(entry, 'application/json'), signal: ctrl.signal,
        }, { timeoutMs: ACC_TIMEOUT_MS, label: 'acc-run-events-poll' })
        if (!this.admitted(entry) || entry.ctrl !== ctrl || entry.reason) {
          if (response.body) await this.releaseBody(response.body)
          return
        }
        if (!response.ok || !response.body) {
          if (response.status === 401 || response.status === 403) {
            if (this.admitted(entry) && entry.ctrl === ctrl) this.setAuthority(null)
          }
          else if ([400, 404, 405].includes(response.status)) {
            this.close(entry, 'unavailable'); this.publish(entry)
          } else {
            if (response.body) await this.releaseBody(response.body)
            throw new Error(`ACC events HTTP ${response.status}`)
          }
          if (response.body) await this.releaseBody(response.body)
          return
        }
        reader = response.body.getReader()
        entry.reader = reader
        const decoder = new TextDecoder()
        let text = '', bytes = 0
        for (;;) {
          const chunk = await reader.read()
          if (!this.admitted(entry) || entry.ctrl !== ctrl || entry.reason) return
          if (chunk.done) break
          bytes += chunk.value.byteLength
          if (bytes > this.options.maxPollBytes) throw new Error('ACC events exceed response budget')
          text += decoder.decode(chunk.value, { stream: true })
          // Polls have a whole-response deadline, not a renewable byte-idle budget.
        }
        text += decoder.decode()
        const raw = JSON.parse(text)
        const rows = Array.isArray(raw) ? raw : raw?.events ?? raw?.data ?? raw?.items ?? []
        if (!Array.isArray(rows)) throw new Error('Invalid ACC events response')
        events = rows.filter(value => value && typeof value === 'object').map(normalizeOrchestrationEvent)
      }
      if (!this.admitted(entry) || entry.ctrl !== ctrl || entry.reason) return
      entry.pollFails = 0
      for (const event of events.sort((a, b) => a.seq - b.seq)) this.accept(entry, event)
      if (entry.reason || !this.admitted(entry)) return
      entry.notice = ''
      this.closeTransport(entry)
      this.publish(entry)
      entry.pollTimer = setTimeout(() => { entry.pollTimer = undefined; void this.poll(entry) }, this.options.pollMs)
    } catch (error) {
      if (!this.admitted(entry) || entry.ctrl !== ctrl || entry.reason) return
      console.warn('[acc-log-runtime] event poll failed', error)
      this.closeTransport(entry)
      this.pollFailed(entry)
    } finally {
      if (reader) reader.releaseLock()
    }
  }

  private pollFailed(entry: Entry): void {
    entry.pollFails++
    if (entry.pollFails >= 3) this.close(entry, 'unavailable')
    else {
      entry.notice = '轮询暂时失败，稍后重试'
      entry.pollTimer = setTimeout(() => { entry.pollTimer = undefined; void this.poll(entry) }, this.options.pollMs)
    }
    this.publish(entry)
  }
}
