#!/usr/bin/env bash
# =====================================================================
# doctor.sh — opencode-pocket 服务自检（rule 22 §9）
#
# 用法: ./doctor.sh
# 返回: 0 = 全部通过, 1 = 检查失败
#
#       ./doctor.sh --selftest
#       对自检脚本自身做回归（详见文件末尾）。这条不是装饰：
#       历史上计数器写成 `((PASS++))` 时，本脚本**恒定只跑完第 1 条就退出**
#       且**永远返回 1**，于是「我们有自检」这件事本身就是假的。
# =====================================================================

set -euo pipefail

# ── 自检（--selftest） ─────────────────────────────────────────────
#
# 守的是「doctor.sh 自己别再坏掉」。判据与仓内 check-*.mjs --selftest 同构：
# 先证量具能用，再验被测行为。
doctor_selftest() {
  local fails=0
  say() { echo "  $1"; }

  # ① 量具自证 + 故障复现。
  #    ⚠️ 必须复刻**真实的触发形态**：`check_*` 总是作为
  #    `[[ 条件 ]] && check_pass || check_warn` 列表的**最后一个**命令被调用。
  #    单独跑 `((C++))` 不会触发 set -e（实测），拿它当反证等于什么都没验。
  say "① 量具自证 + 故障复现（真实形态：check_* **函数**位于 &&/|| 列表末尾）"
  # ⚠️ 必须用**函数**，不能用 `{ ((C++)); }` 这种 brace group —— 实测两者行为不同：
  #    函数调用作为列表末位 ⇒ 终止（历史故障）；
  #    brace group 作为列表末位 ⇒ 组**内部**的命令被 AND-OR 豁免罩住 ⇒ 不终止。
  #    拿 brace group 当反证，等于验了一个现实中不存在的路径。
  if bash -c 'set -euo pipefail; W=0; cw() { ((W++)); }; [[ -f /nonexistent ]] && echo y || cw; echo alive' >/dev/null 2>&1; then
    say "   ❌ 故障形态没能复现（AND-OR 规则若已变，反证与本判据都要重估）"; fails=$((fails+1))
  else
    say "   ✅ 故障形态已复现：check_* 函数位于 &&/|| 列表末位时，((C++)) 会终止 \`set -e\` 脚本"
  fi
  if bash -c 'set -euo pipefail; W=0; cw() { W=$((W+1)); }; [[ -f /nonexistent ]] && echo y || cw; echo alive' >/dev/null 2>&1; then
    say "   ✅ 修复形态在同一位置存活 ⇒ 赋值式计数器确实解除了该故障"
  else
    say "   ❌ 修复形态仍被终止（判据失效）"; fails=$((fails+1))
  fi
  # 对照：证明触发点是「函数作为列表末位」，而不是算术命令本身。
  if bash -c 'set -e; C=0; ((C++)); echo alive' >/dev/null 2>&1; then
    say "   ℹ️  对照：单独位置的 ((C++)) 不触发 set -e ⇒ 触发点是 AND-OR 末位 + 函数"
  else
    say "   ⚠️  对照：本机 bash 下单独位置也会终止 ⇒ 触发机制与注释描述不同，需重新核实"
  fi

  # ② 本文件的计数器必须是赋值式。
  #    ⚠️ 三个坑都在这一条里踩过，逐个说明：
  #      a) 必须 grep **坏形式** `((VAR++))`。第一版 grep 的是赋值形式，
  #         抓的本来就是正确写法 ⇒ 判据恒绿（自己踩了一次恒真判据）。
  #      b) 必须**排除注释行**：文件头要把坏形式写出来给人看。
  #      c) 必须**排除 selftest 函数自身的代码**：它为了做变异注入，
  #         必须在文本里出现坏形式，否则 ② 会被自己绊倒。
  #         ⇒ 统一做法：只扫描 `doctor_selftest() {` 之前的正文。
  say "② 本文件的计数器实现（正文中不得出现 ((VAR++))）"
  local body
  # 取出「除 selftest 函数体以外」的全部正文。
  # ⚠️ 不能用「selftest 之前的部分」—— doctor_selftest 定义在文件靠前处，
  #    而 check_* 三个定义在它**后面**，那样会把被检对象本身排除掉 ⇒ 判据恒绿。
  #    （第一版就是这么写的，又一次恒真。）
  body=$(awk '
    /^doctor_selftest\(\) \{/ { skip=1 }
    skip && /^\}/ { skip=0; next }
    skip { next }
    { print }
  ' "$0")
  # ⚠️ 括号必须显式转义：写成未转义的 ((
  #    会被 grep 当成分组起始，报 "parentheses not balanced" 而 grep 直接失败，
  #    `|| true` 又把失败吞掉 ⇒ 判据恒绿。第一版就是这么错的。
  #
  # ⚠️ 必须滤掉**注释行**：本文件的文件头正要把坏形式写出来给人看，
  #    那是文档不是代码。第一版没滤，于是自检被自己的说明绊倒。
  strip_code() { grep -vE '^[[:space:]]*#' || true; }
  local counter_hits
  counter_hits=$(printf '%s\n' "$body" | strip_code | grep -nE '\(\((PASS|FAIL|WARN)\+\+\)\)' || true)
  if [[ -z "$counter_hits" ]]; then
    say "   ✅ 正文里未检出 ((VAR++))"
  else
    say "   ❌ 检出非赋值式计数器（会终止脚本）："; echo "$counter_hits" | sed 's/^/      /'; fails=$((fails+1))
  fi

  # ②b 反证：判据本身必须能抓到坏形式，否则 ② 是装饰。
  say "②b 判据自证（变异注入：确认 ② 真能抓到坏形式）"
  local mutated injected good_form
  mutated=$(mktemp -t doctor_mutant.XXXXXX)
  # 把正文里的 check_pass 定义换成坏形式，再扫一遍。
  # 两段拼接：好形式/坏形式各存一个变量，sed 里只引用变量，
  # 免得 $(...) / 反斜杠在单引号里打架（第一版就这么写崩了）。
  good_form='PASS=$((PASS+1))'
  bad_form='PASS=$((PASS++))'
  printf '%s\n' "$body" | sed "s|${good_form}|${bad_form}|" > "$mutated"
  injected=$(grep -vE '^[[:space:]]*#' "$mutated" | grep -nE '\(\((PASS|FAIL|WARN)\+\+\)\)' || true)
  if [[ -n "$injected" ]]; then
    say "   ✅ 注入的坏形式被 ② 抓到了（变异生效）"
  else
    say "   ❌ 注入的坏形式没被 ② 抓到 ⇒ ② 的正则抓错了东西（本判据恒绿）"; fails=$((fails+1))
  fi
  rm -f "$mutated"

  # ③ 全量变量展开的传染性扫描：$VAR 紧跟非 ASCII 字符会让 bash
  #    把那些字节算进变量名（本文件就踩过：\$DOCKER_DB_DATA_DIR（）。
  say "③ 变量后紧跟非 ASCII 字符的隐患"
  local bad_lines
  bad_lines=$(grep -nE '\$[A-Za-z_][A-Za-z0-9_]*[^A-Za-z0-9_ "/:$`<=+(){}\[\]!&|;,.\t-]' "$0" \
              | grep -vE '\$\{' | grep -E '[^ -~]' || true)
  if [[ -n "$bad_lines" ]]; then
    say "   ❌ 检出未用 \${} 界定、且紧跟非 ASCII 的变量展开："
    echo "$bad_lines" | sed 's/^/      /'
    fails=$((fails+1))
  else
    say "   ✅ 未检出"
  fi

  echo "  自检失败项: $fails"
  return "$fails"
}

if [[ "${1:-}" == "--selftest" ]]; then
  doctor_selftest
  exit $?
fi

PASS=0; FAIL=0; WARN=0

# ⚠️⚠️ 2026-10-17：这三个计数器原先写成 `((PASS++))`，配合本文件统一的
#      `[[ 条件 ]] && check_pass "…" || check_warn "…"` 写法，会让脚本**只跑完第 1 条检查就退出**。
#
# 机制（第一版这里写错了一步，2026-10-17 实测逐级更正）：
#   1. `((expr))` 的退出码取决于表达式求值：值为 0 时退出码为 **1**。
#      `((VAR++))` 是后置自增，表达式取自增前的值 ⇒ 第一次调用时 PASS/WARN/FAIL 都为 0。
#   2. **但单独写 `((C++))` 并不会触发 `set -e`**（实测：脚本继续执行）。
#      —— 我最初误判为「算术命令返回 1 就终止」，这是错的。
#   3. 真正的触发条件是 bash 的 AND-OR 规则：`set -e` **不作用于** `a && b || c` 列表中
#      除最后一个命令以外的部分。`check_warn` 是**函数调用**，恰好位于列表末尾 ⇒
#      它返回的 1 被当作脚本失败，脚本当场终止。
#      ⚠️ 且必须是**函数**：同样的 `((C++))` 放进 brace group `{ …; }` 作为列表末位时
#      **不会**终止（组内部的命令被 AND-OR 豁免罩住）。这个差别是实测出来的，
#      写反了会让自检去验一条现实中不存在的路径。
#   4. 本文件每一处检查都是这个写法 ⇒ 必然在第 1 条就死。
#
# 实测后果（修复前）：`./doctor.sh` 执行 15+ 条检查中的 **1 条**，且**恒定返回退出码 1**。
# 因为它永远说「失败」，使用者很快学会忽略它 —— 一个恒坏的自检工具比没有自检更糟：
# 它占着「我们有自检」的位置，让真正的配置问题无人发现。
#
# ⇒ 改用 `VAR=$((VAR+1))`：赋值语句的退出码**恒为 0**，与 AND-OR 规则、是否函数无关。
check_pass() { echo "  ✅ $1"; PASS=$((PASS+1)); }
check_fail() { echo "  ❌ $1"; FAIL=$((FAIL+1)); }
check_warn() { echo "  ⚠️  $1"; WARN=$((WARN+1)); }

echo "━━━ doctor: opencode-pocket ━━━"

# ── 1. 配置完整性 ──────────────────────────────────────────────────
[[ -f .env ]] && check_pass ".env 存在" || check_warn ".env 不存在（cp .env.example .env）"
[[ -f .env.example ]] && check_pass ".env.example 存在" || check_fail ".env.example 缺失"

# ── 2. 构建文件完整性 ─────────────────────────────────────────────
[[ -f Dockerfile ]] && check_pass "Dockerfile 存在" || check_fail "Dockerfile 缺失"
[[ -f README.md ]] && check_pass "README.md 存在" || check_warn "README.md 缺失"

# ── 3. deploy/ 完整性 ──────────────────────────────────────────────
if [[ -d deploy ]]; then
  check_pass "deploy/ 存在"
  for script in deploy.sh verify.sh rollback.sh build-image.sh; do
    [[ -f "deploy/$script" ]] && check_pass "deploy/$script 存在" || check_fail "deploy/$script 缺失"
  done
else
  check_fail "deploy/ 缺失"
fi

# ── 4. 本地配置（rule 21） ──────────────────────────────────────────
[[ -f LOCAL_CONFIG.md.template ]] && check_pass "LOCAL_CONFIG.md.template 存在" || check_warn "LOCAL_CONFIG.md.template 缺失"
# 如果 LOCAL_CONFIG.md 存在，检查权限
if [[ -f LOCAL_CONFIG.md ]]; then
  mode=$(stat -c %a LOCAL_CONFIG.md 2>/dev/null || stat -f %Lp LOCAL_CONFIG.md)
  [[ "$mode" == "600" ]] && check_pass "LOCAL_CONFIG.md 权限 600" || check_warn "LOCAL_CONFIG.md 权限 ${mode}（建议 600）"
fi

# ── 5. 端口检查（可选） ────────────────────────────────────────────
# PORT=${PORT:-8080}
# if lsof -i :$PORT &>/dev/null 2>&1; then
#   check_pass "端口 $PORT 已监听"
# else
#   check_warn "端口 $PORT 未监听（服务未运行？）"
# fi

# ── 6. Docker 数据目录属主（2026-10-17 新增） ──────────────────────────
#
# 为什么要有这条：`docker-compose.db.yml` 把 PGDATA 绑到宿主目录。
# 镜像内 postgres 是 uid 70；宿主目录若是别的属主，首次启动时
# initdb 的临时 `pg_ctl` 会以 postgres 身份起不来 → 镜像入口放弃重试
# 并「目录已有数据库，跳过初始化」⇒ **POSTGRES_DB 根本没被创建**，
# 而 healthcheck 过去用 pg_isready（对不存在的库也返回 0）照样报 healthy。
#
# 现在 healthcheck 改用真连库，会转红；本条负责**事前**把它拦下来，
# 因为那个故障的真实线索埋在 `docker logs` 里，现场极难定位。
if [[ -n "${DOCKER_DB_DATA_DIR:-}" ]]; then
  if [[ ! -d "$DOCKER_DB_DATA_DIR" ]]; then
    # ⚠️ 必须写成 ${VAR}：紧跟全角「（」时 bash 会把「（」的字节当成变量名的一部分，
    #    报 `VAR: unbound variable`（本次首次实现时真的踩到了）。
    check_warn "DOCKER_DB_DATA_DIR 不存在：${DOCKER_DB_DATA_DIR}（compose 会自建，但属主将是当前用户）"
  else
    owner_uid=$(stat -c %u "$DOCKER_DB_DATA_DIR" 2>/dev/null || stat -f %u "$DOCKER_DB_DATA_DIR" 2>/dev/null || echo "")
    if [[ "$(uname -s)" == "Darwin" ]]; then
      # macOS 的 bind mount 走 Docker Desktop 的文件共享层，容器内 chown 不生效
      # （chown 需 sudo 且仍可能被忽略）。实测结论：这种环境下**必须**用
      # Docker 命名卷，否则首次启动会跳过建库。
      check_warn "macOS + bind mount：PGDATA 属主 ${owner_uid:-?} ≠ 容器内 postgres(70)，且容器内 chown 不生效。"
      check_warn "  ⇒ 首次启动可能跳过创建 POSTGRES_DB。改用 Docker 命名卷可正常建库；"
      check_warn "    若已踩坑，删掉该目录重建即可（会丢数据，先备份）。"
    elif [[ "$owner_uid" == "70" ]]; then
      check_pass "PGDATA 属主 70（与容器内 postgres 一致）"
    else
      check_fail "PGDATA 属主 ${owner_uid:-?} ≠ 70（容器内 postgres 的 uid）⇒ initdb 起不来，POSTGRES_DB 会被跳过创建。修：sudo chown 70:70 ${DOCKER_DB_DATA_DIR}"
    fi
  fi
else
  # compose 里该变量是必填的，没设就说明尚未进入容器化部署
  check_warn "DOCKER_DB_DATA_DIR 未设置（容器化部署前可忽略）"
fi

# ── 汇总 ───────────────────────────────────────────────────────────
echo "━━━ 汇总 ━━━"
echo "  通过: $PASS, 警告: $WARN, 失败: $FAIL"
exit $FAIL
