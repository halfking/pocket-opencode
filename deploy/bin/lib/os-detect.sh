#!/usr/bin/env bash
# =====================================================================
# os-detect.sh — OpenPocket 部署的操作系统检测库
#
# 提供：
#   os_kind                       darwin | linux | windows-msys | unknown
#   os_detect_base_dir [home]     按 OS 规则推导 ${HOME-or-arg}/kaixuan/openpocket
#   os_path_separator             ":"  | ";"  (PATH)
#   os_normalize_path <path>      把 /d/... 还原为 D:/... (仅 MSYS/Cygwin)
#   os_is_wsl                     是否 WSL（Windows 上的 Linux 子系统）
#   os_docker_socket              unix:///var/run/docker.sock  或  npipe:////./pipe/docker_engine
#   os_require_docker             检查 docker 是否可用，缺失则报错退出
#
# 用法：
#   source "$(dirname "${BASH_SOURCE[0]}")/lib/os-detect.sh"
#   base="$(os_detect_base_dir)"
#   kind="$(os_kind)"
# =====================================================================

# 防止重复 source
if [[ -n "${__OPP_OS_DETECT_LOADED:-}" ]]; then
  return 0 2>/dev/null || true
fi
__OPP_OS_DETECT_LOADED=1

# 允许被 mock（测试用）：如果外部已设 OPP_OS_KIND 直接使用
_os_kind_uname() {
  # uname -s 在 MSYS/Cygwin 下返回 MSYS_NT-10.0-19042 之类；先看 MSYSTEM
  if [[ -n "${MSYSTEM:-}" ]] || [[ -n "${WINDIR:-}" ]] || [[ "${OSTYPE:-}" == "msys" ]] || [[ "${OSTYPE:-}" == "cygwin" ]]; then
    printf '%s' "windows-msys"
    return 0
  fi
  local s
  s="$(uname -s 2>/dev/null || echo unknown)"
  case "${s}" in
    Darwin) printf '%s' "darwin" ;;
    Linux)
      # WSL 检测：/proc/version 含 "microsoft" 或 "WSL"
      if [[ -r /proc/version ]] && grep -qiE 'microsoft|WSL' /proc/version 2>/dev/null; then
        printf '%s' "wsl"
      else
        printf '%s' "linux"
      fi
      ;;
    MINGW*|MSYS*|CYGWIN*) printf '%s' "windows-msys" ;;
    *) printf '%s' "unknown" ;;
  esac
}

os_kind() {
  if [[ -n "${OPP_OS_KIND_OVERRIDE:-}" ]]; then
    printf '%s' "${OPP_OS_KIND_OVERRIDE}"
    return 0
  fi
  _os_kind_uname
}

os_is_wsl() {
  [[ "$(os_kind)" == "wsl" ]]
}

os_is_windows() {
  [[ "$(os_kind)" == "windows-msys" ]]
}

os_is_macos() {
  [[ "$(os_kind)" == "darwin" ]]
}

os_is_linux_native() {
  local k
  k="$(os_kind)"
  [[ "${k}" == "linux" || "${k}" == "wsl" ]]
}

# 默认 ${HOME} 在 MSYS/Git-Bash 下通常是 /c/Users/<name> 或 /home/<name>；
# Windows 上 D:/kaixuan/openpocket 优先于 C:/，因为 D 盘一般数据盘。
#
# 入参：可选显式 home（测试用）。返回：单一路径字符串（无尾斜杠）。
os_detect_base_dir() {
  local home="${1:-${HOME:-}}"
  local kind
  kind="$(os_kind)"
  case "${kind}" in
    darwin)
      printf '%s' "${home}/kaixuan/openpocket"
      ;;
    linux|wsl)
      printf '%s' "/opt/kaixuan/openpocket"
      ;;
    windows-msys)
      # Git-Bash 把 D 盘挂到 /d；优先 D: 盘（存在且可写），否则 C: 盘
      if [[ -d "/d" ]] && [[ -w "/d" || -w "/d/" ]]; then
        printf '%s' "D:/kaixuan/openpocket"
      elif [[ -d "/c" ]]; then
        printf '%s' "C:/kaixuan/openpocket"
      else
        # 极少数环境既无 /d 也无 /c，回退到 home
        printf '%s' "${home}/kaixuan/openpocket"
      fi
      ;;
    *)
      printf '%s' "${home}/kaixuan/openpocket"
      ;;
  esac
}

# PATH 分隔符：POSIX 是 :，Windows 是 ;
os_path_separator() {
  if os_is_windows; then printf ';' ; else printf ':' ; fi
}

# Docker socket 路径：Linux/macOS/WSL → /var/run/docker.sock；Windows MSYS → npipe
os_docker_socket() {
  if os_is_windows; then
    printf '%s' "npipe:////./pipe/docker_engine"
  else
    printf '%s' "unix:///var/run/docker.sock"
  fi
}

os_require_docker() {
  if ! command -v docker >/dev/null 2>&1; then
    echo "❌ docker 未安装或不在 PATH（$(os_path_separator) 当前 PATH）" >&2
    echo "   请先安装 Docker Desktop（macOS/Windows）或 docker-ce（Linux）" >&2
    return 1
  fi
  if ! docker version >/dev/null 2>&1; then
    echo "❌ docker daemon 不可达（$(os_docker_socket)）" >&2
    return 1
  fi
  if ! docker compose version >/dev/null 2>&1; then
    echo "❌ 需要 docker compose v2（docker-compose v1 不支持）" >&2
    return 1
  fi
  return 0
}

# 把 /d/kaixuan/foo 还原为 D:/kaixuan/foo（仅 Windows）。其它 OS 透传。
os_normalize_path() {
  local p="${1:-}"
  if os_is_windows; then
    # MSYS 把 D 盘挂到 /d，E 盘挂到 /e，等等
    if [[ "${p}" =~ ^/([a-zA-Z])(/.*)?$ ]]; then
      local drive="${BASH_REMATCH[1]^^}"
      local rest="${BASH_REMATCH[2]}"
      printf '%s:%s' "${drive}" "${rest}"
      return 0
    fi
  fi
  printf '%s' "${p}"
}

# 调试输出（受 OPP_DEBUG 控制）
if [[ "${OPP_DEBUG:-0}" == "1" ]]; then
  printf '[os-detect] kind=%s base_dir=%s\n' \
    "$(os_kind)" "$(os_detect_base_dir)" >&2
fi
