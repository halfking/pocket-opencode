#!/usr/bin/env bash
# Maestro bootstrap（Windows / PowerShell 版说明见下方）
#
# 为什么需要这个脚本：Maestro 不在 PATH 里，且 GitHub 直连会被切断。
# 2026-09-30 实测：Invoke-WebRequest 和 curl.exe 走
#   https://github.com/mobile-dev-inc/maestro/releases/latest/download/maestro.zip
# 都报「意外的 EOF / 0 个字节」，必须走镜像（gh-proxy.com 实测可用，300MB）。
#
# 用法（PowerShell）：
#   pwsh scripts/maestro-bootstrap.sh
# 或直接：
#   bash scripts/maestro-bootstrap.sh
#
# 幂等：已安装则只打印版本，不重复下载。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEST="$ROOT/logs/maestro"
DIST="$DEST/dist/maestro"
JAR="$DEST/maestro.zip"
# 镜像列表：按顺序尝试，第一个成功的即用
MIRRORS=(
  "https://gh-proxy.com/https://github.com/mobile-dev-inc/maestro/releases/latest/download/maestro.zip"
  "https://ghproxy.net/https://github.com/mobile-dev-inc/maestro/releases/latest/download/maestro.zip"
  "https://github.com/mobile-dev-inc/maestro/releases/latest/download/maestro.zip"
)

echo "== 1/4 check existing install =="
if [ -x "$DIST/bin/maestro" ] || [ -f "$DIST/bin/maestro.bat" ]; then
  echo "already installed at $DIST"
  JAVA_HOME="${JAVA_HOME:-/c/Program Files/Eclipse Adoptium/jdk-21.0.12.101-hotspot}" \
    "$DIST/bin/maestro" --version 2>/dev/null || true
  echo "run:  export JAVA_HOME=...; $DIST/bin/maestro --device <serial> test .maestro/_connectivity.yaml"
  exit 0
fi

echo "== 2/4 download (mirrors) =="
mkdir -p "$DEST"
ok=0
for u in "${MIRRORS[@]}"; do
  echo "--> $u"
  if curl -sSL --retry 2 --connect-timeout 20 --max-time 1800 -o "$JAR" "$u"; then
    sz=$(stat -c%s "$JAR" 2>/dev/null || stat -f%z "$JAR" 2>/dev/null || echo 0)
    echo "    got $sz bytes"
    if [ "$sz" -gt 100000000 ]; then ok=1; break; fi
  fi
  echo "    failed/too small, next mirror"
done
[ "$ok" = 1 ] || { echo "ALL MIRRORS FAILED"; exit 1; }

echo "== 3/4 extract =="
rm -rf "$DEST/dist"
mkdir -p "$DEST/dist"
unzip -q "$JAR" -d "$DEST/dist"

echo "== 4/4 verify =="
chmod +x "$DIST/bin/maestro" 2>/dev/null || true
JAVA_HOME="${JAVA_HOME:-/c/Program Files/Eclipse Adoptium/jdk-21.0.12.101-hotspot}" \
  "$DIST/bin/maestro" --version

cat <<'EOF'

--------------------------------------------------------------------
用法（PowerShell）：

  $env:JAVA_HOME = 'C:\Program Files\Eclipse Adoptium\jdk-21.0.12.101-hotspot'
  $env:PATH     = "$env:JAVA_HOME\bin;$env:PATH"
  $env:MAESTRO_CLI_ANALYSIS_NOTIFICATION_DISABLED = 'true'
  $m = '.\logs\maestro\dist\maestro\bin\maestro.bat'

  & $m --device emulator-5554      test .maestro\_connectivity.yaml
  & $m --device 192.168.31.19:5555  test .maestro\_connectivity.yaml   # 真机见下方限制

真机限制（2026-09-30 实测，MIUI / Redmi 2411DRN47C）：
  Maestro 需要先安装自己的 driver APK，MIUI 安装策略会拦：
    INSTALL_FAILED_USER_RESTRICTED: Install canceled by user
  adb install / pm install / settings put global verifier_verify_adb_installs 0
  / package_verifier_enable 0 / install_non_market 1 全部无效。
  必须在手机上手动开启：
    设置 -> 更多设置 -> 开发者选项 -> 打开「USB 安装」
    （MIUI 还需关闭「安装监控」：设置 -> 应用设置 -> 授权管理 -> 安装监控）
  授权后 driver 会自动装上，或用本脚本抽出的 APK 手动装：
    logs/maestro/driver/maestro-server.apk
    logs/maestro/driver/maestro-app.apk

排查（断言失败时不要看 console，Windows 控制台会把中文输出成乱码）：
  %USERPROFILE%\.maestro\tests\<时间戳>\<flow>\
    screen-hierarchy\*.json   完整 UI 树
    screenshots\*.png         每步截图
--------------------------------------------------------------------
EOF
