#!/usr/bin/env bash
# =====================================================================
# verify.sh — opencode-pocket 部署后验证（rule 22 §7）
#
# 用法: ./deploy/verify.sh [--env local|prod] [--tag <tag>]
# 返回: 0 = 通过, 1 = 失败
# =====================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVICE_NAME="opencode-pocket"
CONTAINER_NAME="kx-${SERVICE_NAME}"

# ── 解析参数 ───────────────────────────────────────────────────────
ENV="local"
TAG="latest"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --env) ENV="$2"; shift 2 ;;
    --tag) TAG="$2"; shift 2 ;;
    --help) echo "用法: $0 [--env local|prod] [--tag <tag>]"; exit 0 ;;
    *) echo "未知参数: $1"; exit 1 ;;
  esac
done

PASS=0; FAIL=0

check() {
  local desc="$1"
  shift
  if "$@" >/dev/null 2>&1; then
    echo "  ✅ ${desc}"; ((PASS++))
    return 0
  else
    echo "  ❌ ${desc}"; ((FAIL++))
    return 1
  fi
}

echo "━━━ verify: ${SERVICE_NAME} (env=${ENV}, tag=${TAG}) ━━━"

# 读取端口配置
DEPLOY_DIR="${SCRIPT_DIR}"
ENV_FILE="${DEPLOY_DIR}/.env"
if [[ ! -f "${ENV_FILE}" ]]; then
  ENV_FILE="${SCRIPT_DIR}/../backend/.env"
fi
PORT=$(grep "^POCKET_HTTP_PORT=" "${ENV_FILE}" 2>/dev/null | cut -d= -f2 || echo "8088")
PORT=${PORT:-8088}

# ── 1. 容器存活检查 ────────────────────────────────────────────────
check "容器运行状态" docker ps --filter "name=${CONTAINER_NAME}" --format "{{.Status}}" | grep -q "Up"

# 等待服务启动（最多 30 秒）
echo "▶ 等待服务启动..."
for i in {1..30}; do
  if curl -sf "http://localhost:${PORT}/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

# ── 2. 健康检查端点 ────────────────────────────────────────────────
check "健康检查 /healthz" curl -sf "http://localhost:${PORT}/healthz"

# ── 3. 实例列表 API ────────────────────────────────────────────────
check "实例列表 /api/instances" curl -sf "http://localhost:${PORT}/api/instances" | grep -q '\['

# ── 4. 关键环境变量检查 ─────────────────────────────────────────────
if [[ -f "${ENV_FILE}" ]]; then
  check "JWT_SECRET 已配置" grep -q "^POCKET_JWT_SECRET=" "${ENV_FILE}"
  
  # 检查生产环境配置
  if [[ "${ENV}" == "prod" ]]; then
    # 生产环境不能使用默认密钥
    if grep -q "^POCKET_JWT_SECRET=pocket-dev-insecure-secret" "${ENV_FILE}"; then
      echo "  ❌ 生产环境使用了默认 JWT 密钥"; ((FAIL++))
    else
      echo "  ✅ JWT 密钥已自定义"; ((PASS++))
    fi
    
    # 生产环境必须禁用开发认证
    if grep -q "^POCKET_DEV_AUTH=true" "${ENV_FILE}"; then
      echo "  ❌ 生产环境启用了开发认证"; ((FAIL++))
    else
      echo "  ✅ 开发认证已禁用"; ((PASS++))
    fi
  fi
else
  echo "  ⚠️  未找到 .env 文件，跳过环境变量检查"
fi

# ── 5. 数据目录权限检查 ─────────────────────────────────────────────
DATA_DIR="${SCRIPT_DIR}/../data"
check "数据目录可写" test -w "${DATA_DIR}"

# ── 6. 容器日志检查（无严重错误） ────────────────────────────────────
if docker logs "${CONTAINER_NAME}" --tail 50 2>&1 | grep -iE "(panic|fatal|error.*database)" >/dev/null 2>&1; then
  echo "  ⚠️  容器日志中发现错误信息"; ((FAIL++))
else
  echo "  ✅ 容器日志正常"; ((PASS++))
fi

echo "━━━ 验证结果 ━━━"
echo "  通过: $PASS, 失败: $FAIL"

# ── 返回 ───────────────────────────────────────────────────────────
if [[ $FAIL -gt 0 ]]; then
  echo "❌ 验证未通过请检查日志或执行 rollback"
  exit 1
fi
echo "✅ 验证通过"
