
## Flow A — Session Create → Prompt → SSE → Messages → Interrupt (2026-09-06T21:05:58Z)
Upstream: opencode serve 1.14.33 @ http://127.0.0.1:4096

### A1. local-opencode instance now has apiBaseURL
{
  "id": "local-opencode",
  "apiBaseURL": "http://localhost:4096",
  "health": "offline"
}

### A2. POST /api/mobile/sessions (create)
{"code":"not_found","error":"resource not found","request_id":"2f7b8ebb861ba6f63007181edb9e2ae0","retryable":false}

HTTP_STATUS:404

SID=null

## Flow A retry with workspace-scoped instance (2026-09-06T21:12:25Z)

### A2 create →
{"id":"ses_f876f4619fferVx2HIIX7m6OkR","projectID":"global","cost":0,"tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}},"time":{"created":1788729145830,"updated":1788729145830},"title":"New session - 2026-09-06T21:12:25.830Z","location":{"directory":""}}

200

SID=ses_f876f4619fferVx2HIIX7m6OkR

### A3. SSE stream: server.connected first frame
event: server.connected
data: {"sessionId":"ses_f876f4619fferVx2HIIX7m6OkR"}


### A4. POST prompt (text echo via upstream LLM-less opencode)
send prompt: opencode send prompt request failed: Post "http://localhost:4096/session/ses_f876f4619fferVx2HIIX7m6OkR/message": context deadline exceeded

502


### A5. SSE frames after prompt (event: lines)
event: server.connected
event: message.updated
event: message.part.updated
event: session.updated
event: session.status
event: message.updated
event: session.updated
event: session.diff
event: message.updated
event: session.status
event: message.part.updated
event: message.part.updated

### A6. messages
{
  "total": 2,
  "roles": [
    "user",
    "assistant"
  ],
  "first_text": "Reply with exactly: E2E-OK"
}

### A7. interrupt
HTTP 204

### A8. Idempotency replay (same key it-e2e-002)
HTTP 200
Idempotency-Replayed: true
ses_f876f4619fferVx2HIIX7m6OkR

### A9. session summary
{"messageCount":2,"sessionID":"ses_f876f4619fferVx2HIIX7m6OkR","summary":" (用户消息: 1, AI回复: 1)","title":""}


### A10. search q=flow
{
  "total": 0,
  "ids": []
}

### A11. audit trail after Flow A
{
  "total": 0,
  "actions": []
}

## Flow A prompt retry with mock LLM provider (2026-09-06T21:21:09Z)

### A4r. POST prompt (expect 200, assistant completes)
{"messageID":"","sessionID":"ses_f876f4619fferVx2HIIX7m6OkR"}

HTTP_STATUS:202


### assistant text from SSE
data: {"id":"","type":"message.part.updated","data":{"part":{"id":"prt_07898bca7001yM1CqXjgBMBbIn","messageID":"msg_07898bca60015TvqyOCSMoiRTG","sessionID":"ses_f876f4619fferVx2HIIX7m6OkR","text":"Reply with exactly: E2E-OK","type":"text"},"sessionID":"ses_f876f4619fferVx2HIIX7m6OkR","time":1788729670832}}

E2E-OK

### messages after completed run
{
  "total": 3,
  "msgs": [
    {
      "role": "user",
      "text": "Reply with exactly: E2E-OK"
    },
    {
      "role": "assistant",
      "text": "<think>\nThe user is asking me to reply with exactly \"E2E-OK\""
    },
    {
      "role": "user",
      "text": "Reply with exactly: E2E-OK"
    }
  ]
}
