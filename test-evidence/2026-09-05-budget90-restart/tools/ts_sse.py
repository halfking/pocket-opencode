#!/usr/bin/env python3
# 逐行加时戳读取 SSE stdin：每帧前缀 [T+xx.xx]s
import sys, time
t0 = time.monotonic()
for line in sys.stdin:
    print(f"[T+{time.monotonic()-t0:6.2f}s] {line}", end="", flush=True)
print(f"[T+{time.monotonic()-t0:6.2f}s] === EOF ===")
