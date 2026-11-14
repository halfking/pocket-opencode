#!/bin/bash
# 说话人分离服务启动脚本
# 用法: ./start_diarization_service.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VENV_DIR="$SCRIPT_DIR/venv-diarization"

echo "==> 检查Python环境..."
if ! command -v python3 &> /dev/null; then
    echo "错误: 未找到python3"
    exit 1
fi

# 创建虚拟环境
if [ ! -d "$VENV_DIR" ]; then
    echo "==> 创建Python虚拟环境..."
    python3 -m venv "$VENV_DIR"
fi

# 激活虚拟环境
source "$VENV_DIR/bin/activate"

# 安装依赖
echo "==> 安装依赖包..."
pip install --upgrade pip > /dev/null
pip install pyannote.audio flask torch torchaudio

# 检查HF_TOKEN
if [ -z "$HF_TOKEN" ]; then
    echo ""
    echo "警告: 未设置HF_TOKEN环境变量"
    echo "说话人分离模型需要HuggingFace token"
    echo "请访问: https://huggingface.co/settings/tokens"
    echo ""
    echo "然后运行: export HF_TOKEN='your-token-here'"
    echo ""
    read -p "是否继续（将尝试使用公开模型）? [y/N] " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        exit 1
    fi
fi

# 启动服务
echo "==> 启动说话人分离服务..."
echo "服务地址: http://localhost:8002"
echo "健康检查: curl http://localhost:8002/health"
echo ""
python3 "$SCRIPT_DIR/diarization_service.py"
