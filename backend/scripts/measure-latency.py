#!/usr/bin/env python3
"""
Phase 1: VAD延迟精确测量工具
用途：解析logcat日志，计算首字延迟分布
"""

import re
import sys
from datetime import datetime
from statistics import mean, median, stdev

def parse_timestamp(line):
    """从logcat行提取时间戳（毫秒）"""
    match = re.search(r'(\d{2}):(\d{2}):(\d{2})\.(\d{3})', line)
    if match:
        h, m, s, ms = map(int, match.groups())
        return (h * 3600 + m * 60 + s) * 1000 + ms
    return None

def main():
    if len(sys.argv) < 2:
        print("用法: python3 measure-latency.py <logcat文件>")
        print("示例: adb logcat > vad-test.log")
        print("      python3 measure-latency.py vad-test.log")
        sys.exit(1)
    
    logfile = sys.argv[1]
    
    # 解析日志
    recording_start = None
    first_emit = None
    emit_times = []
    vad_endpoints = []
    
    with open(logfile, 'r', encoding='utf-8', errors='ignore') as f:
        for line in f:
            ts = parse_timestamp(line)
            if not ts:
                continue
            
            # 录音开始
            if 'startRecording' in line or 'reportStart(true' in line:
                recording_start = ts
                first_emit = None
                print(f"[{ts}ms] 录音开始")
            
            # VAD端点检测
            if 'isSpeechEnd' in line and 'true' in line:
                if recording_start:
                    vad_endpoints.append(ts - recording_start)
                    print(f"[{ts}ms] VAD检测到语音结束 (相对时间: {ts - recording_start}ms)")
            
            # 分片发送
            if 'emitPart' in line:
                match = re.search(r'emitPart\((\d+),', line)
                if match:
                    seq = int(match.group(1))
                    if recording_start:
                        emit_time = ts - recording_start
                        emit_times.append(emit_time)
                        if first_emit is None:
                            first_emit = emit_time
                        print(f"[{ts}ms] 分片 #{seq} 发送 (相对时间: {emit_time}ms)")
    
    # 统计分析
    print("\n" + "="*60)
    print("延迟统计分析")
    print("="*60)
    
    if first_emit:
        print(f"\n首字延迟: {first_emit}ms ({first_emit/1000:.2f}秒)")
        target = 1000  # 目标<1秒
        if first_emit < target:
            print(f"✓ 达标！ (目标: <{target}ms)")
        else:
            print(f"✗ 未达标 (目标: <{target}ms, 超出: {first_emit-target}ms)")
    else:
        print("\n首字延迟: 未检测到")
    
    if emit_times:
        print(f"\n分片延迟分布:")
        print(f"  - 样本数: {len(emit_times)}")
        print(f"  - 平均值: {mean(emit_times):.0f}ms")
        print(f"  - 中位数: {median(emit_times):.0f}ms")
        if len(emit_times) > 1:
            print(f"  - 标准差: {stdev(emit_times):.0f}ms")
        print(f"  - 最小值: {min(emit_times)}ms")
        print(f"  - 最大值: {max(emit_times)}ms")
        
        # P95计算
        sorted_times = sorted(emit_times)
        p95_idx = int(len(sorted_times) * 0.95)
        p95 = sorted_times[p95_idx] if p95_idx < len(sorted_times) else sorted_times[-1]
        print(f"  - P95: {p95}ms")
    
    if vad_endpoints:
        print(f"\nVAD端点检测:")
        print(f"  - 检测次数: {len(vad_endpoints)}")
        print(f"  - 平均响应: {mean(vad_endpoints):.0f}ms")
        print(f"  - 最快响应: {min(vad_endpoints)}ms")
    
    # 性能评估
    print("\n" + "="*60)
    print("Phase 1 目标对照")
    print("="*60)
    
    targets = [
        ("首字延迟", first_emit if first_emit else 99999, 1000, "ms"),
        ("端点响应 P95", p95 if emit_times else 99999, 500, "ms"),
    ]
    
    passed = 0
    for name, actual, target, unit in targets:
        status = "✓" if actual <= target else "✗"
        print(f"{status} {name}: {actual:.0f}{unit} (目标: <{target}{unit})")
        if actual <= target:
            passed += 1
    
    print(f"\n通过率: {passed}/{len(targets)} ({passed*100//len(targets)}%)")
    
    if passed == len(targets):
        print("\n🎉 Phase 1 延迟优化目标已达成！")
        return 0
    else:
        print("\n⚠️  部分指标未达标，需进一步调优")
        return 1

if __name__ == '__main__':
    sys.exit(main())
