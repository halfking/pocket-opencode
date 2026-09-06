
## Flow B/C negative contract + upstream data persistence (2026-09-06T21:21:49Z)

### B1. approvals list (empty pending)
{"permissions":[],"questions":[]}

HTTP_STATUS:200
### B2. permission reply on unknown rid (expect fail-closed 4xx)
{"code":"approval_expired","error":"approval request is no longer pending","request_id":"d89ae072a8efd6422e3a7a2ba5952ac9","retryable":false}

HTTP_STATUS:409
### B3. invalid decision (expect 400 invalid_decision)
{"code":"invalid_decision","error":"permission decision must be once, always, or reject","request_id":"d043523cc249ac8d37f2c65e305f9879","retryable":false}

HTTP_STATUS:400
### B4. question answer unknown rid
{"code":"approval_expired","error":"approval request is no longer pending","request_id":"aaa4b34eea54503e31621cad00ce03da","retryable":false}

HTTP_STATUS:409
### B5. question reject unknown rid
{"code":"approval_expired","error":"approval request is no longer pending","request_id":"aaa5759f933747f6c5c995d777a241a2","retryable":false}

HTTP_STATUS:409

### D1. upstream persistence: session exists in opencode directly
{
  "id": "ses_f876f4619fferVx2HIIX7m6OkR",
  "title": "New session - 2026-09-06T21:12:25.830Z"
}
### D2. upstream session messages count
3
