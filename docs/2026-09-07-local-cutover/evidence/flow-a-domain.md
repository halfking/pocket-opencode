# Flow A via https://pocket.itestu.cn

- LOGIN 200 ws=ws_user-admin
- NOAUTH 401
- INSTANCE health=offline workspaceId=ws_user-admin
- CREATE 200 sid=ses_f836d09c0ffeRA8pVqJ9j8d9Kv snippet={"id":"ses_f836d09c0ffeRA8pVqJ9j8d9Kv","projectID":"0a484221e7eefc07ab87878553ef387a035c17ee","cost":0,"tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}},"time":{"created":1788796401215,"updated":
- SSE urllib IncompleteRead (long-lived stream); curl -N retry on domain: first frame `event: server.connected` (Content-Type text/event-stream). localhost:8090 same.
- PROMPT 502 context deadline (OpenCode LLM slow via gateway); messages still arrived

- MESSAGES 200 n=2 roles=['user', 'assistant']
- INTERRUPT 204
- REPLAY 200 replayed=true sid=ses_f836d09c0ffeRA8pVqJ9j8d9Kv
