#!/usr/bin/env python3
"""
Phase 1 实施审计脚本
验证所有VAD相关代码改动是否完整
"""

import os
import re
import sys

def check_file(path, checks):
    """检查文件是否包含所需内容"""
    if not os.path.exists(path):
        return {"status": "missing", "details": f"文件不存在: {path}"}
    
    with open(path, 'r', encoding='utf-8', errors='ignore') as f:
        content = f.read()
    
    results = []
    for check_name, pattern in checks:
        if re.search(pattern, content, re.MULTILINE):
            results.append({"name": check_name, "status": "✓"})
        else:
            results.append({"name": check_name, "status": "✗"})
    
    passed = sum(1 for r in results if r["status"] == "✓")
    return {
        "status": "passed" if passed == len(checks) else "failed",
        "passed": passed,
        "total": len(checks),
        "details": results
    }

def main():
    base = "/Users/xutaohuang/workspace/ai-native-tools/client/openpocket"
    
    # 审计检查点
    audits = {
        "SherpaPlugin.java": (
            f"{base}/frontend/android/app/src/main/java/com/kaixuan/opencode/pocket/plugins/SherpaPlugin.java",
            [
                ("导入VoiceActivityDetector", r"import.*VoiceActivityDetector"),
                ("导入SileroVadModelConfig", r"import.*SileroVadModelConfig"),
                ("VAD实例变量", r"private VoiceActivityDetector vad"),
                ("VAD模型常量", r"VAD_MODEL_DIR.*silero_vad"),
                ("createVad方法", r"@PluginMethod\s+public void createVad"),
                ("vadProcess方法", r"@PluginMethod\s+public void vadProcess"),
                ("initVad方法", r"private void initVad\(\)"),
                ("ensureVadModel方法", r"private void ensureVadModel\(\)"),
                ("vadModelPresent方法", r"private boolean vadModelPresent\(\)"),
                ("VadWrapper类", r"public static class VadWrapper"),
                ("createVadInstance静态方法", r"public static Object createVadInstance"),
                ("status方法包含vadReady", r"ret\.put\(\"vadReady\""),
            ]
        ),
        "MeetingRecordService.java": (
            f"{base}/frontend/android/app/src/main/java/com/kaixuan/opencode/pocket/plugins/MeetingRecordService.java",
            [
                ("VAD实例变量", r"private Object vad"),
                ("VAD_ENABLED常量", r"VAD_ENABLED\s*=\s*true"),
                ("MIN_PART_MS常量", r"MIN_PART_MS\s*=\s*500"),
                ("MAX_PART_MS常量", r"MAX_PART_MS\s*=\s*10000"),
                ("initializeVad方法", r"private Object initializeVad\(\)"),
                ("checkVadSpeechEnd方法", r"private boolean checkVadSpeechEnd"),
                ("releaseVad方法", r"private void releaseVad\(\)"),
                ("VAD动态切片逻辑", r"if \(vad != null && VAD_ENABLED\)"),
                ("VAD初始化调用", r"vad = initializeVad\(\)"),
                ("VAD释放调用", r"releaseVad\(\)"),
            ]
        ),
        "sherpa.ts": (
            f"{base}/frontend/src/native/sherpa.ts",
            [
                ("VadResult接口", r"export interface VadResult"),
                ("SherpaStatus包含vadReady", r"vadReady:\s*boolean"),
                ("createVad方法签名", r"createVad\(\).*Promise"),
                ("vadProcess方法签名", r"vadProcess\(.*audioPath"),
                ("createVad实现", r"createVad:.*reject"),
                ("vadProcess实现", r"vadProcess:.*reject"),
            ]
        ),
    }
    
    print("="*70)
    print("Phase 1: VAD延迟优化实施审计")
    print("="*70)
    print()
    
    total_passed = 0
    total_checks = 0
    failed_files = []
    
    for file_name, (file_path, checks) in audits.items():
        result = check_file(file_path, checks)
        status_icon = "✓" if result["status"] == "passed" else "✗"
        
        print(f"{status_icon} {file_name}")
        if result["status"] == "missing":
            print(f"  {result['details']}")
            failed_files.append(file_name)
        else:
            print(f"  通过: {result['passed']}/{result['total']}")
            for detail in result["details"]:
                print(f"    {detail['status']} {detail['name']}")
            
            if result["status"] == "passed":
                total_passed += 1
            else:
                failed_files.append(file_name)
            total_checks += 1
        print()
    
    # 检查文档
    docs = [
        f"{base}/docs/optimization/PHASE1-VAD-IMPLEMENTATION.md",
        f"{base}/docs/optimization/IMPLEMENTATION-PROGRESS.md",
    ]
    
    print("文档检查:")
    for doc in docs:
        if os.path.exists(doc):
            size = os.path.getsize(doc)
            print(f"  ✓ {os.path.basename(doc)} ({size} bytes)")
        else:
            print(f"  ✗ {os.path.basename(doc)} (不存在)")
            failed_files.append(os.path.basename(doc))
    print()
    
    # 检查测试脚本
    scripts = [
        f"{base}/backend/scripts/measure-latency.py",
        f"{base}/backend/scripts/test-vad-latency.sh",
    ]
    
    print("测试脚本检查:")
    for script in scripts:
        if os.path.exists(script):
            executable = os.access(script, os.X_OK)
            exec_icon = "✓" if executable or script.endswith('.py') else "⚠"
            print(f"  {exec_icon} {os.path.basename(script)}")
        else:
            print(f"  ✗ {os.path.basename(script)} (不存在)")
            failed_files.append(os.path.basename(script))
    print()
    
    # 总结
    print("="*70)
    print("审计总结")
    print("="*70)
    print(f"核心代码文件: {total_passed}/{total_checks} 通过")
    
    if not failed_files:
        print()
        print("🎉 Phase 1 实施完整，所有检查点通过！")
        print()
        print("下一步:")
        print("  1. 构建APK: cd frontend/android && ./gradlew assembleDebug")
        print("  2. 安装到设备: adb install -r app/build/outputs/apk/debug/app-debug.apk")
        print("  3. 运行测试: ./backend/scripts/test-vad-latency.sh")
        return 0
    else:
        print()
        print(f"⚠️  发现 {len(failed_files)} 个问题:")
        for f in failed_files:
            print(f"  - {f}")
        print()
        print("请修复上述问题后重新审计")
        return 1

if __name__ == '__main__':
    sys.exit(main())
