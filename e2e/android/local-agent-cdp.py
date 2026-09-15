#!/usr/bin/env python3
"""CDP 驱动本地智能体全链路验证(2026-09-15)。

用法: local-agent-cdp.py <webSocketDebuggerUrl> [apiBase]

阶段:
  P0 清残留(pocket:localagent:sessions)→ P1 登录注入 → #/local-agent
  P2 工具循环:calculate 算 23*7+128 → 工具卡 completed + 终答含 289
  P3 审批放行:write_file → 审批条 → 点「允许」→ completed + 终答
  P4 审批拒绝:http_fetch/write_file → 点「拒绝」→ denied + 模型改道
"""
import json, sys, time
import websocket

WS_URL = sys.argv[1]
API_BASE = sys.argv[2] if len(sys.argv) > 2 else "http://192.168.31.37:8090"

ws = websocket.create_connection(WS_URL, timeout=30, suppress_origin=True)
mid = 0

def evaluate(expr, timeout=60):
    global mid
    mid += 1
    ws.send(json.dumps({"id": mid, "method": "Runtime.evaluate",
                        "params": {"expression": expr, "returnByValue": True, "awaitPromise": True}}))
    deadline = time.time() + timeout
    while time.time() < deadline:
        msg = json.loads(ws.recv())
        if msg.get("id") == mid:
            if "exceptionDetails" in msg.get("result", {}):
                raise RuntimeError(json.dumps(msg["result"]["exceptionDetails"])[:600])
            return msg["result"]["result"].get("value")
    raise TimeoutError("evaluate timeout: " + expr[:100])

def send_via_ui(text, verify=True, retries=5):
    """composer 注入文本 + 点发送。

    点击偶发落在 Vue 重渲染前后的旧节点上(v-if 停止/发送按钮互换),因此
    点击后校验「时间线出现该 user 条目」,未出现则重试。
    """
    for attempt in range(retries):
        r = evaluate(f"""
          (async () => {{
            const ta = document.querySelector('.draft');
            const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set;
            setter.call(ta, {json.dumps(text)});
            ta.dispatchEvent(new Event('input', {{bubbles: true}}));
            await new Promise(r => setTimeout(r, 300));
            const btn = document.querySelector('.send-btn');
            if (!btn || btn.disabled) return 'btn-not-ready';
            btn.click();
            await new Promise(r => setTimeout(r, 900));
            {f'''
            const rt = globalThis.__openpocket_localAgentRuntime__;
            const hit = rt.listSessions().some(s => s.timeline.some(i => i.kind === 'user' && i.text === {json.dumps(text)}));
            return JSON.stringify({{got: hit}});''' if verify else "return 'sent';"}
          }})()
        """, timeout=25)
        if not verify:
            return r
        try:
            if json.loads(r).get('got'):
                return 'sent'
        except Exception:
            pass
        time.sleep(2)
    return 'send-failed'

def find_session(prompt, timeout=30):
    """按用户条目文本找到本次 prompt 的会话 id。"""
    deadline = time.time() + timeout
    while time.time() < deadline:
        sid = evaluate("""
          (function(){
            var rt = globalThis.__openpocket_localAgentRuntime__;
            var hit = rt.listSessions().find(function(s){
              return s.timeline.some(function(i){return i.kind==='user' && i.text===%s;});
            });
            return hit ? hit.id : '';
          })()
        """ % json.dumps(prompt), timeout=15)
        if sid:
            return sid
        time.sleep(1.5)
    return None

def wait_session_state(sid, phase, timeout=180):
    """phase='start':等 running=true;phase='end':等 running=false(先经历 start 语义)。"""
    saw_running = False
    deadline = time.time() + timeout
    while time.time() < deadline:
        running = evaluate(
            "String(globalThis.__openpocket_localAgentRuntime__.isRunning(%s))" % json.dumps(sid),
            timeout=15)
        if running == 'true':
            saw_running = True
            if phase == 'start':
                return 'running'
        elif running == 'false':
            if phase == 'end' and saw_running:
                return 'finished'
            if phase == 'start':
                # 可能还没起跑,也可能是瞬间完成;交给上层按状态判断
                st = evaluate(
                    "globalThis.__openpocket_localAgentRuntime__.getSession(%s).status" % json.dumps(sid),
                    timeout=10)
                if st in ('idle', 'error', 'aborted') and saw_running is False:
                    # 短暂等待确认没有 late start
                    time.sleep(3)
                    running2 = evaluate(
                        "String(globalThis.__openpocket_localAgentRuntime__.isRunning(%s))" % json.dumps(sid),
                        timeout=10)
                    if running2 != 'true':
                        return 'finished-fast'
        time.sleep(2)
    return 'timeout'

def session_dump(sid, tail=None):
    items_expr = "s.timeline" if not tail else "s.timeline.slice(-%d)" % tail
    return json.loads(evaluate("""
      (function(){
        var rt = globalThis.__openpocket_localAgentRuntime__;
        var s = rt.getSession(%s);
        return JSON.stringify({status:s.status, usage:s.usage,
          items: %s.map(function(i){return {kind:i.kind,name:i.name,state:i.state,
            text:(i.text||'').slice(0,150), result:(i.result||'').slice(0,120),
            error:(i.error||'').slice(0,120)};})});
      })()
    """ % (json.dumps(sid), items_expr), timeout=20))

def approval_click(label, timeout=90):
    """等审批条出现并点击指定按钮;成功返回 True。"""
    deadline = time.time() + timeout
    while time.time() < deadline:
        r = evaluate("""
          (function(){
            var bar = document.querySelector('.approval-bar');
            if (!bar) return 'no-bar';
            var btns = [...bar.querySelectorAll('.btn')];
            var b = btns.find(x => x.textContent.trim() === %s);
            if (!b) return 'no-btn';
            b.click();
            return 'clicked';
          })()
        """ % json.dumps(label), timeout=15)
        if r == 'clicked':
            return True
        time.sleep(2)
    return False

def show_errs():
    try:
        errs = evaluate("JSON.stringify((window.__errs||[]).slice(0,3))", timeout=10)
        if errs and errs != '[]':
            print("page errors:", errs)
    except Exception:
        pass

# ---------------------------------------------------------------- P0+P1
print("== P0 clear + P1 login ==")
evaluate("""
  window.__errs = [];
  window.addEventListener('error', function(e){ window.__errs.push('err: ' + e.message); });
  window.addEventListener('unhandledrejection', function(e){ window.__errs.push('rej: ' + String(e.reason && e.reason.message || e.reason)); });
  localStorage.removeItem('pocket:localagent:sessions');
  'cleared'
""", timeout=15)

token = None
for pwd in ("Veritrans&9527", "d18db57a2e35e792b5223e562be2c3ea"):
    login = evaluate(f"""
      fetch('{API_BASE}/api/auth/login', {{
        method: 'POST', headers: {{'Content-Type': 'application/json'}},
        body: JSON.stringify({{username: 'admin', password: {json.dumps(pwd)}}})
      }}).then(r => r.json())
    """, timeout=20)
    token = (login or {}).get('token')
    if token:
        print("login ok (password:", pwd[:6] + "...)")
        break
assert token, "login failed both passwords"
evaluate(f"""
  localStorage.setItem('pocket_token', {json.dumps(token)});
  localStorage.setItem('pocket_user', 'admin');
  localStorage.setItem('pocket_workspace_id', 'ws_user-admin');
  localStorage.setItem('pocket_auth_method', 'password');
  location.hash = '#/local-agent';
  location.reload();
  'reloading'
""", timeout=15)
time.sleep(7)
print("route:", evaluate("location.hash", timeout=10))
assert 'local-agent' in evaluate("location.hash", timeout=10)

# ---------------------------------------------------------------- P2
P2_PROMPT = "用 calculate 工具精确计算 23*7+128,然后告诉我最终数字"
print("== P2 calculate tool loop ==")
print("send:", send_via_ui(P2_PROMPT))
sid2 = find_session(P2_PROMPT)
print("session:", sid2)
assert sid2, "P2 会话未创建"
print("phase:", wait_session_state(sid2, 'end', 200))
tl2 = session_dump(sid2)
tools2 = [i for i in tl2['items'] if i['kind'] == 'tool']
answers2 = [i['text'] for i in tl2['items'] if i['kind'] == 'assistant' and i['text']]
calc_ok = any(t['name'] == 'calculate' and t['state'] == 'completed' and '289' in (t['result'] or '') for t in tools2)
ans_ok = any('289' in a for a in answers2)
print("tools:", [(t['name'], t['state']) for t in tools2], "| status:", tl2['status'], "| usage:", tl2['usage'])
print("answer:", (answers2[-1] if answers2 else '')[:100])
print("P2 RESULT:", "PASS" if (calc_ok and ans_ok and tl2['status'] == 'idle') else "FAIL")
json.dump(tl2, open('/tmp/localagent-p2.json', 'w'), ensure_ascii=False, indent=1)

# ---------------------------------------------------------------- P3
P3_PROMPT = "把「买牛奶;预约牙医」保存为 notes/todo.md 文件,保存后告诉我里面有哪几条"
print("== P3 approval allow ==")
print("send:", send_via_ui(P3_PROMPT))
sid3 = find_session(P3_PROMPT)
print("session:", sid3)
assert sid3, "P3 会话未创建"
allowed = approval_click('允许', 120)
print("approval allow clicked:", allowed)
import subprocess
if allowed:
    subprocess.run("adb exec-out screencap -p > /tmp/localagent-approval.png", shell=True)
print("phase:", wait_session_state(sid3, 'end', 200))
tl3 = session_dump(sid3)
tools3 = [i for i in tl3['items'] if i['kind'] == 'tool']
write_ok = any(t['name'] == 'write_file' and t['state'] == 'completed' for t in tools3)
print("tools:", [(t['name'], t['state']) for t in tools3])
print("P3 RESULT:", "PASS" if (allowed and write_ok and tl3['status'] == 'idle') else "FAIL")
json.dump(tl3, open('/tmp/localagent-p3.json', 'w'), ensure_ascii=False, indent=1)

# ---------------------------------------------------------------- P4
P4_PROMPT = "用 http_fetch 抓取 https://example.com 的页面标题"
print("== P4 approval deny ==")
print("send:", send_via_ui(P4_PROMPT))
sid4 = find_session(P4_PROMPT)
print("session:", sid4)
assert sid4, "P4 会话未创建"
denied_click = approval_click('拒绝', 120)
print("approval deny clicked:", denied_click)
print("phase:", wait_session_state(sid4, 'end', 200))
tl4 = session_dump(sid4)
tools4 = [i for i in tl4['items'] if i['kind'] == 'tool']
deny_ok = any(t['state'] == 'denied' for t in tools4)
answers4 = [i['text'] for i in tl4['items'] if i['kind'] == 'assistant' and i['text']]
print("tools:", [(t['name'], t['state']) for t in tools4])
print("answer:", (answers4[-1] if answers4 else '')[:100])
print("P4 RESULT:", "PASS" if (denied_click and deny_ok and answers4) else "FAIL")
json.dump(tl4, open('/tmp/localagent-p4.json', 'w'), ensure_ascii=False, indent=1)

subprocess.run("adb exec-out screencap -p > /tmp/localagent-final.png", shell=True)
show_errs()
print("ALL DONE")
