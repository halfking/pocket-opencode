#!/bin/bash
# scripts/build-ios.sh
# iOS 构建脚本

set -e

echo "=== Building OpenCode Pocket for iOS ==="

# 1. 构建前端
echo "[1/3] Building frontend..."
cd "$(dirname "$0")/../frontend"
npm run build

# 2. 同步到 iOS
echo "[2/3] Syncing to iOS..."
npx cap sync ios

# 3. 打开 Xcode（可选）
if [ "${1}" = "--open" ]; then
    echo "[3/3] Opening Xcode..."
    npx cap open ios
fi

echo "=== Done! ==="