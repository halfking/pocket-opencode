#!/usr/bin/env python3
"""§935 + §936 + §937 + §938 + §939：把各批变异**依次**跑一遍。

⚠ 为什么要「依次」而不是「并行」：s919 与 s920 都会改
   `frontend/src/api/meetings.ts` ⇒ 并行会互相踩（还原时会把对方的改动冲掉）⇒
   ⇒ 变异批次**必须串行**。

⚠ 退出码的读法（§935.4.2 的教训：skip 与通过在退出码上完全同形）：
     0  = 全部批次都跑了，且每条变异都符合预期
     1  = 至少一批**跑了**，但有变异没被抓住（或量具自证失败）
     2  = 至少一批**拒绝跑**（缺依赖，例如 §936/§937 需要真 PG）
   ⇒ 「拒绝跑」**不算**通过 ⇒ 退出码非 0 ⇒ 不会被误读成「全绿」。

用法：
  python3 scripts/mutations/run_all.py
"""
import subprocess
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
BATCHES = ['s919.py', 's920.py', 's921.py', 's922.py', 's936.py', 's937.py', 's938.py', 's939.py']

failed = []
refused = []
for name in BATCHES:
    print('\n' + '=' * 72)
    p = subprocess.run([sys.executable, str(HERE / name)], cwd=str(REPO))
    if p.returncode == 3:
        refused.append(name)
    elif p.returncode != 0:
        failed.append(name)

print('\n' + '=' * 72)
if failed:
    print('❌ 至少一批没过：%s' % '、'.join(failed))
if refused:
    print('⚠ 至少一批**拒绝跑**（缺依赖，不算通过）：%s' % '、'.join(refused))
if not failed and not refused:
    print('✅ 全部批次全过')
sys.exit(1 if failed else (2 if refused else 0))
