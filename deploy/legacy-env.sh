#!/usr/bin/env bash

legacy_env_value() {
  local file="$1"
  local key="$2"
  [[ -f "${file}" ]] || return 0

  awk -v key="${key}" '
    index($0, key "=") == 1 {
      value = substr($0, length(key) + 2)
      sub(/\r$/, "", value)
      found = 1
    }
    END { if (found) print value }
  ' "${file}"
}

legacy_env_key_count() {
  local file="$1"
  local key="$2"
  [[ -f "${file}" ]] || { printf '0\n'; return 0; }

  awk -v key="${key}" 'index($0, key "=") == 1 { count++ } END { print count + 0 }' "${file}"
}

legacy_env_require_canonical_unique() {
  local file="$1"
  shift
  local key count malformed

  for key in "$@"; do
    count="$(legacy_env_key_count "${file}" "${key}")"
    if (( count > 1 )); then
      echo "❌ 环境文件中 ${key} 重复定义，拒绝继续: ${file}" >&2
      return 1
    fi

    malformed="$(awk -v key="${key}" '
      $0 ~ "^[[:space:]]*" key "[[:space:]]*=" && index($0, key "=") != 1 { print; exit }
      index($0, key "=") == 1 {
        value = substr($0, length(key) + 2)
        sub(/\r$/, "", value)
        if (value ~ /^[[:space:]]/ || value ~ /[[:space:]]$/ || value ~ /^\047/ || value ~ /^"/) {
          print
          exit
        }
      }
    ' "${file}")"
    if [[ -n "${malformed}" ]]; then
      echo "❌ ${key} 必须使用无引号、无首尾空白的 KEY=value 格式: ${file}" >&2
      return 1
    fi
  done
}

legacy_validate_managed_env() {
  local file="$1"
  local production="$2"
  local value jwt_secret

  legacy_env_require_canonical_unique "${file}" \
    POCKET_ENV POCKET_DEV_AUTH POCKET_HTTP_PORT POCKET_PORT_BIND_IP \
    POCKET_JWT_SECRET POCKET_POSTGRES_DSN POCKET_ALLOWED_ORIGINS \
    POCKET_MCP_INSECURE_TLS POCKET_MCP_BASE_URL POCKET_MCP_TENANT_ID \
    POCKET_LLM_BASE_URL POCKET_LLM_API_KEY || return 1

  [[ "${production}" == "true" ]] || return 0
  [[ -f "${file}" ]] || { echo "❌ 生产操作缺少环境文件: ${file}" >&2; return 1; }

  value="$(legacy_env_value "${file}" POCKET_ENV)"
  if [[ "${value}" != "production" && "${value}" != "prod" ]]; then
    echo "❌ 生产操作必须设置 POCKET_ENV=production" >&2
    return 1
  fi
  if [[ "$(legacy_env_value "${file}" POCKET_DEV_AUTH)" == "true" ]]; then
    echo "❌ 生产操作禁止启用 POCKET_DEV_AUTH=true" >&2
    return 1
  fi
  jwt_secret="$(legacy_env_value "${file}" POCKET_JWT_SECRET)"
  if (( ${#jwt_secret} < 32 )) || [[ "${jwt_secret}" == "pocket-dev-insecure-secret" ]]; then
    echo "❌ 生产操作要求 POCKET_JWT_SECRET 至少 32 字节且不能使用默认值" >&2
    return 1
  fi
  [[ -n "$(legacy_env_value "${file}" POCKET_POSTGRES_DSN)" ]] || {
    echo "❌ 生产操作必须设置 POCKET_POSTGRES_DSN" >&2
    return 1
  }
  [[ -n "$(legacy_env_value "${file}" POCKET_ALLOWED_ORIGINS)" ]] || {
    echo "❌ 生产操作必须设置 POCKET_ALLOWED_ORIGINS" >&2
    return 1
  }
  if [[ "$(legacy_env_value "${file}" POCKET_MCP_INSECURE_TLS)" == "true" ]]; then
    echo "❌ 生产操作禁止启用 POCKET_MCP_INSECURE_TLS=true" >&2
    return 1
  fi
  if [[ -n "$(legacy_env_value "${file}" POCKET_LLM_BASE_URL)" || \
        -n "$(legacy_env_value "${file}" POCKET_LLM_API_KEY)" ]]; then
    echo "❌ 生产操作禁止配置直连 LLM provider" >&2
    return 1
  fi
  if [[ -n "$(legacy_env_value "${file}" POCKET_MCP_BASE_URL)" && \
        -z "$(legacy_env_value "${file}" POCKET_MCP_TENANT_ID)" ]]; then
    echo "❌ 配置 MCP 时必须设置 POCKET_MCP_TENANT_ID" >&2
    return 1
  fi
}

legacy_resolve_value() {
  local key="$1"
  local file="$2"
  local default_value="$3"
  local explicit_value="${!key:-}"
  local file_value

  if [[ -n "${explicit_value}" ]]; then
    printf '%s\n' "${explicit_value}"
    return 0
  fi

  file_value="$(legacy_env_value "${file}" "${key}")"
  printf '%s\n' "${file_value:-${default_value}}"
}

legacy_validate_port() {
  local port="$1"
  [[ "${port}" =~ ^[0-9]+$ ]] && (( port >= 1 && port <= 65535 ))
}

legacy_probe_host() {
  local bind_ip="$1"
  if [[ "${bind_ip}" == "0.0.0.0" ]]; then
    printf 'localhost\n'
  else
    printf '%s\n' "${bind_ip}"
  fi
}

legacy_atomic_write() {
  local file="$1"
  local value="$2"
  local temp_file="${file}.tmp.$$"

  printf '%s\n' "${value}" > "${temp_file}"
  mv "${temp_file}" "${file}"
}

legacy_acquire_lock() {
  local lock_dir="$1"

  if ! mkdir "${lock_dir}" 2>/dev/null; then
    echo "❌ 已有部署或回滚任务运行中: ${lock_dir}" >&2
    return 1
  fi
  LEGACY_DEPLOY_LOCK_DIR="${lock_dir}"
  LEGACY_DEPLOY_LOCK_OWNER=1
  export LEGACY_DEPLOY_LOCK_DIR
}

legacy_release_lock() {
  if [[ "${LEGACY_DEPLOY_LOCK_OWNER:-0}" == "1" && -n "${LEGACY_DEPLOY_LOCK_DIR:-}" ]]; then
    rmdir "${LEGACY_DEPLOY_LOCK_DIR}" 2>/dev/null || true
    unset LEGACY_DEPLOY_LOCK_DIR LEGACY_DEPLOY_LOCK_OWNER
  fi
}

legacy_container_image() {
  local container_name="$1"
  local names image

  if ! names="$(docker ps -a --filter "name=^/${container_name}$" --format '{{.Names}}')"; then
    echo "❌ 无法查询 Docker 容器状态" >&2
    return 2
  fi
  if ! grep -Fxq "${container_name}" <<<"${names}"; then
    return 1
  fi
  if ! image="$(docker inspect --format '{{.Config.Image}}' "${container_name}")"; then
    echo "❌ 容器存在但无法读取镜像信息: ${container_name}" >&2
    return 2
  fi
  [[ -n "${image}" ]] || { echo "❌ 容器镜像信息为空: ${container_name}" >&2; return 2; }
  printf '%s\n' "${image}"
}

legacy_validate_image_reference() {
  local image="$1"
  [[ -n "${image}" ]] && [[ "${image}" != *$'\n'* ]] && \
    [[ "${image}" != *$'\r'* ]] && [[ "${image}" != [[:space:]]* ]] && \
    [[ "${image}" != *[[:space:]] ]] && [[ "${image}" != *[[:space:]]* ]]
}

legacy_start_container() {
  local container_name="$1"
  local image="$2"
  local env_file="$3"
  local data_dir="$4"
  local bind_ip="$5"
  local host_port="$6"
  local network="$7"

  docker run -d \
    --name "${container_name}" \
    --restart always \
    -p "${bind_ip}:${host_port}:8088" \
    --env-file "${env_file}" \
    -e POCKET_HTTP_PORT=8088 \
    --network "${network}" \
    -v "${data_dir}:/app/data" \
    "${image}"
}

legacy_remove_container() {
  local container_name="$1"
  local names

  if names="$(docker ps -a --filter "name=^/${container_name}$" --format '{{.Names}}')"; then
    :
  else
    echo "❌ 无法查询待清理容器: ${container_name}" >&2
    return 1
  fi
  if ! grep -Fxq "${container_name}" <<<"${names}"; then
    return 0
  fi

  if ! docker rm -f "${container_name}" >/dev/null; then
    echo "❌ 无法删除容器: ${container_name}" >&2
    return 1
  fi
  if ! names="$(docker ps -a --filter "name=^/${container_name}$" --format '{{.Names}}')"; then
    echo "❌ 无法复查容器清理结果: ${container_name}" >&2
    return 1
  fi
  if grep -Fxq "${container_name}" <<<"${names}"; then
    echo "❌ 容器清理后仍存在: ${container_name}" >&2
    return 1
  fi
}
