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
import re
import subprocess
import sys
import tempfile
from pathlib import Path

# ⚠ 路径一律从 __file__ 推导 ⇒ 仓内不得出现任何机器相关的绝对路径
#   （`check:no-hardcoded-paths` 那道门的道理：写死的路径在别的机器上是死路）。
REPO = Path(__file__).resolve().parents[2]
FRONTEND = REPO / 'frontend'
BACKEND = REPO / 'backend'

# ⚠⚠ §957：**变异专用的 Go 构建缓存**（与交互式 `go test` 隔离）。
#
#   ⚠⚠ **事实边界（不许把未复现的东西写成已证结论）**：
#     本轮实测到的**是症状**（「该红却绿」：s939 的 M7），**根因未唯一确定** ⇒ 两候选：
#     ① **两份跑批并发** ⇒ 抢同一个被变异文件 ⇒ `go test` 编译时读到的可能
#        已经是**另一份跑批还原后**的原文 ⇒ 变异没进二进制 ⇒ 门照样绿
#        （⇒ 证据：s942/s944 的「残留检查」看到的是**对方**的文件）；
#     ② **构建缓存命中了内容对不上的旧产物**（Go 缓存按内容寻址，理论上不该发生
#        ⇒ ⛔ **未复现** ⇒ 不作为已证结论）。
#   ⇒ ⇒ 因此**两条都堵**：单实例守卫（治 ①，见 README）+ 专属缓存 & 「换新缓存复跑」（治 ②）。
#
#   ⇒ ⇒ 交互式 `go test` 与跑批**不再共用缓存目录** ⇒ 交互侧污染不到它。
#   ⚠ 目录名带 `-v2`：上一份（无后缀）曾在并发跑批期间被写过 ⇒ ⛔ 保留不删
#     （可恢复删除闸拦 `rm`）⇒ 改用新名 ⇒ 本轮跑批从**未被写过**的缓存起步。
# ⚠⚠⚠ §958：**位置选错 ⇒ 系统会把它清掉**（本轮实测到的一次真实事故）
#
#   ⚠⚠ **实测**：本轮跑批时 `s949` 整批 10 条红项，根因是
#     `link: cannot open file .../opk-mutation-gocache-v2/6b/6b6ec...-d:
#      no such file or directory`
#     ⇒ ⇒ **linker 正在读那个缓存条目时，它被删了**
#     ⇒ ⇒ 目录本身还在、mtime 变过、大小从 **535 M 掉到 55 M**
#     ⇒ ⇒ ⇒ **macOS 的临时目录清理器**（`/var/folders/…/T/`，实测 11 900+ 项）
#     ⇒ ⇒ ⇒ **会删掉这里的东西**（Go 官方缓存就不放这儿：它在 `~/Library/Caches/go-build`）。
#
#   ⇒ ⇒ **处置**：专属缓存搬到 **`~/.cache/openpocket-mutations/gocache-v3`**
#     ⇒ ⇒ 与系统清理区**物理隔离** ⇒ ⇒ 与交互式 `go test` 仍然隔离（另一个维度）。
#
#   ⚠ 目录名带 `-v3`：前一份（`-v2`，在临时目录里的那份）已被系统清理过
#     ⇒ ⛔ **不删**（可恢复删除闸拦 `rm`）⇒ 改用新名 ⇒ 从**没被写过**的缓存起步。
MUTATION_GOCACHE = os.path.join(
    os.path.expanduser('~'), '.cache', 'openpocket-mutations', 'gocache-v3')

# ⚠⚠⚠ §958：**「构建环境坏了」的特征**（本轮实测到的一次真实事故）
#
#   ⇒ 症状：linker 读缓存条目时那个条目**被删了**
#     `link: cannot open file …/opk-mutation-gocache-v2/6b/…-d: no such file or directory`
#   ⇒ 或：包编译失败（`FAIL … [build failed]`）
#
#   ⇒ ⇒ 为何必须单独认出来：**退出码非 0 在 harness 眼里 = 「门红」**
#     ⇒ ⇒ 而「门红」的下一步动作是**改判据**（拆门）
#     ⇒ ⇒⇒ ⇒ 而这次的红**根本不是判据的问题** ⇒ ⇒ 必须挡住这条误读。
_BUILD_ENV_BROKEN_RE = re.compile(
    r'cannot open file .*gocache|no such file or directory'
    r'|\[build failed\]|compile: |cannot find package|'
    r'cache entry not found|failed to load cache',
    re.IGNORECASE)


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

    def _dirty_targets(self):
        """本批目标文件里，哪些**相对 HEAD 不干净**（⇒ 还带着变异）。

        ⚠ 用 `git diff --quiet HEAD -- <file>` 判（**不看工作树其它文件**）
          ⇒ ⇒ 别的会话改的别的文件**不会**误报 ⇒ ⇒ 只盯本批的目标。
        """
        dirty = []
        for t in self.targets:
            p = subprocess.run(['git', 'diff', '--quiet', 'HEAD', '--', str(t)],
                               cwd=str(REPO), capture_output=True, text=True)
            if p.returncode != 0:
                dirty.append(str(t.relative_to(REPO)))
        return dirty

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

        ⚠⚠ **「没让门红」有两种可能，「门没牙」只是其中一种**（§957 实测）：
          ① 门没牙（判据量不到那条语义）；
          ② ⛔ **变异没进二进制** —— 构建缓存命中了旧产物，**或**别的跑批
            把文件还原了 ⇒ 编译到的不是被变异的那份源码 ⇒ 门照样绿。
            ⇒ 若直接下结论「门没牙」，就会**改判据去迁就一个没生效的变异**。
            ⇒⇒⇒ 正确处置：**换一份全新缓存再跑一次**，用第二次的结果下结论。
          ⇒ 本 harness 因此在「该红却绿」时**自动用全新缓存复跑**（若运行器支持）。
        """
        run = runner or self.runner
        # ⚠⚠⚠ §958：**「本条开始前的快照」本身可能已经是脏的**
        #   本轮实测到的一次真实事故（`s952` 的 N2 负控在跑批里红了，
        #   独立复跑却 27/27 全绿）：
        #   · N2 的锚点（`body, err := sizedread.All(resp.Body, …)`）是
        #     **N1 与 M6 的锚点的真子串** ⇒ 三条变异**共用同一行文本**；
        #   · ⇒ 于是「前序变异没被完全还原」时，N2 撞上的是**别人的形态**
        #     ⇒ 实测「M6 态 + N2 态」叠加 ⇒ 判据红 + N2 的 must_not_contain
        #       标记出现 ⇒ **精确复现了跑批里的现象**。
        #
        #   ⚠⚠ **检查的位置**：必须是「**本条开始前**」⇒ 因为还原是按**这个快照**
        #     写回的 ⇒ ⇒ 若快照已脏 ⇒ 还原会把脏一起带下去 ⇒ ⇒ 本条量到的
        #     **不是它自己那个变异** ⇒ ⇒ 而本条自己**看不出**异常（它量到的是对的）。
        #   ⇒ ⇒ ⇒ 而「快照脏」这件事，**只能由下一条用例来发现**
        #   ⇒ ⇒ ⇒ 所以每条用例**开始时**都要查一次：目标文件相对 HEAD 必须干净。
        #   ⇒ ⇒ ⇒ 这一查把「前序残留」**当场**变成一条明确的错误，
        #     而不是「留到后面那条用例去撞、然后误读成『负控写错了』」。
        dirty_before = self._dirty_targets()
        if dirty_before:
            raise HarnessError(
                '%s: 本条**开始前**目标文件相对 HEAD 就不干净 ⇒ 前序用例有残留'
                ' ⇒ 本条量到的可能不是它自己的变异：\n  %s'
                % (name, '\n  '.join(dirty_before)))
        before = {f: f.read_bytes() for f in self.targets}
        stale_cache_note = ''
        try:
            if mutate_fn is not None:
                mutate_fn()
            rc, out = run()
            # ⚠⚠⚠ §958：「构建环境坏了」⇒ **不是门红** ⇒ 换新缓存复跑
            #   （退出码 5 = `_go_runner` 认出的构建环境故障）
            if rc == 5:
                fresh = getattr(run, 'with_fresh_cache', None)
                if callable(fresh):
                    rc2, out2 = fresh()
                    if rc2 != 5:
                        stale_cache_note = ('（首次红是**构建环境坏了**（缓存条目被系统清理）'
                                            '⇒ 换全新缓存复跑后转红）')
                        rc, out = rc2, out2
            if expect_red and rc == 0:
                # ⛔ 「该红却绿」⇒ 先排除「变异没进二进制」⇒ 再谈「门没牙」。
                fresh = getattr(run, 'with_fresh_cache', None)
                if callable(fresh):
                    rc2, out2 = fresh()
                    if rc2 != 0:
                        stale_cache_note = '（首次绿是**变异没进二进制** ⇒ 换全新缓存复跑后转红）'
                        rc, out = rc2, out2
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
        if ok and stale_cache_note:
            detail += ' ' + stale_cache_note
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


def _go_runner(cmd, env=None, cwd=None, forbid_skip=False, timeout=1800):
    """Go 侧判据运行器的公共实现（**专属缓存** + **可换新缓存复跑**）。

    ⚠⚠ **为什么必须带「专属缓存」**（§957 实测到的**症状**）：
      「该红却绿」有两种可能 —— ① 门没牙；② ⛔ **变异没进二进制**
      （构建缓存命中旧产物，**或**别的跑批把文件还原了 ⇒ 编译到的不是被变异的那份源码）。
      ⇒⇒⇒ ② 一旦被误读成 ①，就会**改判据去迁就一个没生效的变异**。
      ⇒ ⇒ 本 runner ① 默认用一份**专属缓存**（与交互式 `go test` 隔离）；
      ⇒ ⇒ ② 暴露 `with_fresh_cache` ⇒ `Batch.case` 在「该红却绿」时自动换新缓存复跑。
    """
    counter = {'n': 0}

    def _run_with(cache):
        run_env = dict(os.environ)
        run_env['GOCACHE'] = cache
        if env:
            run_env.update(env)
        p = subprocess.run(cmd, cwd=str(cwd or BACKEND), capture_output=True,
                           text=True, timeout=timeout, env=run_env)
        out = p.stdout + p.stderr
        if forbid_skip and '--- SKIP' in out:
            return 4, out + '\n\n[mutations] 判据里有用例被 skip ⇒ 结论无效（退出码 0 同样是绿）\n'
        # ⚠⚠⚠ §958：**构建环境坏了 ⇒ 不是「门红」**（本轮实测到的第三类）
        #   症状：`link: cannot open file .../opk-mutation-gocache-v2/6b/…-d:
        #         no such file or directory`
        #   ⇒ ⇒ 那是**缓存条目在 linker 读它的时候被删了**
        #   ⇒ ⇒⇒ ⇒ 若不认出来，就会被当成「门红 ⇒ 判据有问题 ⇒ 改判据」
        #   ⇒ ⇒⇒ ⇒ 而**下一步动作是改判据** ⇒ ⇒⇒ ⇒ 那是**拆门** ⇒ ⇒ 必须挡住。
        if _BUILD_ENV_BROKEN_RE.search(out):
            return 5, out + ('\n\n[mutations] ⚠️ **构建环境坏了**（缓存条目消失 / 编译失败）'
                             '\n  ⇒ ⇒ 这**不是**「门红」⇒ ⇒ **不要**据此改判据'
                             '\n  ⇒ ⇒ 请换一份全新缓存复跑（with_fresh_cache）再下结论。\n')
        return p.returncode, out

    def run():
        return _run_with(MUTATION_GOCACHE)

    def with_fresh_cache():
        """换一份**真正全新**的缓存再跑一次（用来排除「变异没进二进制」）。

        ⚠ 每调用一次换一个**新目录** ⇒ 同一次跑批里的第二次调用也是「全新」
        ⇒ 否则「第一次 fresh 之后目录里已有产物」⇒ 第二次就退化成普通复跑。
        """
        counter['n'] += 1
        fresh = os.path.join(
            os.path.dirname(MUTATION_GOCACHE),
            'gocache-fresh-%d-%d' % (os.getpid(), counter['n']))
        os.makedirs(fresh, exist_ok=True)
        return _run_with(fresh)

    run.with_fresh_cache = with_fresh_cache
    return run


def go_runner(test_pattern, cwd=None):
    """跑 Go 侧判据（`go test -run`，server 包）。"""
    cmd = ['go', 'test', './internal/server/', '-run', test_pattern, '-count=1']
    return _go_runner(cmd, cwd=cwd, timeout=900)


def go_pkg_runner(pkg, test_pattern, env=None, cwd=None, forbid_skip=False):
    """跑 Go 侧判据（可指定包），可选把「被 skip」当成**失败**。

    ⚠ **forbid_skip 的理由**（§935.4.2 的教训）：Go 的 `t.Skip` 之后
      退出码**仍是 0** ⇒ ⇒ 「用例被跳过」与「用例全过」在退出码上**完全同形**
      ⇒ ⇒ 一条依赖 PG 的判据在没有 PG 的机器上会**绿得毫无意义**。
      ⇒ ⇒ 本 runner 看到 `--- SKIP` 就返回非 0 ⇒ 批次要么真跑，要么拒绝跑。

    ⚠ env 用来注入 `POCKET_TEST_POSTGRES_DSN`（测试专用 DSN）。
      ⛔ **绝不**注入 `POCKET_POSTGRES_DSN` —— 那个会让测试打**生产库**。

    ⚠ 缓存隔离与「换新缓存复跑」见 `_go_runner` 的注释（§957 实测到的症状）。
    """
    cmd = ['go', 'test', pkg, '-run', test_pattern, '-count=1']
    return _go_runner(cmd, env=env, cwd=cwd, forbid_skip=forbid_skip)


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
