## Auth suite — 2026-09-06T20:59:41Z

### 1. healthz
HTTP 200 body=2B
ok

### 2. login (admin / Veritrans&9527)
{"auth_method":"dev-bypass","role":"admin","token":"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VyX2lkIjoidXNlci1hZG1pbiIsInJvbGUiOiJhZG1pbiIsIndvcmtzcGFjZV9pZCI6ImRlZmF1bHQiLCJpc3MiOiJwb2NrZXQiLCJhdWQiOlsicG9ja2V0LWFwaSJdLCJleHAiOjE3ODg4MTQ3ODEsIm5iZiI6MTc4ODcyODM4MSwiaWF0IjoxNzg4NzI4MzgxfQ.<sig-redacted>","user":"admin","user_id":"user-admin","workspace_id":"default"}

HTTP_STATUS:200

### 3. login wrong password (expect 401)
HTTP 401

### 4. instances without auth (expect 401)
HTTP 401

### 5. instances with auth
{
  "code": "unauthenticated",
  "error": "missing authorization token",
  "request_id": "ace94523a984669a2ccac43016d327b0",
  "retryable": false
}

### 6. /api/auth/me
{
  "code": "unauthenticated",
  "error": "missing authorization token",
  "request_id": "7d667eff812db0617088d32d463dc3ae",
  "retryable": false
}

### 7. /api/auth/refresh
{
  "code": "unauthenticated",
  "error": "missing authorization token",
  "request_id": "65a0166eea348d781e20ef8607cbaddf",
  "retryable": false
}
### 5. instances with auth (re-run)
{
  "instances": [
    {
      "id": "discovered-local-3000",
      "displayName": "OpenCode (127.0.0.1:3000)",
      "environment": "discovered",
      "npsClientId": 0,
      "capabilities": [
        "session",
        "summary",
        "pty"
      ],
      "health": "unknown",
      "lastHeartbeatAt": "2026-09-06T20:59:19Z",
      "apiBaseURL": "http://127.0.0.1:3000",
      "hostname": "127.0.0.1",
      "ip": "127.0.0.1",
      "port": 3000,
      "machine": {
        "hostname": "127.0.0.1"
      },
      "origin": "discovered",
      "migrationStatus": "idle"
    },
    {
      "id": "discovered-192.168.31.37-3000",
      "displayName": "OpenCode (192.168.31.37:3000)",
      "environment": "discovered",
      "npsClientId": 0,
      "capabilities": [
        "session",
        "summary",
        "pty"
      ],
      "health": "unknown",
      "lastHeartbeatAt": "2026-09-06T20:59:19Z",
      "apiBaseURL": "http://192.168.31.37:3000",
      "hostname": "192.168.31.37",
      "ip": "192.168.31.37",
      "port": 3000,
      "machine": {
        "hostname": "192.168.31.37"
      },
      "origin": "discovered",
      "migrationStatus": "idle"
    },
    {
      "id": "local-opencode",
      "displayName": "本地 OpenCode 实例",
      "environment": "development",
      "npsClientId": 0,
      "capabilities": [
        "session",
        "summary",
        "pty"
      ],
      "health": "offline",
      "lastHeartbeatAt": "2026-09-06T20:59:18Z",
      "machine": {},
      "origin": "static"
    },
    {
      "id": "discovered-192.168.31.37-8080",
      "displayName": "OpenCode dev (192.168.31.37:8080)",
      "environment": "discovered",
      "npsClientId": 0,
      "capabilities": [
        "session",
        "summary",
        "pty"
      ],
      "health": "unknown",
      "lastHeartbeatAt": "2026-09-06T20:59:19Z",
      "apiBaseURL": "http://192.168.31.37:8080",
      "hostname": "192.168.31.37",
      "ip": "192.168.31.37",
      "port": 8080,
      "version": "dev",
      "machine": {
        "hostname": "192.168.31.37"
      },
      "origin": "discovered",
      "migrationStatus": "idle"
    },
    {
      "id": "discovered-local-8080",
      "displayName": "OpenCode dev (127.0.0.1:8080)",
      "environment": "discovered",
      "npsClientId": 0,
      "capabilities": [
        "session",
        "summary",
        "pty"
      ],
      "health": "unknown",
      "lastHeartbeatAt": "2026-09-06T20:59:19Z",
      "apiBaseURL": "http://127.0.0.1:8080",
      "hostname": "127.0.0.1",
      "ip": "127.0.0.1",
      "port": 8080,
      "version": "dev",
      "machine": {
        "hostname": "127.0.0.1"
      },
      "origin": "discovered",
      "migrationStatus": "idle"
    },
    {
      "id": "discovered-localhost-8080",
      "displayName": "OpenCode dev (localhost:8080)",
      "environment": "discovered",
      "npsClientId": 0,
      "capabilities": [
        "session",
        "summary",
        "pty"
      ],
      "health": "unknown",
      "lastHeartbeatAt": "2026-09-06T20:59:19Z",
      "apiBaseURL": "http://localhost:8080",
      "hostname": "localhost",
      "ip": "localhost",
      "port": 8080,
      "version": "dev",
      "machine": {
        "hostname": "localhost"
      },
      "origin": "discovered",
      "migrationStatus": "idle"
    },
    {
      "id": "discovered-localhost-3000",
      "displayName": "OpenCode (localhost:3000)",
      "environment": "discovered",
      "npsClientId": 0,
      "capabilities": [
        "session",
        "summary",
        "pty"
      ],
      "health": "unknown",
      "lastHeartbeatAt": "2026-09-06T20:59:19Z",
      "apiBaseURL": "http://localhost:3000",
      "hostname": "localhost",
      "ip": "localhost",
      "port": 3000,
      "machine": {
        "hostname": "localhost"
      },
      "origin": "discovered",
      "migrationStatus": "idle"
    }
  ]
}

### 6. /api/auth/me (re-run)
{
  "email": "",
  "id": "user-admin",
  "name": "user-admin",
  "role": "user"
}

### 7. /api/auth/refresh (re-run)
eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ...

### 8. /api/mobile/events/snapshot
{"sessions":[],"generated_at":1788728410000}

HTTP_STATUS:200

### 9. /api/mobile/approvals (no pending seeded)
{"code":"not_found","error":"resource not found","request_id":"50464746e634ec4615d6062ddbe49ba1","retryable":false}

HTTP_STATUS:404

### 10. /api/mobile/sessions list (upstream :4096 down — document behavior)
{"code":"not_found","error":"resource not found","request_id":"678262012a454ba907565bcafd826263","retryable":false}

HTTP_STATUS:404
