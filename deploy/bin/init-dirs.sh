#!/usr/bin/env bash
# =====================================================================
# init-dirs.sh — 初始化部署根目录的子目录结构
#
# 按 env.sh 解析出的 POCKET_*_DIR 创建子目录；已存在则跳过；
# 创建失败则终止。可重复执行，幂等。
#
# 用法：
#   ./deploy/bin/init-dirs.sh                 # 用当前 DEPLOY_ENV 默认值
#   DEPLOY_ENV=server ./deploy/bin/init-dirs.sh
#   DEPLOY_BASE_DIR=/srv/opp ./deploy/bin/init-dirs.sh
#
# 副作用：
#   - mkdir -p 每个 POCKET_*_DIR
#   - 在空目录里写 .gitkeep，方便 git 跟踪目录结构
#   - 在 ${POCKET_LOG_DIR}/ 写 .gitkeep + .gitignore (*.log)
#   - 校验可写权限，失败退出
# =====================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "${SCRIPT_DIR}/env.sh"

# 子目录列表：base + 五个数据/日志/配置/镜像/备份目录
DIRS=(
  "${POCKET_BASE_DIR}"
  "${POCKET_DATA_DIR}"
  "${POCKET_LOG_DIR}"
  "${POCKET_CONFIG_DIR}"
  "${POCKET_IMAGE_DIR}"
  "${POCKET_BACKUP_DIR}"
)

echo "━━━ init-dirs: DEPLOY_ENV=${DEPLOY_ENV} ━━━"
echo "  DEPLOY_BASE_DIR = ${DEPLOY_BASE_DIR}"

CREATED=0
SKIPPED=0
for d in "${DIRS[@]}"; do
  if [[ -d "${d}" ]]; then
    SKIPPED=$((SKIPPED + 1))
  else
    if mkdir -p "${d}"; then
      CREATED=$((CREATED + 1))
      echo "  ✅ mkdir ${d}"
    else
      echo "  ❌ failed to mkdir ${d}" >&2
      exit 1
    fi
  fi
done

# 空目录里放 .gitkeep，让 git 能跟踪目录结构（不影响容器挂载）
for d in "${DIRS[@]}"; do
  [[ -f "${d}/.gitkeep" ]] || : > "${d}/.gitkeep"
done

# logs/ 备份/数据/镜像目录自忽略（防 DEPLOY_BASE_DIR 指进仓库时误提交运行时产物）
for sub in logs backup data images; do
  cat > "${POCKET_BASE_DIR:?}/${sub}/.gitignore" <<'EOF'
*
!.gitignore
!.gitkeep
EOF
done

# 可写权限校验（init 阶段就要失败，不要等到容器启动才发现）
for d in "${POCKET_DATA_DIR}" "${POCKET_LOG_DIR}" "${POCKET_CONFIG_DIR}"; do
  if ! [[ -w "${d}" ]]; then
    echo "  ❌ directory not writable: ${d}" >&2
    echo "     当前用户 $(id -un) 无写权限，请检查属主或 sudo 执行" >&2
    exit 1
  fi
done

echo "━━━ 完成 ━━━"
echo "  新建 ${CREATED} 个目录，跳过 ${SKIPPED} 个已存在目录"
if [[ "${DEPLOY_ENV}" == "server" ]]; then
  # Linux 上容器内非 root 用户(pocket)对 bind mount 的可写性依赖宿主属主；
  # root 建的 755 目录会让运行期写 /app/data 失败。给出 chown 提示。
  if [[ "$(id -u)" -eq 0 ]]; then
    echo "  ⚠️  当前以 root 建目录；若容器内进程非 root，确保属主匹配："
    echo "     docker run --rm opencode-pocket:${OPP_IMAGE_TAG:-pocket-opp} id -u   # 查容器内 uid"
    echo "     chown -R <uid> ${POCKET_DATA_DIR} ${POCKET_LOG_DIR}"
  fi
fi
if [[ "${DEPLOY_ENV}" == "local" ]]; then
  echo "  下一步: ./deploy/bin/deploy-local.sh（会自动生成 ${POCKET_ENV_FILE}）"
else
  echo "  下一步: 手工填写 ${POCKET_ENV_FILE}（生产密钥/DSN），再 sudo DEPLOY_ENV=server ./deploy/bin/deploy-252.sh"
fi
