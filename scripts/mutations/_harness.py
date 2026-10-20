#!/usr/bin/env python3
"""§935 变异 harness —— 四个批次（§919/§920/§921/§922）**共用**的一份实现。

⚠ 为什么要有这个文件（与 `frontend/src/api/__tests__/_wire-helpers.mjs` 同一个道理）：
   §935 要重建四批变异 ⇒ 「落盘自证 / 还原自证 / 跑判据 / 期望红绿 / 残留核对」
   在四个脚本里是**同一段逻辑** ⇒ 抄四遍 ⇒ 同一缺陷必然出现在两处
   （§80 记过的形状：同一段逻辑抄两份 ⇒ 有测试的那一份有判据、没测试的那一份没有，
   于是同一缺陷只在一处暴露）⇒ ⇒ 抽成一份，四批共用。

⚠⚠ **这些脚本会就地改工作树**（这是「变异」的定义）⇒ ⇒ 三条自我保护：
   ① **前置**：目标文件必须与 HEAD 一致（`git diff --quiet HEAD -- <file>`）
      ⇒ 你有未提交的改动时它直接拒绝跑 ⇒ 不会把你的改动和变异混在一起。
   ② **还原自证**：还原后逐字节比对原文件 ⇒ 还原失败立刻退出（不吞）。
   ③ **残留核对**：跑完比对 `git status --porcelain` 与前置快照**完全一致**
      ⇒ 任何一个字节的残留都会被抓到。
   ⇒ 跑完工作树必须还是你交给我时的样子。

⚠ **为什么必须先落盘、再跑判据、最后还原**（§935 本轮踩过两次）：
   还原早于跑判据 ⇒ 测的是**没变异的文件** ⇒ 变异「全绿」⇒ 假通过。

⚠ **「落盘自证」不能用「旧串不在场」**（旧串不在场可能只是锚点写错）：
   ⇒ 用「文件确实变了 **且** 新串在场」。
"""
import os
import subprocess
import sys
from pathlib import Path

# ⚠ 路径一律从 __file__ 推导 ⇒ 仓内不得出现任何机器相关的绝对路径
#   （`check:no-hardcoded-paths` 那道门的道理：写死的路径在别的机器上是死路）。
REPO = Path(__file__).resolve().parents[2]
FRONTEND = REPO / 'frontend'
BACKEND = REPO / 'backend'


class HarnessError(RuntimeError):
    """前置/落盘/还原任一自证失败 ⇒ 立刻中止（不吞、不继续）。"""


class Batch:
    """一批变异。

    参数
    ----
    title  : 批次标题（打印用）
    runner : 无参可调用 ⇒ 返回 (exit_code, 输出文本)
    targets: 本批**允许**改动的文件（相对仓根）⇒ 还原与残留核对只看它们
    """

    def __init__(self, title, runner, targets):
        self.title = title
        self.runner = runner
        self.targets = [REPO / t for t in targets]
        self.results = []
        self._status_before = None
        self._bytes_before = None

    # ── 前置 ────────────────────────────────────────────────────────
    def preflight(self):
        for t in self.targets:
            if not t.exists():
                raise HarnessError('目标文件不存在：%s' % t)
        p = subprocess.run(
            ['git', 'diff', '--quiet', 'HEAD', '--'] + [str(t) for t in self.targets],
            cwd=str(REPO), capture_output=True, text=True)
        if p.returncode != 0:
            raise HarnessError(
                '目标文件相对 HEAD 有未提交改动 ⇒ 拒绝在别人的改动上做变异：\n' +
                '\n'.join('  ' + str(t.relative_to(REPO)) for t in self.targets))
        self._status_before = self._status()
        self._bytes_before = {t: t.read_bytes() for t in self.targets}

    def _status(self):
        p = subprocess.run(['git', 'status', '--porcelain'], cwd=str(REPO),
                           capture_output=True, text=True)
        return p.stdout

    # ── 落盘（**自证**：文件确实变了 且 新串在场）──────────────────
    def mutate(self, path, old, new, label, count=1):
        if path not in self.targets:
            raise HarnessError('变异目标 %s 不在本批的 targets 里 ⇒ 还原时会被漏掉' % path)
        before = path.read_text(encoding='utf-8')
        n = before.count(old)
        if n != count:
            raise HarnessError('%s: 锚点出现 %d 次（应为 %d）⇒ 锚点可能已过期' % (label, n, count))
        path.write_text(before.replace(old, new), encoding='utf-8')
        on_disk = path.read_text(encoding='utf-8')
        if on_disk == before:
            raise HarnessError('%s: 落盘自证失败（文件没变）' % label)
        if new and new not in on_disk:
            raise HarnessError('%s: 落盘自证失败（新串不在场）' % label)
        return before

    # ── 一条变异 ────────────────────────────────────────────────────
    def case(self, name, expect_red, must_contain=(), must_not_contain=(), mutate_fn=None,
             runner=None):
        """落盘 → **在变异状态下**跑判据 → 还原 → 核对。

        expect_red=True  ⇒ 判据必须**转红**（证明它有牙）
        expect_red=False ⇒ 判据必须**保持绿**（负控：语义等价的改动不该误报）
        runner           ⇒ 本条用例自己的判据运行器（不传则用批次默认的）
        """
        run = runner or self.runner
        before = {f: f.read_bytes() for f in self.targets}
        try:
            if mutate_fn is not None:
                mutate_fn()
            rc, out = run()
        finally:
            # ⚠ 还原必须在**跑完判据之后** ⇒ 否则测的是没变异的文件。
            for f, b in before.items():
                f.write_bytes(b)
                if f.read_bytes() != b:
                    raise HarnessError('%s: 还原自证失败（%s 没回到原样）' % (name, f.name))
        red = rc != 0
        ok = (red == expect_red)
        detail = 'exit=%d' % rc
        if ok and expect_red:
            missing = [m for m in must_contain if m not in out]
            if missing:
                ok = False
                detail += ' 缺: %s' % missing[:3]
        if ok and not expect_red:
            present = [m for m in must_not_contain if m in out]
            if present:
                ok = False
                detail += ' 不该出现: %s' % present[:3]
        self.record(name, ok, detail)
        return out

    def record(self, name, ok, detail=''):
        self.results.append((name, ok))
        print(('  ✅ ' if ok else '  ❌ ') + name + ('' if ok else ' —— ' + detail))

    # ── 收尾 ────────────────────────────────────────────────────────
    def finish(self, extra_runners=()):
        """收尾三条：判据还原后仍绿 / 目标文件逐字节回原样 / 工作树与前置快照一致。"""
        rc, out = self.runner()
        self.record('还原后判据必须仍然绿（exit 0）', rc == 0, 'exit=%d\n%s' % (rc, out[-800:]))
        for i, r in enumerate(extra_runners):
            rc, out = r()
            self.record('还原后附加判据 %d 必须仍然绿（exit 0）' % (i + 1), rc == 0,
                        'exit=%d\n%s' % (rc, out[-800:]))
        same = all(t.read_bytes() == self._bytes_before[t] for t in self.targets)
        self.record('还原后目标文件必须逐字节回原样', same)
        now = self._status()
        self.record('还原后工作树必须与前置快照一致（无变异残留）', now == self._status_before,
                    '前置:\n%s\n现在:\n%s' % (self._status_before, now))

    def summary(self):
        failed = [r for r in self.results if not r[1]]
        print('\n%s %s 变异 %d/%d 条通过' % (
            '✅' if not failed else '❌', self.title,
            len(self.results) - len(failed), len(self.results)))
        return 1 if failed else 0


# ── 判据运行器 ──────────────────────────────────────────────────────
def node_runner(test_file, name_pattern=None, cwd=None):
    """跑前端判据（裸 node --test + esbuild 预打包）。

    ⚠ `--test-name-pattern` 是**子串匹配**（node 把 describe 名与 it 名拼起来匹配）
      ⇒ 用来只跑本节的那一段，省掉整文件的时间。
    """
    cmd = ['node', '--experimental-strip-types', '--test']
    if name_pattern:
        cmd.append('--test-name-pattern=' + name_pattern)
    cmd.append(test_file)

    def run():
        p = subprocess.run(cmd, cwd=str(cwd or FRONTEND), capture_output=True,
                           text=True, timeout=900)
        return p.returncode, p.stdout + p.stderr
    return run


def go_runner(test_pattern, cwd=None):
    """跑 Go 侧判据（`go test -run`）。"""
    cmd = ['go', 'test', './internal/server/', '-run', test_pattern, '-count=1']

    def run():
        p = subprocess.run(cmd, cwd=str(cwd or BACKEND), capture_output=True,
                           text=True, timeout=900)
        return p.returncode, p.stdout + p.stderr
    return run


def go_pkg_runner(pkg, test_pattern, env=None, cwd=None, forbid_skip=False):
    """跑 Go 侧判据（可指定包），可选把「被 skip」当成**失败**。

    ⚠ **forbid_skip 的理由**（§935.4.2 的教训）：Go 的 `t.Skip` 之后
    退出码**仍是 0** ⇒ ⇒ 「用例被跳过」与「用例全过」在退出码上**完全同形**
    ⇒ ⇒ 一条依赖 PG 的判据在没有 PG 的机器上会**绿得毫无意义**。
    ⇒ ⇒ 本 runner 看到 `--- SKIP` 就返回非 0 ⇒ 批次要么真跑，要么拒绝跑。

    ⚠ env 用来注入 `POCKET_TEST_POSTGRES_DSN`（测试专用 DSN）。
    ⛔ **绝不**注入 `POCKET_POSTGRES_DSN` —— 那个会让测试打**生产库**。
    """
    cmd = ['go', 'test', pkg, '-run', test_pattern, '-count=1']

    def run():
        run_env = dict(os.environ)
        if env:
            run_env.update(env)
        p = subprocess.run(cmd, cwd=str(cwd or BACKEND), capture_output=True,
                           text=True, timeout=1800, env=run_env)
        out = p.stdout + p.stderr
        if forbid_skip and '--- SKIP' in out:
            return 4, out + '\n\n[mutations] 判据里有用例被 skip ⇒ 结论无效（退出码 0 同样是绿）\n'
        return p.returncode, out
    return run


def require_pg_dsn(var='POCKET_TEST_POSTGRES_DSN'):
    """取测试专用 PG DSN；**没有就抛**（而不是让它 skip 成「绿」）。

    ⚠ 为什么必须抛：§935.4.2 记着那道 Go 门在缺前端树时会 skip ⇒
    退出码 0 ⇒ 绿得毫无意义 ⇒ ⇒ 缺依赖时**拒绝跑**才是诚实做法。
    ⚠ ⛔ 只认 `POCKET_TEST_POSTGRES_DSN`（独立 schema + 自动 DROP），
    **绝不**碰 `POCKET_POSTGRES_DSN`（那是生产库，见 pg_store_test.go 的注释）。
    """
    dsn = os.environ.get(var, '').strip()
    if not dsn:
        raise HarnessError(
            '需要 %s（测试专用 PG DSN）才跑得动本批次。\n'
            '  ⛔ 不要设 POCKET_POSTGRES_DSN —— 那个会打生产库。\n'
            '  ⚠ 没有它 ⇒ 依赖 PG 的判据会被 skip ⇒ 退出码仍是 0 ⇒ 绿得毫无意义。\n'
            '  例：export %s=postgres://opk@127.0.0.1:55432/opk?sslmode=disable' % (var, var))
    return dsn


def run(batch, body, extra_runners=()):
    """批次统一入口：前置 → 跑批次主体 → 收尾 → 退出码。"""
    print('═══ %s ═══' % batch.title)
    batch.preflight()
    body()
    batch.finish(extra_runners)
    sys.exit(batch.summary())
