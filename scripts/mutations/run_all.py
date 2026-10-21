#!/usr/bin/env python3
"""§935 … §957：把各批变异**依次**跑一遍。

⚠ 为什么要「依次」而不是「并行」：s919 与 s920 都会改
   `frontend/src/api/meetings.ts` ⇒ 并行会互相踩（还原时会把对方的改动冲掉）⇒
   ⇒ 变异批次**必须串行**。

⚠⚠ §958：**必须串行**这件事，以前只是「写在注释里的约定」，**没有任何东西强制它**
   ⇒ ⇒ 本轮实测到一次真实事故：**两份跑批同时跑**（我以为第一份已被工具的 3600 s
   上限杀掉，**实际没有**）⇒ 两份在**同一个工作树**上抢同一批文件 ⇒ ⇒ 症状是
   「该红却绿」**与**「该绿却红」**同时**出现（实测：`s939` 假绿；`s942`/`s944` 的
   「还原后工作树必须与前置快照一致」看到的是**对方**正在改的文件）⇒ ⇒⇒ ⇒
   而这两个方向的**下一步动作都是改判据**（一个当「门没牙」、一个当「负控写错了」）
   ⇒ ⇒⇒ ⇒ ⇒ ⇒ **都是在拆门** ⇒ ⇒⇒ ⇒ ⇒ 所以「跑批必须单实例」不是建议，**必须被强制**。
   ⇒ ⇒ 本文件因此带**基于 PID 存活**的单实例守卫（⚠ 不能用「我以为它超时了所以它死了」
   当判据 —— 那个假设在本轮**被证伪过**）。

⚠ 退出码的读法（§935.4.2 的教训：skip 与通过在退出码上完全同形）：
     0  = 全部批次都跑了，且每条变异都符合预期
     1  = 至少一批**跑了**，但有变异没被抓住（或量具自证失败）
     2  = 至少一批**拒绝跑**（缺依赖，例如 §936/§937 需要真 PG）
     9  = **已有另一份跑批在跑** ⇒ 本次拒绝跑（不是「失败」，是「不并发」）
   ⇒ 「拒绝跑」**不算**通过 ⇒ 退出码非 0 ⇒ 不会被误读成「全绿」。

用法：
  python3 scripts/mutations/run_all.py
"""
import atexit
import os
import subprocess
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
BATCHES = ['s919.py', 's920.py', 's921.py', 's922.py', 's936.py', 's937.py', 's938.py', 's939.py', 's940.py', 's941.py', 's942.py', 's943.py', 's944.py', 's945.py', 's946.py', 's947.py', 's948.py', 's949.py', 's950.py', 's951.py', 's952.py', 's953.py', 's954.py', 's955.py', 's956.py', 's957.py']

# ⚠ PID 文件放**系统临时目录**（不在仓里 ⇒ 不会进版本库，也不会污染工作树）。
PIDFILE = Path(tempfile.gettempdir()) / 'openpocket-mutations-run.pid'


def _alive(pid):
    """PID 是否**还活着**（只探活，不发任何信号）。"""
    try:
        os.kill(pid, 0)
    except PermissionError:
        # 存在但属于别的用户 ⇒ 它**活着**，只是我们探不动它。
        return True
    except OSError:
        return False
    return True


def _release():
    try:
        if PIDFILE.exists() and PIDFILE.read_text(encoding='utf-8').strip() == str(os.getpid()):
            PIDFILE.unlink()
    except OSError:
        pass


def _acquire():
    """抢跑批锁。抢不到 ⇒ **拒绝跑**（退出码 9），而不是并行跑。"""
    if PIDFILE.exists():
        try:
            other = int(PIDFILE.read_text(encoding='utf-8').strip())
        except (ValueError, OSError):
            other = None
        if other and other != os.getpid() and _alive(other):
            print('❌ 已有跑批在跑（pid %d）⇒ 拒绝再起一份。' % other)
            print('   ⛔ 变异批次**就地改同一个工作树** ⇒ 并发两份会互相把对方的改动还原掉')
            print('   ⇒ 读数会同时出现「该红却绿」与「该绿却红」⇒ ⇒ 两者的下一步动作都是改判据。')
            print('   ⇒ 等那一份跑完再起（或先确认它已经不在了）。')
            return False
    PIDFILE.write_text(str(os.getpid()), encoding='utf-8')
    atexit.register(_release)
    return True


def main():
    if not _acquire():
        return 9
    failed = []
    refused = []
    try:
        for name in BATCHES:
            print('\n' + '=' * 72)
            p = subprocess.run([sys.executable, str(HERE / name)], cwd=str(REPO))
            if p.returncode == 3:
                refused.append(name)
            elif p.returncode != 0:
                failed.append(name)
    finally:
        _release()

    print('\n' + '=' * 72)
    if failed:
        print('❌ 至少一批没过：%s' % '、'.join(failed))
    if refused:
        print('⚠ 至少一批**拒绝跑**（缺依赖，不算通过）：%s' % '、'.join(refused))
    if not failed and not refused:
        print('✅ 全部批次全过')
    return 1 if failed else (2 if refused else 0)


if __name__ == '__main__':
    sys.exit(main())
