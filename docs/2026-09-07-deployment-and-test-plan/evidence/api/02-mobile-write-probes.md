
## Mobile write-path probes (upstream :4096 down) — 2026-09-06T21:01:03Z

### 11. POST /api/mobile/sessions (create)
{"code":"not_found","error":"resource not found","request_id":"0c4e95086bb0c8a08c60c8c960963a25","retryable":false}

HTTP_STATUS:404

### 12. POST /api/mobile/sessions mismatched/unknown instance
{"code":"not_found","error":"resource not found","request_id":"1e5992c24db17a57f371a31dbc88689c","retryable":false}

HTTP_STATUS:404

### 13. SSE headers probe (3s window)
HTTP/1.1 200 OK
Access-Control-Allow-Headers: Content-Type, Authorization
Access-Control-Allow-Methods: GET, POST, PUT, PATCH, DELETE, OPTIONS
Access-Control-Max-Age: 3600
Cache-Control: no-cache, no-store, must-revalidate
Connection: keep-alive
Content-Type: text/event-stream
Referrer-Policy: no-referrer
X-Accel-Buffering: no
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
X-Request-Id: 85141f5f4b8da92935ea497600724e26
---SSE body---
event: error
data: {"error":"resolve instance base URL: instance API URL not configured: local-opencode"}



### 14. /api/audit/logs
{"entries":[],"total":0}

HTTP_STATUS:200
