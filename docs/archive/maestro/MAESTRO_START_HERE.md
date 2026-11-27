# 🚀 OpenPocket Maestro 测试 - 从这里开始

**欢迎使用 OpenPocket Maestro 自动化测试框架！**

这是你的入口文档，帮助你在 5 分钟内开始第一个测试。

---

## ⚡ 最快开始方式（1 分钟）

```bash
# 一键启动向导（推荐）
./scripts/maestro-wizard.sh
```

这个交互式向导会：
- ✅ 自动检查环境
- ✅ 引导你配置参数
- ✅ 选择测试套件
- ✅ 执行测试
- ✅ 生成报告

---

## 📚 文档导航

### 我是新手，想快速上手
👉 **阅读**: [MAESTRO_QUICK_START.md](MAESTRO_QUICK_START.md)  
⏱️ **时间**: 5-10 分钟  
📖 **内容**: 环境配置、首次测试、结果查看

### 我想了解完整的测试计划
👉 **阅读**: [MAESTRO_TEST_PLAN.md](MAESTRO_TEST_PLAN.md)  
⏱️ **时间**: 20-30 分钟  
📖 **内容**: 测试策略、套件说明、覆盖范围

### 我想学习最佳实践
👉 **阅读**: [MAESTRO_BEST_PRACTICES.md](MAESTRO_BEST_PRACTICES.md)  
⏱️ **时间**: 30-40 分钟  
📖 **内容**: 测试编写技巧、常见陷阱、调试方法

### 我想查看完整交付内容
👉 **阅读**: [MAESTRO_README.md](MAESTRO_README.md)  
⏱️ **时间**: 10-15 分钟  
📖 **内容**: 完整索引、文件清单、快速导航

### 我想查看最终交付总结
👉 **阅读**: [MAESTRO_COMPLETE.md](MAESTRO_COMPLETE.md)  
⏱️ **时间**: 5 分钟  
📖 **内容**: 交付统计、验收确认、成就徽章

---

## 🎯 常见使用场景

### 场景 1: 我刚提交了代码，想快速验证

```bash
./scripts/run-maestro-suite.sh smoke
```

⏱️ **时间**: 3-5 分钟  
✅ **验证**: 核心功能未破坏

### 场景 2: 我准备提交 PR

```bash
./scripts/run-maestro-suite.sh regression
```

⏱️ **时间**: 10-12 分钟  
✅ **验证**: 无功能回归

### 场景 3: 准备发布新版本

```bash
./scripts/run-maestro-suite.sh master
```

⏱️ **时间**: 15-20 分钟  
✅ **验证**: 完整功能测试

### 场景 4: 想看性能指标

```bash
./scripts/run-performance-test.sh
```

⏱️ **时间**: 2-3 分钟  
✅ **验证**: 启动时间、内存、APK 大小

### 场景 5: 想看测试报告

```bash
./scripts/generate-maestro-report.sh
```

⏱️ **时间**: 10 秒  
✅ **输出**: 精美的 HTML 报告

---

## 🛠️ 快速命令参考

```bash
# 环境验证
./scripts/verify-maestro-env.sh

# 测试执行
./scripts/run-maestro-suite.sh [smoke|regression|master|all]

# 性能测试
./scripts/run-performance-test.sh

# 生成报告
./scripts/generate-maestro-report.sh

# 一键向导
./scripts/maestro-wizard.sh

# 单个测试
maestro test .maestro/notes-crud.yaml
```

---

## 📊 当前测试覆盖

```
总功能域: 34 个
已覆盖: 23 个 (68%)

分层覆盖:
├─ L0 认证: 100% ✅
├─ L1 核心: 82% 🟢
├─ L2 扩展: 42% 🟡
└─ L3 高级: 27% 🟡

测试套件: 6 个
测试文件: 59 个
执行脚本: 5 个
文档文件: 8 个
```

---

## ❓ 常见问题

### Q: 我需要安装什么？

**A**: 需要：
- Maestro CLI
- Android SDK (ADB)
- 已连接的 Android 设备或模拟器

运行 `./scripts/verify-maestro-env.sh` 检查。

### Q: 测试失败了怎么办？

**A**: 
1. 查看日志：`logs/maestro/*.log`
2. 查看截图：`logs/maestro/*.png`
3. 参考故障排查：[MAESTRO_QUICK_START.md](MAESTRO_QUICK_START.md) 第 9 节

### Q: 如何添加新测试？

**A**:
1. 参考现有测试：`.maestro/*.yaml`
2. 学习最佳实践：[MAESTRO_BEST_PRACTICES.md](MAESTRO_BEST_PRACTICES.md)
3. 复制模板开始编写

### Q: CI/CD 如何集成？

**A**:
GitHub Actions 配置已就绪：`.github/workflows/maestro-tests.yml`

---

## 🎓 学习路径

### 第 1 天
1. ✅ 运行一键向导
2. ✅ 执行冒烟测试
3. ✅ 查看测试报告

### 第 1 周
1. ✅ 阅读测试计划
2. ✅ 运行所有套件
3. ✅ 了解覆盖范围

### 第 1 月
1. ✅ 学习最佳实践
2. ✅ 编写自定义测试
3. ✅ 集成到工作流

---

## 📞 获取帮助

- 📖 查看文档目录（见上方）
- 🐛 报告问题：GitHub Issues
- 💡 功能建议：Pull Request
- 📧 联系团队：项目维护者

---

## 🎉 准备好了吗？

### 立即开始！

```bash
# 运行一键向导
./scripts/maestro-wizard.sh
```

或者

```bash
# 快速验证
./scripts/verify-maestro-env.sh
./scripts/run-maestro-suite.sh smoke
```

---

**祝测试顺利！** 🚀

如有问题，随时查看文档或联系团队。
