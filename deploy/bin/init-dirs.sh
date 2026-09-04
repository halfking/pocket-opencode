#!/usr/bin/env bash
# =====================================================================
# init-dirs.sh — 初始化部署根目录的子目录结构
#
# 目录清单（2026-09-03 重构）：
#   始终创建：
#     attachments/  bin/  backups/  logs/  raw-logs/  run/
#     data/         config/  images/
#   条件创建（按 OPP_DEPLOY_<DB>）：
#     postgres/  redis/  mysql/
#
# 副作用：
#   - mkdir -p 每个目录；存在则跳过
#   - 每个目录写 .gitkeep
#   - 每个目录写 .gitignore（防误提交运行时产物）
#   - 校验可写权限，失败退出
#   - 调 bg_init 准备 bin/（用于 blue-green）
#
# 用法：
#   ./deploy/bin/init-dirs.sh
#   DEPLOY_ENV=server OPP_SERVER_NAME=154 ./deploy/bin/init-dirs.sh
#   DEPLOY_BASE_DIR=/tmp/opp ./deploy/bin/init-dirs.sh
# =====================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "${SCRIPT_DIR}/env.sh"
# shellcheck disable=SC1091
source "${LIB_DIR}/blue-green.sh"

# 始终创建的子目录（顺序：base 在前，其它依赖 base 存在）
ALWAYS_DIRS=(
  "${POCKET_BASE_DIR}"
  "${POCKET_DATA_DIR}"
  "${POCKET_LOG_DIR}"
  "${POCKET_RAW_LOG_DIR}"
  "${POCKET_CONFIG_DIR}"
  "${POCKET_IMAGE_DIR}"
  "${POCKET_BACKUP_DIR}"
  "${POCKET_ATTACHMENTS_DIR}"
  "${POCKET_BIN_DIR}"
  "${POCKET_RUN_DIR}"
)

# 条件 DB 数据目录
CONDITIONAL_DB_DIRS=()
[[ "${OPP_DEPLOY_PG:-false}" == "true" ]]    && CONDITIONAL_DB_DIRS+=("${POCKET_PG_DATA_DIR}")
[[ "${OPP_DEPLOY_REDIS:-false}" == "true" ]] && CONDITIONAL_DB_DIRS+=("${POCKET_REDIS_DATA_DIR}")
[[ "${OPP_DEPLOY_MYSQL:-false}" == "true" ]] && CONDITIONAL_DB_DIRS+=("${POCKET_MYSQL_DATA_DIR}")

echo "━━━ init-dirs: DEPLOY_ENV=${DEPLOY_ENV} OS=${OPP_OS_KIND} ━━━"
echo "  DEPLOY_BASE_DIR = ${DEPLOY_BASE_DIR}"
echo "  OPP_SERVER_NAME = ${OPP_SERVER_NAME:-<none>}"

CREATED=0
SKIPPED=0
mkdir_dir() {
  local d="$1"
  if [[ -d "${d}" ]]; then
    SKIPPED=$((SKIPPED + 1))
    return 0
  fi
  if mkdir -p "${d}"; then
    CREATED=$((CREATED + 1))
    echo "  ✅ mkdir ${d}"
  else
    echo "  ❌ failed to mkdir ${d}" >&2
    exit 1
  fi
}

for d in "${ALWAYS_DIRS[@]}" "${CONDITIONAL_DB_DIRS[@]}"; do
  mkdir_dir "${d}"
done

# 写 .gitkeep（空目录也保留结构）
for d in "${ALWAYS_DIRS[@]}" "${CONDITIONAL_DB_DIRS[@]}"; do
  [[ -f "${d}/.gitkeep" ]] || : > "${d}/.gitkeep"
done

# 每个目录写 .gitignore（防误提交运行时产物）
write_gitignore() {
  local d="$1"
  # bin/ 由 bg_init 自己处理（覆盖含更宽松的规则）
  if [[ "${d}" == "${POCKET_BIN_DIR}" ]]; then
    return 0
  fi
  cat > "${d}/.gitignore" <<'EOF'
*
!.gitignore
!.gitkeep
EOF
}
for d in "${ALWAYS_DIRS[@]}" "${CONDITIONAL_DB_DIRS[@]}"; do
  write_gitignore "${d}"
done

# 初始化 bin/：写 .gitignore + .gitkeep，但不创建 current 符号链接
# （current 在第一次 deploy 时由 blue-green 的 bg_stage + bg_switch 创建）
bg_init

# 可写权限校验（init 阶段就要失败，不要等到容器启动才发现）
for d in "${POCKET_DATA_DIR}" "${POCKET_LOG_DIR}" "${POCKET_RAW_LOG_DIR}" "${POCKET_CONFIG_DIR}" "${POCKET_RUN_DIR}" "${POCKET_ATTACHMENTS_DIR}"; do
  if ! [[ -w "${d}" ]]; then
    echo "  ❌ directory not writable: ${d}" >&2
    echo "     当前用户 $(id -un) 无写权限，请检查属主或 sudo 执行" >&2
    exit 1
  fi
done

echo "━━━ 完成 ━━━"
echo "  新建 ${CREATED} 个目录，跳过 ${SKIPPED} 个已存在目录"
if (( ${#CONDITIONAL_DB_DIRS[@]} > 0 )); then
  echo "  条件 DB 数据目录已建: ${CONDITIONAL_DB_DIRS[*]}"
fi

if [[ "${DEPLOY_ENV}" == "server" ]]; then
  if [[ "$(id -u)" -eq 0 ]]; then
    echo "  ⚠️  当前以 root 建目录；若容器内进程非 root，确保属主匹配："
    echo "     docker run --rm opencode-pocket:${OPP_IMAGE_TAG:-pocket-opp} id -u   # 查容器内 uid"
    echo "     chown -R <uid> ${POCKET_DATA_DIR} ${POCKET_LOG_DIR} ${POCKET_RAW_LOG_DIR} ${POCKET_ATTACHMENTS_DIR}"
  fi
fi

if [[ "${DEPLOY_ENV}" == "local" ]]; then
  echo "  下一步: ./deploy-local.sh（会自动生成 ${POCKET_ENV_FILE}）"
elif [[ -n "${OPP_SERVER_NAME}" ]]; then
  echo "  下一步: 填写 ${POCKET_ENV_FILE}（生产密钥/DSN），再 ./deploy-${OPP_SERVER_NAME}.sh"
else
  echo "  下一步: 填写 ${POCKET_ENV_FILE}（生产密钥/DSN），再 DEPLOY_ENV=server ./deploy/bin/start.sh"
fi

