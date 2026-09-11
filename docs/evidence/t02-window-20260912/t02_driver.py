#!/usr/bin/env python3
"""T02 driver — phase 1 (pre-offline) and phase 2 (post-reconnect) helper.

Usage:
  t02_driver.py setup    create ACC plan/run/task + Pocket delegate + initial cursor
  t02_driver.py recover  re-login, GET task, replay events after the initial cursor
Environment: T02_* variables (see runbook); ACC secret never logged.
"""
import base64, hashlib, hmac, json, os, pathlib, sys, time, urllib.error, urllib.request

EVID = pathlib.Path(__file__).resolve().parent
ACC = os.environ.get("T02_ACC_URL", "http://127.0.0.1:14102")
POCKET = os.environ.get("T02_POCKET_URL", "http://127.0.0.1:18088")
TENANT = "t16d-tenant"
SECRET = os.environ["T02_ACC_SECRET"]
TASK = os.environ.get("T02_TASK_ID", "t02-pocket-task")
WORKSPACE = os.environ["T02_WORKSPACE"]
POCKET_TOKEN_FILE = EVID / ".pocket-token"


def acc_token():
    def enc(x):
        return base64.urlsafe_b64encode(json.dumps(x, separators=(",", ":")).encode()).rstrip(b"=")
    body = enc({"alg": "HS256", "typ": "JWT"}) + b"." + enc(
        {"sub": "t02-supervisor", "type": "agent", "tenant_id": TENANT,
         "isAdmin": True, "iat": int(time.time()), "exp": int(time.time()) + 3600})
    return (body + b"." + base64.urlsafe_b64encode(
        hmac.new(SECRET.encode(), body, hashlib.sha256).digest()).rstrip(b"=")).decode()


def http(label, url, body=None, token=None, method=None, expect=None):
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    req = urllib.request.Request(url, data=None if body is None else json.dumps(body).encode(),
                                 headers=headers, method=method or ("GET" if body is None else "POST"))
    try:
        with urllib.request.urlopen(req, timeout=20) as r:
            status, raw = r.status, r.read()
    except urllib.error.HTTPError as r:
        status, raw = r.code, r.read()
    try:
        data = json.loads(raw)
    except Exception:
        data = {"raw": raw.decode(errors="replace")[:500]}
    with (EVID / "http.jsonl").open("a") as f:
        f.write(json.dumps({"label": label, "time": time.time(), "url": url,
                            "status": status, "request": body, "response": data}) + "\n")
    mark = "" if expect is None else ("OK" if status == expect else f"MISMATCH(expected {expect})")
    print(f"[{label}] {req.method} {url.split(ACC)[-1] if url.startswith(ACC) else url} -> {status} {mark}", flush=True)
    return status, data


def pocket_login():
    s, d = http("login", POCKET + "/api/auth/login",
                {"username": os.environ.get("T02_POCKET_USER","admin"), "password": os.environ["T02_POCKET_PASS"]}, expect=200)
    tok = d.get("token") or d.get("data", {}).get("token")
    if not tok:
        sys.exit(f"no token in login response: {json.dumps(d)[:300]}")
    POCKET_TOKEN_FILE.write_text(tok)
    return tok


def setup():
    # 1. ACC orchestration: plan → publish → run → graph(task)
    s, plan = http("plan", ACC + "/api/v2/orchestration/plans",
                   {"goal": "T02 offline window with real execution", "constraints": ["isolated trial"]}, token=acc_token(), expect=201)
    plan_id = plan["data"]["plan_id"]
    http("publish", ACC + f"/api/v2/orchestration/plans/{plan_id}/publish", {}, token=acc_token(), expect=200)
    s, run = http("run", ACC + "/api/v2/orchestration/runs", {"plan_id": plan_id}, token=acc_token(), expect=202)
    run_id = run["data"]["run_id"]
    base = subprocess_git_head()
    spec = {"task_type": "test",
            "objective": {"goal": "valid_port requires exact int and 1..65535"},
            "permissions": {"scopes": ["workspace.write:port_validation.py"],
                            "side_effect_level": "workspace_write"},
            "background": {"base_commit": base},
            "audit_standard": {"evidence_required": True},
            "budget": {"max_steps": 3, "max_tool_calls": 3, "max_tokens": 8000, "max_elapsed_ms": 240000},
            "acceptance_criteria": ["type(value) is int; 1 <= value <= 65535",
                                    "13 independent boundary cases",
                                    "no file changes other than port_validation.py"]}
    http("graph", ACC + f"/api/v2/orchestration/runs/{run_id}/graph",
         {"nodes": [{"task_id": TASK, "spec": spec}]}, token=acc_token(), expect=202)
    time.sleep(1)

    # 2. Pocket login + delegate with the canonical run/task binding
    tok = pocket_login()
    s, d = http("delegate", POCKET + "/api/tasks/delegate",
                {"kind": "test", "title": "T02 offline-window task", "run_id": run_id, "task_id": TASK},
                token=tok, expect=200)
    if d.get("task_id") != TASK:
        sys.exit(f"delegate binding mismatch: {json.dumps(d)[:400]}")

    # 3. initial projection + events (the offline watermark)
    s, task = http("task-initial", POCKET + f"/api/tasks/{TASK}", token=tok, expect=200)
    s, ev = http("events-initial", POCKET + f"/api/tasks/{TASK}/events?after=0", token=tok, expect=200)
    seqs = parse_sse_sequences(ev if isinstance(ev, dict) else {})
    initial_cursor = max(seqs) if seqs else 0
    print(f"initial_cursor={initial_cursor} events={seqs}", flush=True)
    (EVID / "offline-state.json").write_text(json.dumps({
        "run_id": run_id, "task_id": TASK, "initial_cursor": initial_cursor,
        "initial_sequences": seqs, "initial_task": task,
        "offline_start_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "offline_start_epoch": time.time(),
    }, indent=2))


def recover():
    state = json.loads((EVID / "offline-state.json").read_text())
    run_id, task, cursor = state["run_id"], state["task_id"], state["initial_cursor"]
    tok = pocket_login()
    s, t = http("task-recovered", POCKET + f"/api/tasks/{task}", token=tok, expect=200)
    s, ev = http("events-replay", POCKET + f"/api/tasks/{task}/events?after={cursor}", token=tok, expect=200)
    seqs = parse_sse_sequences(ev if isinstance(ev, dict) else {})
    elapsed = time.time() - state["offline_start_epoch"]
    print(f"offline_elapsed={int(elapsed)}s replayed_sequences={seqs}", flush=True)
    (EVID / "recover-state.json").write_text(json.dumps({
        "offline_elapsed_s": round(elapsed, 1),
        "task_after_recovery": t,
        "replayed_sequences": seqs,
        "replay_events": ev,
    }, indent=2))
    ok = elapsed >= 1800 - 5 and len(seqs) > 0
    print("T02_RECOVER_ASSERT:", "PASS" if ok else "FAIL")
    return 0 if ok else 1


def parse_sse_sequences(payload):
    # the endpoint answers text/event-stream; urllib surfaces it as bytes-ish dict only if JSON.
    if isinstance(payload, dict) and "raw" in payload:
        seqs = []
        for line in payload["raw"].splitlines():
            if line.startswith("id: "):
                seqs.append(int(line[4:].strip()))
        return seqs
    if isinstance(payload, dict):
        evs = payload.get("events") or payload.get("data") or []
        return [e.get("sequence") for e in evs if isinstance(e, dict)]
    return []


def subprocess_git_head():
    import subprocess
    return subprocess.check_output(["git", "-C", WORKSPACE, "rev-parse", "HEAD"], text=True).strip()


if __name__ == "__main__":
    cmd = sys.argv[1]
    sys.exit({"setup": setup, "recover": recover}[cmd]())
