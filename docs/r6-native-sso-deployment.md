# R6 原生企业登录部署

Pocketd 的浏览器入口仍由 `POCKET_REDCLAW_AUTH_AGENT_PUBLIC_URL` 指定。Casdoor 的精确回调仍是 Pocketd 的 `/api/auth/sso/callback`。给原生 App 单独分配 HTTPS 域名，把该 origin 写入 Pocketd 的 `POCKET_REDCLAW_SSO_APP_ORIGIN` 和发行前端的 `VITE_SSO_APP_LINK_ORIGIN`。两处使用相同的域名，无尾随斜杠或端口。App Link 域名应与 Pocketd 回调域名不同。

Android 发行构建在 `build-mobile.mjs` 和 Gradle 打包时传入同一 `VITE_SSO_APP_LINK_ORIGIN`。合并清单应只将该域的 `/auth/sso/app-return` 交给 App。用发行包的实际 application ID 和签名证书 SHA-256 指纹生成 `/.well-known/assetlinks.json`，在真机检查系统验证状态。[Android App Links 验证](https://developer.android.com/training/app-links/verify-applinks)

iOS 发行签名须具备 Associated Domains。归档时传入 Xcode 构建设置 `CODE_SIGN_ENTITLEMENTS=App/OpenPocketSSO.entitlements` 和 `OPENPOCKET_SSO_APP_HOST=<不带协议的回跳域名>`。使用发行产物的实际 application identifier 在该域发布 `/.well-known/apple-app-site-association`，将 association 限制到 `/auth/sso/app-return`，核对签名内的 entitlement。[Apple Associated Domains](https://developer.apple.com/documentation/xcode/supporting-associated-domains) [Safari 跨域回跳](https://developer.apple.com/documentation/technotes/tn3155-debugging-universal-links)

确认两个关联文件都能在回跳域通过 HTTPS 直接读到。反代可把回跳域的精确路径 `/auth/sso/app-return` 转给 Pocketd，向浏览器显示未能打开 App 时的提示。Pocketd API 的 CORS 配置须包含发行 WebView 的实际 origin。Auth Agent 的内部回调和续期仍由 Pocketd 调用。

## 从签名产物生成关联文件

在 macOS 上用最终分发的 Android APK 和 iOS 真机 `.app` 生成关联文件。Android 证书指纹与 iOS application identifier 从批准的发行记录取得。脚本读取产物并核验签名、回跳域和 WebView 构建配置，结果保存在新目录。

```bash
python3 scripts/prepare-r6-app-associations.py \
  --origin "$VITE_SSO_APP_LINK_ORIGIN" --output ./r6-app-links \
  --android-apk "$R6_ANDROID_APK" --android-fingerprint "$R6_ANDROID_CERT_SHA256" \
  --ios-app "$R6_IOS_APP" --ios-app-id "$R6_IOS_APP_ID"
```

Android SDK 工具若未加入 PATH，用 `--apksigner` 和 `--apkanalyzer` 指定路径。可单独提供一个平台的产物及其对应身份。按回跳域的部署流程发布生成的 `assetlinks.json` 和 `apple-app-site-association`，随后带 `--check-hosted` 与一个新的 `--output` 目录复验线上 HTTPS 正文。Android 分发链路和 iOS entitlement 均以签名产物为准。[apksigner](https://developer.android.com/tools/apksigner) [Apple 签名 entitlement](https://developer.apple.com/library/archive/technotes/tn2415/)

## 真机验收

App 发起登录时生成本次验证值，只把 SHA-256 challenge 交给系统浏览器。浏览器验证成功后，由用户点击「返回 OpenPocket」。回跳链接只携带一次性交接码和 challenge，App 在原登录视图中校验这次尝试，再向 Pocketd 兑换。兑换窗口为 90 秒。App 被系统终止或登录视图已退出时，需要重新发起登录。验收覆盖关闭浏览器、错误域名、不同登录尝试、重复点击、工作区绑定、续期和登出。
