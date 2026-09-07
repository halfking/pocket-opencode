import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  applyCaptionResult, createLiveCaption, pickSpeechRecognition, type SpeechRecLike,
} from './meeting-live-caption.ts'

function fakeRec(): SpeechRecLike & { started: number; stopped: number } {
  return {
    continuous: false,
    interimResults: false,
    lang: '',
    onresult: null,
    onerror: null,
    onend: null,
    started: 0,
    stopped: 0,
    start() { this.started += 1 },
    stop() { this.stopped += 1 },
  }
}

describe('meeting-live-caption', () => {
  it('picks webkit SpeechRecognition when standard is missing', () => {
    assert.equal(pickSpeechRecognition(null), null)
    const Ctor = function Webkit() {} as unknown as new () => SpeechRecLike
    assert.equal(pickSpeechRecognition({ webkitSpeechRecognition: Ctor }), Ctor)
  })

  it('commits finals and keeps interim on the live line', () => {
    const seen: string[] = []
    let interim = ''
    applyCaptionResult({ text: '  今天评审  ', isFinal: false }, {
      setInterim: (t) => { interim = t },
      commit: (t) => { seen.push(t) },
    })
    assert.equal(interim, '今天评审')
    assert.deepEqual(seen, [])
    applyCaptionResult({ text: '今天评审预算', isFinal: true }, {
      setInterim: (t) => { interim = t },
      commit: (t) => { seen.push(t) },
    })
    assert.equal(interim, '')
    assert.deepEqual(seen, ['今天评审预算'])
  })

  it('restarts recognition after an unexpected end while wanted', () => {
    const rec = fakeRec()
    const caption = createLiveCaption({
      recognition: rec,
      onResult: () => {},
    })
    assert.equal(caption.start(), true)
    assert.equal(rec.started, 1)
    rec.onend?.()
    assert.equal(rec.started, 2)
    caption.stop()
    rec.onend?.()
    assert.equal(rec.started, 2)
    assert.equal(rec.stopped, 1)
  })
})
