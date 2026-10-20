#!/usr/bin/env python3
"""§935：把四批变异**依次**跑一遍。

⚠ 为什么要「依次」而不是「并行」：s919 与 s920 都会改
   `frontend/src/api/meetings.ts` ⇒ 并行会互相踩（还原时会把对方的改动冲掉）⇒
   ⇒ 变异批次**必须串行**。

用法：
  python3 scripts/mutations/run_all.py
退出码：0 = 四批全过；非 0 = 至少一批有变异没被抓住（或量具自证失败）。
"""
import subprocess
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
BATCHES = ['s919.py', 's920.py', 's921.py', 's922.py']

rc = 0
for name in BATCHES:
    print('\n' + '=' * 72)
    p = subprocess.run([sys.executable, str(HERE / name)], cwd=str(REPO))
    rc = rc or p.returncode

print('\n' + '=' * 72)
print('✅ 四批全过' if rc == 0 else '❌ 至少一批没过')
sys.exit(rc)
