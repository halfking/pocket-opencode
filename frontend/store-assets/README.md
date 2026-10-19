# 应用商店发布资源包 — AI Work Mess（openpocket）

> 产物来源：基座 vibe-coding rule 69 SSOT（`spec/app-icon/categories.yaml`，ai-agent 类目），
> 由 `python3 <vibe-coding>/scripts/gen-app-icons.py --export-store --category=ai-agent --dest=frontend/store-assets` 生成。
> **禁止手改**；改图标只改 SSOT 再重跑（rule 69 铁律）。
## 文件清单

| 文件 | 用途 | 平台要求 |
|------|------|----------|
| `play-store-icon-512.png` | Google Play 商店图标 | 512×512，32-bit PNG（含 alpha）；Play 会自动圆角裁切，勿再带圆角/透明边框外的装饰 |
| `feature-graphic-1024x500.png` | Google Play Feature Graphic | 1024×500，PNG/JPEG；中央 320px 安全区已置图标，边缘无关键信息 |
| `logo-icon.{webp,png}` / `logo-icon-dark.{webp,png}` | 品牌物料（官网/发布会/商店页头图，1024²，亮暗双皮肤） | 自带圆角底透明四角，可直接叠加浅/深背景 |

## Google Play（Android）提交清单

- [ ] 应用名：AI Work Mess（`app_name` 已配置）
- [ ] 包名：`com.kaixuan.opencode.pocket`（已固化，勿改——改了相当于新应用）
- [ ] `versionCode` 单调递增（当前 3）；`versionName` 当前 `1.2.0-openpocket`——上架建议去后缀改 `1.2.0`
- [ ] targetSdk 35（满足 2026 Play API 级要求）
- [ ] AAB 上传（Play Console → Production → Create release；`./gradlew :app:bundleRelease`）
- [ ] 商店图标：`play-store-icon-512.png`
- [ ] Feature Graphic：`feature-graphic-1024x500.png`
- [ ] 手机截图 ≥2 张（最小边 ≥ 320px，建议 1080×1920；可截自真机）
- [ ] 数据安全表单：按实际（应用含录音/语音识别，需申报麦克风数据收集用途）
- [ ] 隐私政策 URL（必须，含麦克风/语音数据说明）
- [ ] 内容分级问卷

## Apple App Store（iOS）提交清单

- [ ] 显示名：AI Work Mess（`CFBundleDisplayName` 已配置）
- [ ] Bundle ID：与 Xcode 工程一致；`MARKETING_VERSION=1.0 / CURRENT_PROJECT_VERSION=1`——首次提审前统一为 `1.2.0 (3)` 与 Android 对齐
- [ ] App 图标：已由 Assets.xcassets AppIcon.appiconset 提供（1024 亮/暗/着色三张，iOS 18 全皮肤）
- [ ] `ITSAppUsesNonExemptEncryption = NO`（合规导出声明；已补入 Info.plist——HTTPS/TLS 属豁免加密）
- [ ] `NSMicrophoneUsageDescription` / `NSSpeechRecognitionUsageDescription` 中文文案已配置（需在审核备注里说明用途）
- [ ] 截图：6.9"/6.5" iPhone 必需（1320×2868 / 1284×2778）；iPad（TARGETED_DEVICE_FAMILY=1,2）需 13" iPad 截图
- [ ] 隐私政策 URL + App 隐私标签（麦克风、语音识别数据申报）
- [ ] 上传：Xcode → Product → Archive → Distribute App（App Store Connect）

## 截图生成建议

真机截图后可批量套框（fastlane frameit 或手动）；本项目 UI 为中文，
截图文案建议：会议列表 / 实时转写 / 智能摘要 三张核心场景。
