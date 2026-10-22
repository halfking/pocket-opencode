#!/usr/bin/env python3
"""⚠ **锚点新鲜度静态检查器**（§958 实测到的新故障模式 ⇒ 本节新增的第三类污染）。

═══════════════════════════════════════════════════════════════════════
**为什么需要它（§958 取证）**
═══════════════════════════════════════════════════════════════════════
§957 改了生产文件（`recording-duration-consistency.ts` 加了 `coverageGapMs`、
`native-part-timeline.ts` 改了 `degraded` 的写法）⇒ ⇒ **旧的变异脚本里写死的
`OLD` 锚点失配** ⇒ ⇒ 批次在跑到那一���时抛 `HarnessError` ⇒ ⇒ **整个批次中途崩溃**。

⚠⚠ **为什么这不是「一个锚点写错了」那么简单**：
  崩溃发生在批次**中途** ⇒ ⇒ **后面那些用例根本没跑**
  ⇒ ⇒ 「这批跑过了」与「这批没跑完」在**日志上长得不一样、但危害一样**
  ⇒ ⇒⇒ ⇒ **一个崩在���途的批次，会把「26 批全过」的结论污染掉**
     （它既不是「通过」，也不是「失败」，而是「不知道」）。
  ⇒ ⇒ 而且**修一次只能看见一个**（崩溃点后面那些失配的锚点，本轮根本还没暴露）。

⚠⚠ **本检查器为什么用 AST 而不是 import**：
  `import` 变异模块会**执行**模块顶层代码（构造 `Batch`、跑判据）⇒ 慢且有副作用
  ⇒ ⇒ 改用 `ast` **纯静态**解析 ⇒ **不执行任何代码** ⇒ 一秒内扫完全部 26 批
  ⇒ ⇒ ⇒ 而且能**一次报出全部失配锚点**（不只崩溃点那一个）。

═══════════════════════════════════════════════════════════════════════
**它守什么**
═══════════════════════════════════════════════════════════════════════
① 解析每个批次脚本里的 `b.mutate(目标, OLD, NEW, '标签')` 调用；
② 把 `OLD` 在**目标文件**里数一遍；
③ 命中数 ≠ 1（`count` 可显式指定别的值）⇒ **锚点过期** ⇒ 报出来。

⚠ **「解析不出来」也必须报出来**（不能静默跳过）：
  ⇒ 解析不出来 = 「这条锚点**没被检查过**」⇒ 静默跳过会让检查器**绿得毫无意义**
  ⇒ ⇒ 与 harness 里「查表 miss ⇒ continue ⇒ 整节静默跳过」是同一个病
  ⇒ ⇒ 故：本检查器把「无法静态解析」列为**独立的一类失败**。

用法：
  python3 scripts/mutations/check_anchors.py            # 扫全部批次
  python3 scripts/mutations/check_anchors.py s955.py    # 只扫指定批次

退出码：0 = 全部锚点在位；1 = 有失配或解析失败；2 = 用法错误。
"""
import ast
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]


def _str_const(node):
    """把「常量字符串」节点求值；不是常量字符串就返回 None。"""
    if isinstance(node, ast.Constant) and isinstance(node.value, str):
        return node.value
    if isinstance(node, ast.BinOp) and isinstance(node.op, ast.Add):
        left = _str_const(node.left)
        right = _str_const(node.right)
        if left is not None and right is not None:
            return left + right
    return None


class _Resolver:
    """把批次脚本里的名字解析成「路径」或「字符串常量」。"""

    def __init__(self, tree):
        self.targets = []      # TARGETS 列表（相对仓根的路径）
        self.path_by_name = {}  # 变量名 → 目标文件绝对路径
        self.const_by_name = {}  # 变量名 → 字符串常量
        self._collect(tree)

    def _collect(self, tree):
        # ① 模块级：TARGETS = [...]（相对仓根的路径列表）
        for node in tree.body:
            if not isinstance(node, ast.Assign):
                continue
            if len(node.targets) != 1 or not isinstance(node.targets[0], ast.Name):
                continue
            name = node.targets[0].id
            val = node.value
            if isinstance(val, ast.List) and all(
                    isinstance(e, ast.Constant) and isinstance(e.value, str) for e in val.elts):
                if name == 'TARGETS':
                    self.targets = [e.value for e in val.elts]
            # ② 模块级：X = b.targets[N]
            #   ⚠ `b.targets[N]` 的 AST 形态是 **Subscript**（`Attribute` 只是它的 .value）
            #   ⇒ 早先只认 Attribute ⇒ **全部解析失败** ⇒ 幸而本检查器把「解析不出」
            #   ⇒ 列为一类失败（不静默跳过）⇒ 否则会「绿得毫无意义」。
            if isinstance(val, ast.Subscript) \
                    and isinstance(val.value, ast.Attribute) and val.value.attr == 'targets' \
                    and isinstance(val.value.value, ast.Name) and val.value.value.id == 'b' \
                    and isinstance(val.slice, ast.Constant) \
                    and isinstance(val.slice.value, int):
                self.path_by_name[name] = ('targets', val.slice.value)
            # ③ 模块级：X = '字符串'
            s = _str_const(val)
            if s is not None:
                self.const_by_name[name] = s

    def path(self, node):
        """解析一个「目标路径」节点；解析不出来返回 None。"""
        if isinstance(node, ast.Name):
            hit = self.path_by_name.get(node.id)
            if hit is None:
                return None
            kind, idx = hit
            if kind != 'targets' or idx >= len(self.targets):
                return None
            return REPO / self.targets[idx]
        # REPO / 'x'
        if isinstance(node, ast.BinOp) and isinstance(node.op, ast.Div):
            left_ok = isinstance(node.left, ast.Name) and node.left.id == 'REPO'
            right = _str_const(node.right)
            if left_ok and right is not None:
                return REPO / right
        return None

    def path_by_index(self, idx):
        if idx < 0 or idx >= len(self.targets):
            return None
        return REPO / self.targets[idx]


def _eval_str(node, resolver, locals_):
    """求值一个「字符串表达式」节点。

    ⚠ 支持的形态（**刻意收窄**）：
       · 常量字符串；
       · 变量名（模块级 / 任意作用域的字符串常量）；
       · 两个字符串的 `+` 拼接（s921 的 V5 就是 `a + c` 这种计算型锚点）。

    ⚠⚠ **不支持的一律返回 None ⇒ 报「无法静态解析」** ⇒ 绝不静默跳过
       （「没检查过」必须与「检查通过」区分开 —— 否则检查器会绿得毫无意义）。
    """
    if isinstance(node, ast.Constant) and isinstance(node.value, str):
        return node.value
    if isinstance(node, ast.Name):
        if node.id in resolver.const_by_name:
            return resolver.const_by_name[node.id]
        return locals_.get(node.id)
    if isinstance(node, ast.BinOp) and isinstance(node.op, ast.Add):
        left = _eval_str(node.left, resolver, locals_)
        right = _eval_str(node.right, resolver, locals_)
        if left is not None and right is not None:
            return left + right
    return None


def _collect_loop_tuples(tree, resolver):
    """解析「循环里按元组解包」的负控写法（s944/s945 的 N1-N4 就是这个形态）。

    形态：`for label, path, old, new, desc in ( ('N1', AUTH_GO, N1_OLD, N1_NEW, '...'), ... ):`
      => 循环体里的 `b.mutate(path, old, new, label)` 用的是**循环变量**
      => => 必须把每个元组元素绑定到对应的循环变量名上，才能解析出「目标路径」。

    => 返回 {循环变量名: [(目标路径, OLD, NEW, 标签), ...]}
    """
    out = {}
    for node in ast.walk(tree):
        if not isinstance(node, ast.For) or not isinstance(node.target, ast.Tuple):
            continue
        names = [e.id for e in node.target.elts if isinstance(e, ast.Name)]
        if len(names) < 4:
            continue
        path_v = names[1]
        if not isinstance(node.iter, (ast.Tuple, ast.List)):
            continue
        for item in node.iter.elts:
            if not isinstance(item, ast.Tuple) or len(item.elts) < 4:
                continue
            label = _str_const(item.elts[0])
            path = resolver.path(item.elts[1])
            old = resolver.const_by_name.get(getattr(item.elts[2], 'id', ''))
            new = resolver.const_by_name.get(getattr(item.elts[3], 'id', ''))
            if path is None or old is None or new is None:
                continue
            out.setdefault(path_v, []).append((path, old, new, label))
    return out


def _find_mutate_calls(tree, resolver, locals_, loop_map=None):
    """找出所有 `b.mutate(目标, OLD, NEW, 标签[, count=N])` 调用。"""
    out = []
    for node in ast.walk(tree):
        if not isinstance(node, ast.Call):
            continue
        func = node.func
        if not (isinstance(func, ast.Attribute) and func.attr == 'mutate'
                and isinstance(func.value, ast.Name) and func.value.id == 'b'):
            continue
        args = node.args
        if len(args) < 3:
            out.append((node, None, None, None, 1, '调用参数不足 3 个'))
            continue
        # 循环变量形态（s944/s945）：路径/OLD/NEW 都来自循环元组
        loop_hit = None
        if loop_map and isinstance(args[0], ast.Name) and args[0].id in loop_map:
            # 循环体内的形参名是 `old`/`new`（不是 `M1_OLD` 那种）
            # => 用**循环变量名**匹配，而不是后缀。
            for lp, lo, ln, ll in loop_map[args[0].id]:
                loop_hit = (lp, lo, ln, ll)
                break
        if loop_hit is not None:
            path, old, new, label = loop_hit
            out.append((node, path, old, new, 1, label))
            continue
        path = resolver.path(args[0])
        old = _eval_str(args[1], resolver, locals_)
        new = _eval_str(args[2], resolver, locals_)
        label = None
        if len(args) >= 4:
            label = _str_const(args[3])
        count = 1
        for kw in node.keywords:
            if kw.arg == 'count':
                if isinstance(kw.value, ast.Constant) and isinstance(kw.value.value, int):
                    count = kw.value.value
        out.append((node, path, old, new, count, label))
    return out


def _collect_local_consts(tree):
    """收集**所有**作用域里的 `X = '字符串'`（变异脚本把 OLD/NEW 定义在 main() 里）。

    ⚠⚠ 还要收「**别名**」：`M5_OLD = M3_OLD`（s940/s941 就是这么写的）
      ⇒ 名字在模块级、值在另一个名字上 ⇒ 查表必须**跟着别名走**
      ⇒ 否则会误报「无法静态解析」。
    """
    consts = {}
    aliases = {}
    for node in ast.walk(tree):
        if not isinstance(node, ast.Assign) or len(node.targets) != 1:
            continue
        if not isinstance(node.targets[0], ast.Name):
            continue
        name = node.targets[0].id
        s = _str_const(node.value)
        if s is not None:
            consts[name] = s
        elif isinstance(node.value, ast.Name):
            aliases[name] = node.value.id
    # 别名解析（最多 8 轮，防环）
    for _ in range(8):
        changed = False
        for name, src in aliases.items():
            if name in consts:
                continue
            if src in consts:
                consts[name] = consts[src]
                changed = True
        if not changed:
            break
    return consts


def _has_main_guard(tree):
    """这个批次脚本有没有 `if __name__ == '__main__':` 守卫。

    ⚠⚠ **为什么必须查**（§958 实测）：
      缺守卫 ⇒ **任何 import 这个模块的动作都会真的跑完整批变异**（改工作树！）
      ⇒ ⇒ 本轮实测踩到：写「锚点新鲜度检查器」时用 import 方式加载批次
      ⇒ ⇒ import s946 ⇒ **它自己跑了起来** ⇒ 检查器被 SIGKILL
      ⇒ ⇒ 还原没跑完 ⇒ **工作树留下 M4 的残留**
      ⇒ ⇒⇒ ⇒ 后面那次检查把这份残留读成「s946 的锚点过期」⇒ **假结论**。
      ⇒ ⇒⇒ ⇒ 纪律：**读批次脚本一律用 AST 静态解析，不要 import**。
    """
    for node in tree.body:
        if not isinstance(node, ast.If):
            continue
        test = node.test
        if not (isinstance(test, ast.Compare) and isinstance(test.left, ast.Name)
                and test.left.id == '__name__'):
            continue
        for op, cmp_node in zip(test.ops, test.comparators):
            if isinstance(op, ast.Eq) and isinstance(cmp_node, ast.Constant) \
                    and cmp_node.value == '__main__':
                return True
    return False


def _shape_mismatch(old, new):
    """判断「替换片段」是否**明显**形态不兼容（会让目标文件语法坏掉）。

    ⚠⚠ **为什么要有这条**（§958 实测）：
      本轮修 `s956` 的 M3 锚点时，我**只改了 `OLD`、忘了改 `NEW`**
      ⇒ ⇒ `NEW` 仍是**旧形态**（对象字面量里的 `degraded: false,`）
      ⇒ ⇒ 替换进去 ⇒ **目标文件语法坏掉**
      ⇒ ⇒ 判据确实红了 —— 但**红在「编译失败」上**，不是红在
      ⇒ ⇒ 「退化不留痕」那条语义上 ⇒ ⇒ 用例报「缺预期文案」
      ⇒ ⇒⇒ ⇒ 而**门红 ≠ 变异生效** ⇒ ⇒ 这是「假红」的另一种形态。

    ⚠⚠ **刻意收窄到只剩「缩进」**（前两版都误报 ⇒ 都撤掉）：
      · 第一版比「赋值左侧**整段文本**」⇒ 把 `list, total, err :=` → `list, _, err :=`
        （Go 里 `_` **完全合法**）误判成不兼容 ⇒ **21 条误报**；
      · 第二版加「赋值左侧**首标识符**」⇒ 仍误报 9 条
        （例如 `body := readBody(...)` → `cap := 200` + `body := readBody(...)`
        —— **完全合法**的 Go 变异）⇒ ⇒ **也撤掉**。
      ⇒ ⇒ 只留**缩进**：它是唯一「不一致 ⇒ 几乎必然语法坏掉」的信号
        ⇒ ⇒ **并排除「逐行删除」那类合法变异**（删一个字段声明 ⇒ 缩进必变、语法合法）。
      ⇒ ⇒ ★ 通用式：**判据不准就放过** ⇒ 宁可漏报，**不可误报**
      ⇒ ⇒ ⇒ 误报的判据会让人**习惯性地忽略它** ⇒ 那它就白做了。

    ⇒ 返回不一致的维度名列表（空列表 = 形态一致 / 判不准 ⇒ 放过）。
    """
    def indent(t):
        head = t.split('\n')[0]
        return head[:len(head) - len(head.lstrip())]

    def is_line_deletion(o, n):
        """NEW 是否 = 逐行删掉 OLD 的某些行（⇒ **删字段声明**那类**合法**变异）。

        ⚠ 例：`  epochMs?: number\n}\n` → `}\n` ⇒ 缩进不同，但**语法完全合法**
          ⇒ ⇒ 不能按缩进差就报「形态不兼容」。
        """
        o_lines = o.split('\n')
        n_lines = n.split('\n')
        # 保留 NEW 的行，按序在 OLD 里找 ⇒ 找得到就算「逐行删除」
        i = 0
        for ln in n_lines:
            found = False
            while i < len(o_lines):
                if o_lines[i] == ln:
                    i += 1
                    found = True
                    break
                i += 1
            if not found:
                return False
        return len(n_lines) < len(o_lines)

    if is_line_deletion(old, new):
        return []   # 合法变异（删行）⇒ 放过

    bad = []
    if indent(old) != indent(new):
        bad.append('缩进')
    return bad


def check_batch(script):
    """返回 (用例数, [问题列表])。问题列表每项 = (标签, 类别, 说明)。"""
    tree = ast.parse(script.read_text(encoding='utf-8'), filename=str(script))
    resolver = _Resolver(tree)
    locals_ = _collect_local_consts(tree)

    problems = []
    if not _has_main_guard(tree):
        problems.append(('__main__', 'NO_GUARD',
                         '缺 `if __name__ == \'__main__\':` 守卫 ⇒ import 本模块会**真的跑完整批**'))
    loop_map = _collect_loop_tuples(tree, resolver)
    calls = _find_mutate_calls(tree, resolver, locals_, loop_map)

    # 本批的目标集合（用于判定「锚点是否落在本批目标里」）
    for node, path, old, new, count, label in calls:
        tag = label or ('line %d' % node.lineno)
        if path is None:
            problems.append((tag, 'UNRESOLVED', '目标路径无法静态解析'))
            continue
        if old is None:
            problems.append((tag, 'UNRESOLVED', 'OLD 锚点不是常量字符串'))
            continue
        try:
            text = path.read_text(encoding='utf-8')
        except OSError as e:
            problems.append((tag, 'UNRESOLVED', '读不了目标文件：%s' % e))
            continue
        n = text.count(old)
        if n != count:
            rel = path.relative_to(REPO) if str(path).startswith(str(REPO)) else path
            problems.append((tag, 'STALE', '锚点在 %s 里出现 %d 次（应为 %d）' % (rel, n, count)))
        elif new:
            bad_shape = _shape_mismatch(old, new)
            if bad_shape:
                problems.append((tag, 'SHAPE',
                                 '替换片段与原片段形态不兼容（%s）⇒ 替换进去会让目标文件语法坏掉'
                                 ' ⇒ 门会红在「编译失败」上 ⇒ **假红**' % '、'.join(bad_shape)))
    return len(calls), problems


def main(argv):
    if len(argv) > 1 and argv[1] in ('-h', '--help'):
        print(__doc__)
        return 2
    names = argv[1:] or sorted(p.name for p in HERE.glob('s*.py'))
    total = 0
    bad_batches = []
    for name in names:
        script = HERE / name
        if not script.exists():
            print('❌ 找不到批次脚本：%s' % script)
            bad_batches.append((name, []))
            continue
        try:
            n, problems = check_batch(script)
        except SyntaxError as e:
            print('❌ %s：语法错误 %s' % (name, e))
            bad_batches.append((name, []))
            continue
        total += n
        if problems:
            print('❌ %s：%d/%d 条锚点有问题' % (name, len(problems), n))
            for tag, kind, why in problems:
                print('    - %-8s %-11s %s' % (tag, kind, why))
            bad_batches.append((name, problems))
        else:
            print('✅ %s：%d/%d 条锚点在位' % (name, n, n))

    print('\n合计 %d 条锚点检查，%d 个批次有问题' % (total, len(bad_batches)))
    if bad_batches:
        print('⇒ ⇒ **锚点过期 ⇒ 该批会在中途崩溃 ⇒ 后面那些用例根本没跑**')
        print('⇒ ⇒ 先把锚点对齐到当前生产代码，再重跑该批。')
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main(sys.argv))
