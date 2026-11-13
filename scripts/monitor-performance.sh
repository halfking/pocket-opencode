#!/bin/bash
# Cicada Phase A 性能监控脚本
# 用于测试 8 期间监控应用性能

set -e

DEVICE_ID="${POCKET_SERIAL:-10AF6H1MLM003HF}"
PACKAGE="com.kaixuan.opencode.pocket"
INTERVAL="${1:-5}"  # 监控间隔（秒）

echo "=========================================="
echo "Cicada Phase A 性能监控"
echo "=========================================="
echo "设备: $DEVICE_ID"
echo "应用: $PACKAGE"
echo "监控间隔: ${INTERVAL}s"
echo "按 Ctrl+C 停止监控"
echo "=========================================="
echo ""

# 函数：获取内存占用
get_memory() {
    local mem=$(adb -s "$DEVICE_ID" shell dumpsys meminfo "$PACKAGE" 2>/dev/null | grep "TOTAL PSS" | awk '{print $3}')
    if [ -n "$mem" ]; then
        echo "$mem"
    else
        echo "N/A"
    fi
}

# 函数：获取 CPU 占用
get_cpu() {
    local cpu=$(adb -s "$DEVICE_ID" shell top -n 1 | grep "$PACKAGE" | head -1 | awk '{print $9}')
    if [ -n "$cpu" ]; then
        echo "$cpu"
    else
        echo "N/A"
    fi
}

# 函数：获取电池温度
get_battery_temp() {
    local temp=$(adb -s "$DEVICE_ID" shell dumpsys battery | grep temperature | awk '{print $2}')
    if [ -n "$temp" ]; then
        echo "scale=1; $temp / 10" | bc
    else
        echo "N/A"
    fi
}

# 函数：检查应用是否运行
is_app_running() {
    adb -s "$DEVICE_ID" shell "ps -A | grep $PACKAGE" > /dev/null 2>&1
    return $?
}

# 主监控循环
count=1
while true; do
    timestamp=$(date "+%H:%M:%S")
    
    if is_app_running; then
        memory=$(get_memory)
        cpu=$(get_cpu)
        temp=$(get_battery_temp)
        
        printf "[%s] #%3d | 内存: %8s KB | CPU: %5s%% | 温度: %s°C\n" \
            "$timestamp" "$count" "$memory" "$cpu" "$temp"
        
        # 检查告警阈值
        if [ "$memory" != "N/A" ] && [ "$memory" -gt 512000 ]; then
            echo "  ⚠️  内存超过 500MB!"
        fi
        
        if [ "$cpu" != "N/A" ]; then
            cpu_int=${cpu%.*}
            if [ "$cpu_int" -gt 30 ]; then
                echo "  ⚠️  CPU 超过 30%!"
            fi
        fi
    else
        echo "[$timestamp] #$count | ❌ 应用未运行"
    fi
    
    sleep "$INTERVAL"
    ((count++))
done
