# scripts/ 目录索引

> 401 个脚本，按用途分类。**文件位置保持不动**（package.json / CI / 文档有 59+ 处路径引用），
> 本文件只做导航。新增脚本请按分类放到对应小节的列表里。

## 分类总览（2026-11-27 审计）

| 分类 | 数量（约） | 代表脚本 | 说明 |
|------|-----------|---------|------|
| audit-* | 25+ | audit-i18n-keys.mjs | 静态审计卡口（i18n/路由/模型列表/跳过点） |
| cdp-* | 15+ | cdp-notes-probe.mjs | CDP 浏览器自动化探针 |
| maestro 相关 | 8 | run-maestro-suite.sh | 移动端 UI 测试套件（CI 用） |
| test-* / verify-* | 25+ | verify-knowledge-schema.sh | 功能验证与 API 冒烟 |
| build / deploy | 5 | build-deploy.sh, start-dev.sh | 构建与本地部署 |
| seed / 数据 | 5 | seed_email_accounts.sh | 种子数据灌入 |
| 性能 / 录音 | 8 | monitor-performance.sh | 性能监控与录音端到端 |
| __tests__ | 1 | test_r6_app_associations.py | Python 单测（r6 关联准备器） |

## CI 关键入口（.github/workflows/maestro-tests.yml 引用）

- `run-maestro-suite.sh smoke` — 冒烟套件
- `run-maestro-suite.sh regression` — 回归套件
- `verify-maestro-env.sh` — 环境自检
- `generate-maestro-report.sh` — 报告生成

## Maestro 工具族

| 脚本 | 用途 |
|------|------|
| maestro-bootstrap.sh | 安装/初始化 Maestro 环境 |
| maestro-run-all.sh | 全套件顺序执行 |
| maestro-bisect.sh | 二分定位失败用例 |
| maestro-wizard.sh | 交互式选择用例执行 |
| run-email-tests.sh | 邮箱密码管理专项 |
| run-performance-test.sh | 性能口径测试 |
| verify-complete.sh / verify-email-password-manual.sh | 人工验收辅助 |

## 维护约定

1. **不要移动现有脚本**——package.json scripts、CI workflow、docs 引用会断。
2. 新增审计卡口脚本命名 `audit-<topic>.mjs`，与 backend/internal/repohygiene 的门禁分开（那是 Go 侧）。
3. 一次性验证脚本（历史交付物）若 30 天无人引用，移入 `scripts/archive/` 并在本文件登记。
