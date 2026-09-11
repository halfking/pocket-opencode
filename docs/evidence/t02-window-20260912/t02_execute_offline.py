#!/usr/bin/env python3
"""T02 phase 2 — execute the task while Pocket is offline.

Supervisor path (same trust shape as the 2026-09-10 standalone T16, now on
the Pocket-bound run): dispatch → claim → start → t16-runner (real OpenCode)
→ independent checker service → checker-signed attestation → gated complete
→ review. The phone (Pocket) is down the whole time; nothing here touches it.
"""
import base64, hashlib, hmac, json, os, pathlib, subprocess, sys, time, urllib.error, urllib.request

EVID = pathlib.Path(__file__).resolve().parent
ACC = os.environ.get("T02_ACC_URL", "http://127.0.0.1:14102")
TENANT = "t16d-tenant"
SECRET = os.environ["T02_ACC_SECRET"]
CHECKER = os.environ.get("T02_CHECKER_URL", "http://127.0.0.1:19710")
WORKSPACE = os.environ["T02_WORKSPACE"]
RUNNER = os.environ["T02_RUNNER_BIN"]
AGENT = os.environ["T02_AGENT_BIN"]
KEY_ID = "checker-1"
CHECKER_KEY = base64.b64decode(os.environ["T16D_CHECKER_KEY"])


def acc_token():
    def enc(x):
        return base64.urlsafe_b64encode(json.dumps(x, separators=(",", ":")).encode()).rstrip(b"=")
    body = enc({"alg": "HS256", "typ": "JWT"}) + b"." + enc(
        {"sub": "t02-supervisor", "type": "agent", "tenant_id": TENANT,
         "isAdmin": True, "iat": int(time.time()), "exp": int(time.time()) + 3600})
    return (body + b"." + base64.urlsafe_b64encode(
        hmac.new(SECRET.encode(), body, hashlib.sha256).digest()).rstrip(b"=")).decode()


def call(label, path, body=None, expect=None, method=None):
    headers = {"Content-Type": "application/json", "Authorization": "Bearer " + acc_token(),
               "Idempotency-Key": "t02-" + label + "-" + str(int(time.time() * 1000))}
    req = urllib.request.Request(ACC + path, data=None if body is None else json.dumps(body).encode(),
                                 headers=headers, method=method or ("GET" if body is None else "POST"))
    try:
        with urllib.request.urlopen(req, timeout=20) as r:
            status, raw = r.status, r.read()
    except urllib.error.HTTPError as r:
        status, raw = r.code, r.read()
    data = json.loads(raw)
    with (EVID / "http.jsonl").open("a") as f:
        f.write(json.dumps({"label": label, "time": time.time(), "path": path,
                            "status": status, "request": body, "response": data}) + "\n")
    mark = "" if expect is None else ("OK" if status == expect else f"MISMATCH(expected {expect})")
    print(f"[{label}] {req.method} {path} -> {status} {mark}", flush=True)
    if expect is not None and status != expect:
        sys.exit(f"{label} failed: {json.dumps(data)[:500]}")
    return data


def spec_hash_of(task_id):
    out = subprocess.check_output(["docker", "exec", "w1-t16-evidence-pg", "psql", "-U", "t16", "-d", "t16",
                                   "-Atc", "SELECT spec_hash FROM orchestration_task_revisions "
                                           f"WHERE tenant_id='{TENANT}' AND task_id='{task_id}'"], text=True).strip()
    assert len(out) == 64, out
    return out


def main():
    state = json.loads((EVID / "offline-state.json").read_text())
    run_id, task = state["run_id"], state["task_id"]
    base = state["initial_task"].get("base_commit") or subprocess.check_output(
        ["git", "-C", WORKSPACE, "rev-parse", "HEAD"], text=True).strip()

    # workspace back to baseline; the runner requires a fresh artifact
    subprocess.run(["git", "-C", WORKSPACE, "reset", "--hard", "HEAD"], check=True, capture_output=True)
    subprocess.run(["git", "-C", WORKSPACE, "clean", "-fdq"], check=True, capture_output=True)

    d = call("dispatch", f"/api/v2/orchestration/tasks/{task}/dispatch", {}, expect=202)["data"]
    disp, rev = d["dispatch_id"], d["revision_id"]
    call("claim", f"/api/v2/orchestration/dispatches/{disp}/claim",
         {"holder_id": "t02-live-worker", "ttl_ms": 600000}, expect=202)
    started = call("start", f"/api/v2/orchestration/dispatches/{disp}/start",
                   {"holder_id": "t02-live-worker", "fencing_token": 1})["data"]
    fence = started["fencing_token"]
    spec_hash = spec_hash_of(task)
    print(f"dispatch={disp} fence={fence}", flush=True)

    # real execution via the runner (same binary the daemon uses)
    evid = EVID / "runner-evidence"
    subprocess.run(["rm", "-rf", str(evid)], check=True)
    # the runner owns the evidence dir (fresh-dir guarantee); the assignment
    # lives one level up, mirroring the daemon executor layout
    assignment_path = EVID / "assignment-current.json"
    assignment = {"tenant_id": TENANT, "task_id": task, "run_id": run_id, "dispatch_id": disp,
                  "revision_id": rev, "fencing_token": fence, "base_commit": base, "spec_hash": spec_hash}
    assignment_path.write_text(json.dumps(assignment, indent=2))
    proc = subprocess.run([RUNNER, "-workspace", WORKSPACE, "-evidence", str(evid),
                           "-agent", AGENT, "-assignment", str(assignment_path)],
                          capture_output=True, text=True, timeout=330)
    (evid / "runner-stdout.txt").write_text(proc.stdout)
    (evid / "runner-stderr.txt").write_text(proc.stderr)
    print(f"runner_exit={proc.returncode} :: {proc.stdout.strip()[:160]}", flush=True)
    if proc.returncode != 0:
        sys.exit(f"runner failed: {proc.stderr[:300]}")
    pins = dict(p.split("=", 1) for p in proc.stdout.strip().splitlines()[-2].split()[1:]) \
        if "TRUST" in proc.stdout else None
    trust_line = [l for l in proc.stdout.splitlines() if l.startswith("TRUST ")][-1]
    pins = dict(p.split("=", 1) for p in trust_line.split()[1:])

    # independent checker (separate process, holds the checker key)
    req = {"evidence_dir": str(evid), "workspace": WORKSPACE,
           "key_pin": pins["public_key_sha256"], "expected_pin": pins["expected_sha256"],
           **{k: assignment[k] for k in ("tenant_id", "task_id", "run_id", "dispatch_id",
                                         "revision_id", "base_commit", "fencing_token")}}
    creq = urllib.request.Request(CHECKER + "/verify", data=json.dumps(req).encode(),
                                  headers={"Content-Type": "application/json"}, method="POST")
    with urllib.request.urlopen(creq, timeout=60) as r:
        verdict = json.loads(r.read())
    (evid / "checker-verdict.json").write_text(json.dumps(verdict, indent=2))
    print(f"checker accepted={verdict['accepted']} tests={verdict.get('tests_run')}", flush=True)
    if not verdict["accepted"]:
        sys.exit("checker rejected: " + verdict.get("reason", ""))

    complete = call("complete", f"/api/v2/orchestration/dispatches/{disp}/complete",
                    {"holder_id": "t02-live-worker", "fencing_token": fence, "success": True,
                     "result": {"artifact_sha256": verdict["artifact_sha256"],
                                "checker": {"key_id": KEY_ID, "payload": verdict["payload"],
                                            "signature": verdict["signature"]}}}, expect=200)
    review = call("review", f"/api/v2/orchestration/tasks/{task}/review",
                  {"approved": True, "reason": "offline-window execution verified by independent checker"},
                  expect=200)
    print("task_status:", review["data"]["status"], flush=True)
    (EVID / "execution-result.json").write_text(json.dumps({
        "dispatch_id": disp, "fencing_token": fence,
        "artifact_sha256": verdict["artifact_sha256"],
        "task_status": review["data"]["status"],
        "runner_stdout_tail": proc.stdout.strip().splitlines()[-1],
    }, indent=2))


if __name__ == "__main__":
    main()
