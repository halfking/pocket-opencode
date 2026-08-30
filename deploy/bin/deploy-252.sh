#!/usr/bin/env bash
# =====================================================================
# deploy-252.sh — 252 服务器部署入口
#
# 默认根目录: /opt/kaixuan/opp
# 网络:       acc-server-net（或 kaixuan_net，按 .env 决定）
# Profile:    server（关闭 dev-auth、POCKET_ENV=production）
#
# 注意：
#   - 需要 root 或 sudo（/opt/kaixuan 通常属 root:kaixuan）
#   - 如 252 走 sudo，请加 sudo ./deploy/bin/deploy-252.sh
#   - 服务器上不会自动生成 .env，必须由运维手工填入（含生产密钥）
#
# 用法：
#   ./deploy/bin/deploy-252.sh
#   sudo DEPLOY_BASE_DIR=/srv/kaixuan/opp ./deploy/bin/deploy-252.sh
# =====================================================================

set -euo pipefail

export DEPLOY_ENV="${DEPLOY_ENV:-server}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "${SCRIPT_DIR}/env.sh"

echo "━━━ deploy-252 ━━━"
echo "  DEPLOY_BASE_DIR = ${POCKET_BASE_DIR}"
echo "  当前用户 = $(id -un)"

# 0) 服务器必须以 root 写入 /opt；非 root 显式拒绝
if [[ "${POCKET_BASE_DIR}" == /opt/* || "${POCKET_BASE_DIR}" == /srv/* ]]; then
  if [[ "$(id -u)" -ne 0 ]]; then
    echo "  ❌ 服务器部署到 ${POCKET_BASE_DIR} 需要 root，请用 sudo 重跑" >&2
    exit 1
  fi
fi

# 1) 创建目录
"${SCRIPT_DIR}/init-dirs.sh"

# 2) 服务器不会写 .env，必须存在才启动
if [[ ! -f "${POCKET_ENV_FILE}" ]]; then
  echo "  ❌ 服务器 .env 不存在: ${POCKET_ENV_FILE}" >&2
  echo "     请先把生产密钥、DSN、JWT_SECRET 等填入该文件" >&2
  echo "     模板可参考: deploy/acc-integration/.env.example" >&2
  exit 1
fi

# 服务器环境强制校验：禁 dev-auth、禁 prod 跑测试密钥
# 读值时统一去 CRLF 与成对引号，避免 POCKET_ENV="production" 写法误判
read_env_stripped() {
  awk -F= -v key="$1" '$1 == key {sub(/^[^=]*=/, ""); gsub(/\r/, ""); gsub(/^"|"$/, ""); print; exit}' "${POCKET_ENV_FILE}"
}
if [[ "$(read_env_stripped POCKET_DEV_AUTH)" == "true" ]]; then
  echo "  ❌ 服务器 .env 里 POCKET_DEV_AUTH=true，拒绝启动" >&2
  echo "     生产必须 POCKET_DEV_AUTH=false（或留空）" >&2
  exit 1
fi

# 服务器必须显式声明 production（与 deploy/deploy.sh 的 prod 门禁一致）
POCKET_ENV_VALUE="$(read_env_stripped POCKET_ENV)"
if [[ "${POCKET_ENV_VALUE}" != "production" && "${POCKET_ENV_VALUE}" != "prod" ]]; then
  echo "  ❌ 服务器 .env 必须设置 POCKET_ENV=production（当前: '${POCKET_ENV_VALUE}'）" >&2
  exit 1
fi

# 数据库：openpocket 权威 PG 在 252 本机 docker 中（内网 ${OPP_PG_HOST}:${OPP_PG_PORT}）
if ! grep -q '^POCKET_POSTGRES_DSN=' "${POCKET_ENV_FILE}"; then
  echo "  ❌ 服务器 .env 缺少 POCKET_POSTGRES_DSN" >&2
  echo "     252 本机直连示例: postgresql://${OPP_PG_USER}:<密码>@${OPP_PG_HOST}:${OPP_PG_PORT}/${OPP_PG_DB}?sslmode=disable" >&2
  exit 1
fi
if timeout 3 bash -c "</dev/tcp/${OPP_PG_HOST}/${OPP_PG_PORT}" >/dev/null 2>&1; then
  echo "  ✅ PG 可达: ${OPP_PG_HOST}:${OPP_PG_PORT}"
else
  echo "  ❌ PG 不可达: ${OPP_PG_HOST}:${OPP_PG_PORT}（docker PG 未启动？）" >&2
  exit 1
fi

# 3) 拉起服务（透传 --backend-only 等参数给 start.sh）
exec "${SCRIPT_DIR}/start.sh" "$@"
