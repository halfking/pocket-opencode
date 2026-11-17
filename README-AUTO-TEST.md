# OpenPocket 自动化录音测试使用说明

快速体验全自动录音测试（无需人工介入）：

```bash
# 一键运行
./scripts/quick-test.sh
```

## 这个测试会做什么？

1. **自动导航** - Maestro 自动打开应用，进入录音页面
2. **自动录音** - 自动点击开始录音按钮
3. **自动注入音频** - 向设备注入 5 秒测试音频
4. **自动验证** - 检查 VAD、Paraformer、录音服务是否正常
5. **自动生成报告** - 输出 JSON 测试报告和详细日志

## 前置条件

- ✅ 真机通过 USB 连接并启用调试
- ✅ 已安装 Node.js 和 adb
- ✅ 应用已安装在设备上

## 查看结果

测试完成后会显示:
```
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
测试结果总结
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  通过: 4
  失败: 1
  日志: /tmp/openpocket-recording-test-20261117-143025.log
  报告: /tmp/openpocket-test-result-20261117-143025.json
```

查看详细信息:
```bash
# 查看日志
cat /tmp/openpocket-recording-test-*.log

# 查看 JSON 报告
cat /tmp/openpocket-test-result-*.json | jq .

# 查看截图
open logs/maestro/
```

## 完整文档

详细说明请查看: [docs/testing/auto-recording-test-guide.md](docs/testing/auto-recording-test-guide.md)
