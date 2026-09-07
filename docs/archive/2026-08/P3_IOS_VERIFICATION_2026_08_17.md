# P3 iOS 编译与核心流程验证报告（2026-08-16/17）

## 结论：✅ 通过（模拟器级）

09 篇 P3 门要求「iOS 只在构建、安装、核心导航和关键流程真实验证后标记支持；
未验证则标为预览」。本次在 macOS（Xcode 26.2）完成 **编译 → 安装 → 启动 → 登录 →
选实例 → 会话详情 → 历史回填 → diff 渲染** 全链路真实验证。按门口径，iOS 可从
「预览」升级为「模拟器已验证」；**真机（物理 iPhone）验证仍未做**，签名/发布链路
未验证。

## 环境

| 项 | 值 |
|---|---|
| 构建机 | macOS darwin 25.5.0 arm64, Xcode 26.2 (17C52) |
| 模拟器 | iPhone 16 Pro (`pocket-ios-test`)，iOS 18.6 runtime |
| 工程 | `frontend/ios/`（`npx cap add ios` 生成，Capacitor 8.4.1 SPM 模式，无 CocoaPods） |
| 依赖 | capacitor-swift-pm 8.5.0、@capacitor-community/sqlite（SQLCipher 4.17.0 二进制目标）、text-to-speech 8.0.2、@capacitor/app 8.1.1 |
| 后端链路 | 宿主 mock OpenCode 实例 → pocketd(:8088) → 模拟器（`VITE_API_BASE=http://localhost:8088`，iOS 模拟器与宿主共享网络） |

## 验证矩阵

| 步骤 | 结果 | 证据 |
|---|---|---|
| `xcodebuild build`（generic/platform=iOS Simulator） | ✅ BUILD SUCCEEDED（194 编译单元） | `/tmp/ios-build6.log` |
| `simctl install` + `launch` | ✅ 启动无崩溃 | pid 64533/74257 |
| 登录页渲染（WebView 加载本地打包资源） | ✅ 🦞 欢迎页/登录表单完整 | 截图 ios-launch.png |
| 登录（admin/admin → JWT） | ✅ 进入 AI 工具页 | 截图 ios-after-login.png |
| 实例列表/选择 | ✅ Mock Diff Baseline 选中回任务页 | 截图 ios-inst-selected.png |
| 会话详情 + 消息历史回填（890eb86 映射层） | ✅ 用户气泡（含📎附件行）、AI 文本、`edit 48.0s 完成`/`bash 14.0s 失败` 工具卡片 | 截图 ios-session2.png |
| Diff 工具卡展开（E5-S3 DiffBlock） | ✅ `1 个变更段 +3 -1 6 行` + 红绿配色渲染 | 截图 ios-diff.png |

截图存档：`/tmp/pocket-diff-baseline/ios-*.png`（会话存续期）。

## 受限网络下的构建要点（复现）

本机 HTTPS 直连 github.com 被限速/阻断（~100KB/s），SSH 正常。两个绕行手段
均为**本机/repo 局部、可逆**：

1. SPM git 依赖走 SSH：repo 本地 git 配置
   `url."git@github.com:".insteadOf "https://github.com/"`（作用于本仓库）。
2. SQLCipher 二进制目标（releases 下载 50MB xcframework.zip）：
   - `curl -C - --resolve github.com:443:140.82.112.3` 断点续传拉完
   - **SHA256 校验一致**：`dd5a650346c1ba9933d6ba179f8844e03e4a075b3dd3a892796149864cd9ae57`（与 manifest 声明完全匹配，供应链完整性确认）
   - 解压至 `build/SourcePackages/checkouts/SQLCipher.swift/SQLCipher.xcframework`，
     并把该 checkout 的 Package.swift 二进制目标改为 `path:` 引用（仅存在于被
     gitignore 的 build/ 内，不影响干净环境构建）。

干净网络环境下无需以上步骤，`npm install && npm run build && npx cap sync ios &&
xcodebuild` 即可。

## 已知限制 / 未验证项

- **物理真机未验证**（签名、安装到 iPhone、真机 WebView 行为）；按 P3 门，iOS
  当前状态为「模拟器已验证」，真机验证后才能标「支持」。
- 本次验证 build 使用 `VITE_API_BASE=http://localhost:8088`（模拟器可达宿主）；
  仓库默认构建是安卓模拟器的 `10.0.2.2:8088`，iOS 真机需要真实服务器地址——
  API 地址构建期注入的双平台差异是既有架构问题（安卓 assets 同样入库了
  10.0.2.2 构建），发布前需要按目标平台出包的流水线。
- iOS 端未创建主密码即进入了会话详情（安卓端 requiresLobster 会拦）。差异
  原因未深查（疑似 iOS 首启 lobster 初始化时序不同）；本地库在 iOS 上的加密
  行为需要专项验证后再放开「更多」里的敏感功能结论。
- 生产构建（Release/签名/归档上传）未做。

## 复现命令

```bash
cd frontend && npm install && npm run build && npx cap sync ios
xcodebuild -project frontend/ios/App/App.xcodeproj -scheme App \
  -destination 'generic/platform=iOS Simulator' \
  -derivedDataPath frontend/ios/build build
xcrun simctl create "pocket-ios-test" com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro com.apple.CoreSimulator.SimRuntime.iOS-18-6
xcrun simctl boot "pocket-ios-test"
xcrun simctl install "pocket-ios-test" frontend/ios/build/Build/Products/Debug-iphonesimulator/App.app
xcrun simctl launch "pocket-ios-test" com.kaixuan.opencode.pocket
# iOS 模拟器验证需 VITE_API_BASE=http://localhost:8088 重打 dist
```
