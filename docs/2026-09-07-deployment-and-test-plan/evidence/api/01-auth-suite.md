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

## Register suite — PG-backed (/api/auth/register) 2026-09-06T22:46:39Z
环境：POCKET_POSTGRES_DSN → pocket-e2e-pg:15434/pocket_e2e（一次性测试库）；POCKET_SMTP_DEBUG_ECHO=true

### R1. send-code（SMTP 未配置，debug 回显验证码）
{"debug_code":"903755","ok":true,"ttl_sec":300}

200
code=903755

### R2. register（新用户）→ 200 + token
{"token":"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.<sig-redacted>","user":"e2euser","user_id":"user-e2e-user@example.com","workspace_id":"ws_user-e2e-user@example.com"}

200

### R3. /api/auth/me（新注册用户）
{
  "email": "",
  "id": "user-e2e-user@example.com",
  "name": "user-e2e-user@example.com",
  "role": "user"
}

### R4. login（新用户名+密码，legacy VerifyPassword 路径）→ 200
HTTP 401
### R5. login（新用户+错误密码）→ 401
HTTP 401

### R6. 重复注册（同邮箱+重发新 code）→ 409 邮箱已被注册
{"error":"验证码错误或已过期"}

HTTP 400
### R7. 弱密码（<8 位）→ 400
{"error":"密码至少 8 位"}

HTTP 400
### R8. 数据闭环：users 表落库
ERROR:  relation "users" does not exist
LINE 1: ...CT id, username, email, email_verified, role FROM users WHER...
                                                             ^

## Register suite 续（修复后复测，dev 旁路 fall-through fix + 代码夹具）2026-09-06T22:49:57Z

### R4r. login（注册用户 e2euser，修复后）→ 期望 200
{"auth_method":"legacy","role":"user","token":"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.<sig-redacted>","user":"e2euser","user_id":"user-e2e-user@example.com","workspace_id":"ws_user-e2e-user@example.com"}

HTTP 200
### R5r. login（e2euser + 错误密码）→ 401
HTTP 401
### R6r. 重复注册（种库验证码 654321）→ 409 邮箱已被注册
{"error":"邮箱已被注册"}

HTTP 409
### R8r. 数据闭环：users 表（schema opencode_pocket）
            id             | username |        email         | email_verified | role  
---------------------------+----------+----------------------+----------------+-------
 user-admin                | admin    |                      | f              | admin
 user-e2e-user@example.com | e2euser  | e2e-user@example.com | t              | user
(2 rows)

### R9. admin 登录回归（dev 旁路仍命中）→ 200
HTTP 200
