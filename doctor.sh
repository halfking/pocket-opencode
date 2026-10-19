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

# ── 6. postgres PGDATA 模式（2026-10-17 定案并实测）─────────────────────
#
# 为什么要有这条：docker-compose.db.yml 现在把 PGDATA 默认放在 Docker **命名卷**上。
# 之前是宿主 bind mount，于是 macOS 上有一条真实故障链（2026-10-17 实测复现）：
#   宿主目录属主 uid 501（容器内 postgres 是 uid 70）
#   → initdb 的临时 pg_ctl 以 postgres 身份起不来（wrong ownership）
#   → 镜像入口放弃重试，看到 PG_VERSION 就「目录已有数据库，跳过初始化」
#   → **POSTGRES_DB 根本没被创建**，而旧 healthcheck 用 pg_isready 照样报 healthy
#
# 定案：**命名卷为默认**（理由见 docker-compose.db.yml 里那段注释）——
# uid 70 是镜像实现细节、macOS 上容器内 chown 不生效、官方镜像入口本就围绕
# 「一个它自己拥有的目录」设计。
#
# ⇒ 本条只在**有人显式退回 bind mount**时才说话，且在 macOS 上是 **fail** 不是 warn：
#    过去的 warn 提的修法（sudo chown 70:70）在 macOS 上**根本无效**，
#    一条永远无法执行的建议等于没有建议。
if [[ "${OPP_PG_DATA_MODE:-volume}" == "bind" ]]; then
  pg_dir="${DOCKER_DB_DATA_DIR:-}"
  owner_uid=""
  [[ -n "$pg_dir" && -d "$pg_dir" ]] && owner_uid=$(stat -c %u "$pg_dir" 2>/dev/null || stat -f %u "$pg_dir" 2>/dev/null || echo "")
  if [[ "$(uname -s)" == "Darwin" ]]; then
    check_fail "postgres 退回 bind mount（OPP_PG_DATA_MODE=bind）于 macOS：PGDATA 属主 ${owner_uid:-?} ≠ 容器内 postgres(70)，且 Docker Desktop 的文件共享层使容器内 chown **不生效**。"
    check_fail "  ⇒ 首次启动会跳过创建 POSTGRES_DB（2026-10-17 实测复现）。修法只有一条：unset OPP_PG_DATA_MODE 改用命名卷。"
    check_fail "    若已踩坑：数据在宿主目录里，先备份再删掉该目录重建（换模式 = 换数据位置，两边不是同一个库）。"
  elif [[ "$owner_uid" == "70" ]]; then
    check_pass "postgres 用 bind mount，PGDATA 属主 70（与容器内 postgres 一致）"
  else
    check_fail "postgres 用 bind mount，但 PGDATA 属主 ${owner_uid:-?} ≠ 70 ⇒ initdb 起不来，POSTGRES_DB 会被跳过创建。修：sudo chown 70:70 ${pg_dir}"
  fi
else
  # 默认路径。命名卷由 Docker 按镜像 uid 创建，initdb 必然能跑，无需查属主。
  check_pass "postgres PGDATA 用 Docker 命名卷（${OPP_PG_VOLUME_NAME:-opp-pgdata}）—— initdb 不会因属主不符而跳过建库"
  if [[ -n "${DOCKER_DB_DATA_DIR:-}" ]]; then
    check_warn "  （旧的 ${DOCKER_DB_DATA_DIR} 宿主目录已不再挂载；若那里曾有旧数据，它现在是**孤儿**——新卷是空库，别把两个搞混）"
  fi
fi

# ── 6b. 存量实例的慢查询观测（2026-10-17 新增）─────────────────────────
#
# 为什么单独一条：pg-init-slow-query.sql 挂在 /docker-entrypoint-initdb.d/，
# 而镜像**只在数据目录为空的那一次**执行它 ⇒ 已经跑过初始化的实例**永远**
# 不会再装 pg_stat_statements。而 DEPLOYMENT_GUIDE 却让人查
# pocket_slow_statements ⇒ 存量环境里那条语句必然报 relation does not exist。
#
# ⇒ 这是「新增配置只对新环境生效」的典型，而没人对存量环境补过。
if docker inspect opp-db-postgres >/dev/null 2>&1; then
  if [[ "$(docker inspect --format '{{.State.Status}}' opp-db-postgres 2>/dev/null)" == "running" ]]; then
    if docker exec opp-db-postgres psql -U "${OPP_PG_USER:-llm_gateway}" -d "${OPP_PG_DB:-pocket}" \
         -tAc "SELECT 1 FROM pg_extension WHERE extname='pg_stat_statements';" 2>/dev/null | grep -q 1; then
      check_pass "慢查询观测已就绪（pg_stat_statements 已装）"
    else
      check_fail "存量实例缺 pg_stat_statements ⇒ DEPLOYMENT_GUIDE 里的慢查询查询必然报 relation does not exist。补：deploy/bin/pg-backfill-slow-query.sh"
    fi
  else
    check_warn "容器 opp-db-postgres 未运行，跳过慢查询观测检查"
  fi
fi

# ── 汇总 ───────────────────────────────────────────────────────────
echo "━━━ 汇总 ━━━"
echo "  通过: $PASS, 警告: $WARN, 失败: $FAIL"
exit $FAIL
