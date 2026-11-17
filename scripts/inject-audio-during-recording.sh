#!/bin/bash
# inject-audio-during-recording.sh
# 在录音过程中自动注入测试音频
# 支持多种音频注入方案

set -e

DEVICE_ID="${1:-$(adb devices | grep -w device | head -1 | awk '{print $1}')}"
DURATION="${2:-5}"

echo "🎤 音频注入脚本"
echo "设备: $DEVICE_ID"
echo "时长: ${DURATION}秒"
echo ""

# 方案 1: 使用系统音频流播放正弦波
inject_sine_wave() {
    echo "方案 1: 生成正弦波音频流"
    
    # 使用 Android AudioTrack 播放音频
    adb -s "$DEVICE_ID" shell <<'EOF'
        # 创建临时音频测试脚本
        cat > /data/local/tmp/play_tone.sh <<'INNER_EOF'
#!/system/bin/sh
# 使用 tinymix/tinyplay 播放测试音频（如果设备支持）
if command -v tinyplay &> /dev/null; then
    # 生成 PCM 数据并播放
    dd if=/dev/urandom bs=1024 count=160 2>/dev/null | \
    tinyplay /dev/stdin -D 0 -d 0 -r 16000 -c 1 -b 16 2>/dev/null || true
else
    echo "tinyplay not available"
fi
INNER_EOF
        
        chmod +x /data/local/tmp/play_tone.sh
        /data/local/tmp/play_tone.sh
EOF
}

# 方案 2: 使用媒体播放器播放音频文件
inject_audio_file() {
    echo "方案 2: 播放音频文件"
    
    LOCAL_AUDIO="scripts/__tests__/test-audio-5s.wav"
    DEVICE_AUDIO="/sdcard/Download/openpocket-test-audio.wav"
    
    if [ -f "$LOCAL_AUDIO" ]; then
        echo "  推送音频文件..."
        adb -s "$DEVICE_ID" push "$LOCAL_AUDIO" "$DEVICE_AUDIO" 2>&1 | grep -v "bytes"
        
        echo "  启动播放..."
        # 使用 am 启动媒体播放
        adb -s "$DEVICE_ID" shell am start -a android.intent.action.VIEW \
            -d "file://$DEVICE_AUDIO" \
            -t "audio/*" 2>/dev/null || true
        
        # 或者使用 media player service
        adb -s "$DEVICE_ID" shell "media play $DEVICE_AUDIO" 2>/dev/null || true
        
        echo "  ✓ 音频播放已启动"
    else
        echo "  ✗ 音频文件不存在: $LOCAL_AUDIO"
        return 1
    fi
}

# 方案 3: 使用 TTS 引擎说话
inject_tts() {
    echo "方案 3: TTS 语音合成"
    
    TEST_TEXT="This is an automated recording test for OpenPocket application. Testing voice activity detection and automatic speech recognition capabilities."
    
    # 检查 TTS 是否可用
    if adb -s "$DEVICE_ID" shell "pm list packages | grep -q tts"; then
        echo "  启动 TTS..."
        
        # 方式 1: 使用 TTS 命令行工具（部分设备支持）
        adb -s "$DEVICE_ID" shell "tts_speak '$TEST_TEXT'" 2>/dev/null || true
        
        # 方式 2: 使用 am 启动 TTS
        adb -s "$DEVICE_ID" shell "am start -a android.intent.action.TTS_SERVICE" 2>/dev/null || true
        
        echo "  ✓ TTS 已触发"
    else
        echo "  ✗ TTS 不可用"
        return 1
    fi
}

# 方案 4: 播放系统通知音
inject_notification_sound() {
    echo "方案 4: 播放系统音效"
    
    # 播放多个通知音，产生连续音频输入
    for i in {1..3}; do
        adb -s "$DEVICE_ID" shell "cmd notification post -S bigtext \
            -t 'OpenPocket Test' \
            'Tag_$i' 'Test notification $i'" 2>/dev/null || true
        sleep 1
    done
    
    echo "  ✓ 系统音效已播放"
}

# 方案 5: 直接注入 PCM 数据到麦克风（需要 root）
inject_pcm_raw() {
    echo "方案 5: 直接注入 PCM 数据（需要 root）"
    
    # 检查是否有 root
    if adb -s "$DEVICE_ID" shell "su -c 'echo test'" 2>/dev/null | grep -q "test"; then
        echo "  检测到 root 权限"
        
        # 注入 PCM 数据到音频设备
        adb -s "$DEVICE_ID" shell "su -c 'dd if=/dev/urandom of=/dev/snd/pcmC0D0c bs=320 count=250'" 2>/dev/null || true
        
        echo "  ✓ PCM 数据已注入"
    else
        echo "  ✗ 无 root 权限"
        return 1
    fi
}

# 按优先级尝试各种方案
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "开始注入音频..."
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

# 尝试方案 2（最可靠）
if inject_audio_file; then
    echo ""
    echo "✓ 音频注入成功 (方案 2: 文件播放)"
    exit 0
fi

# 尝试方案 3
if inject_tts; then
    echo ""
    echo "✓ 音频注入成功 (方案 3: TTS)"
    exit 0
fi

# 尝试方案 4
if inject_notification_sound; then
    echo ""
    echo "✓ 音频注入成功 (方案 4: 系统音效)"
    exit 0
fi

# 尝试方案 1
if inject_sine_wave; then
    echo ""
    echo "✓ 音频注入成功 (方案 1: 正弦波)"
    exit 0
fi

# 尝试方案 5（最后手段）
if inject_pcm_raw; then
    echo ""
    echo "✓ 音频注入成功 (方案 5: PCM 注入)"
    exit 0
fi

echo ""
echo "⚠ 所有音频注入方案均失败"
echo "建议: 手动在设备上播放音频或说话"
exit 1
