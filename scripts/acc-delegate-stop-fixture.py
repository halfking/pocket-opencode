#!/usr/bin/env python3
"""夹具：删掉上一轮 acc-delegate-stop.yaml 建的同名任务。

理由与 tasks-crud-fixture.mjs 完全一样：flow 第 4 步断言
`visible: "maestro-acc-stop"` —— 而 Maestro 判断 visible 只看节点在不在
无障碍树里，**不看它是不是上一轮留下的** ⇒ 残留卡片会让那一步假通过
（恰好在最需要它报警的时候不报）。
"""
import json, os, sys, urllib.request

ENV = os.path.expanduser("~/kaixuan/acp/logs/rerun-round52/pocketd-18099.env")
BASE = os.environ.get("POCKET_API_BASE", "http://127.0.0.1:18099")
TITLE = "maestro-acc-stop"

def env():
    d = {}
    for line in open(ENV):
        line = line.strip()
        if line.startswith("export "):
            line = line[7:]
        if "=" in line and not line.startswith("#"):
            k, v = line.split("=", 1)
            d[k.strip()] = v.strip().strip('"').strip("'")
    return d

e = env()
req = urllib.request.Request(BASE + "/api/auth/login", method="POST",
    data=json.dumps({"username": e.get("POCKET_AUTH_USER"), "password": e.get("POCKET_AUTH_PASS")}).encode(),
    headers={"Content-Type": "application/json"})
tok = json.loads(urllib.request.urlopen(req, timeout=20).read())["token"]

def get(path):
    r = urllib.request.Request(BASE + path, headers={"Authorization": "Bearer " + tok})
    return json.loads(urllib.request.urlopen(r, timeout=20).read())

def delete(path):
    r = urllib.request.Request(BASE + path, method="DELETE", headers={"Authorization": "Bearer " + tok})
    try:
        return urllib.request.urlopen(r, timeout=20).status
    except Exception as ex:
        return str(ex)

tasks = get("/api/tasks")
items = tasks.get("tasks") if isinstance(tasks, dict) else tasks
hit = [t for t in (items or []) if (t.get("title") or "") == TITLE]
print("找到同名残留任务 %d 条" % len(hit))
for t in hit:
    print("  删除", t.get("id"), "->", delete("/api/tasks/" + t.get("id")))
