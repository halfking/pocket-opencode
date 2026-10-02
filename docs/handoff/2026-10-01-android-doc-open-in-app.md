# 2026-10-01 · 真机「打开方式」弹窗归零 + 文档打开内置化

> 目标：真机测试时系统总是弹「打开方式」选择框；文档打开必须是应用内置的。
> **完成度以本文「已验证」一节的真机证据为准。** 全部证据来自 Redmi 2411DRN47C
> （Android 14 / API 34，WebView 126.0.6478.71）。

---

## 0. 一句话结论

真机复现出**两个独立缺陷**，都让「文档打开」无法内置；两者都已修复并真机验证：

| # | 缺陷 | 表现 | 修法 |
|---|---|---|---|
| **D1** | 导出走 `@capacitor/share` | 任何导出入口都拉起系统选择框 | 新增原生 `Document` 插件，MediaStore 静默落盘到系统「下载」目录 |
| **D2** | 发票文件用相对 URL 取 | 真机上取回来的是 `index.html`，不是 PDF | 改用 `resolveRuntimeApiBase()` 拼绝对地址 + `assertNotHTML` 守卫 |

D2 是本轮**过程中才发现的既有缺陷**，与 D1 独立：即使不弹窗，应用在真机上
也根本拿不到发票字节。详见 §3。

---

## 1. D1 根因：`Share.share` 就是那个「打开方式」框

`frontend/src/utils/download.ts` 的原生分支写文件到 Cache 后调
`Share.share({ dialogTitle: '保存或分享文件' })`。真机复现（`scripts/cdp-doc-share-shot.mjs`）：

```
BEFORE top = com.kaixuan.opencode.pocket/.MainActivity
fired      = dispatched
t=2000ms   top = android/com.android.internal.app.MiuiChooserActivity   ← 系统选择框
t=4000ms   top = android/com.android.internal.app.MiuiChooserActivity
```

截图（`.scratch/share-t2000.png`）里弹层标题正是 **「保存或分享文件」**，
候选只有 QQ 的几个动作 + 取消。这台机器上能处理 `ACTION_SEND + application/pdf`
的应用就那几个，MIUI 因此把它渲染成 chooser。

> ⚠️ **一次差点被骗过去的证据**：接手时设备上已经停着一个
> `MiuiResolverActivity`，intent 是 `file:///sdcard/Download/voice-test.wav`、
> `launchedFromPackage=com.android.shell` —— 那是上一轮会话用 adb shell 测语音
> 留下的残留，**不是**本 App 触发的。清掉 `voice-test.wav` 并重建干净基线
> （`baseline resolver tasks = []`）之后才拿到上面的对照证据。
> 这与 `docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md` §4.5 记的是同一类教训。

**同时被证伪的假设**：`<iframe src="blob:...pdf">` 才是弹窗元凶。
实测注入 PDF blob 到 iframe，`top` 始终是 `MainActivity`，无任何 chooser，
但**预览区全白** —— Android WebView 内核压根不渲染 PDF。两条链路要分开修。

---

## 2. 修法：原生 `Document` 插件（无第三方依赖）

新增 `frontend/android/.../plugins/DocumentPlugin.java`，在 `MainActivity.onCreate` 注册：

| 方法 | 用途 |
|---|---|
| `pdfInfo({path})` | 页数 |
| `renderPdfPage({path,page,width})` | `android.graphics.pdf.PdfRenderer` 逐页栅格化成 PNG（base64 data URL） |
| `saveToDownloads({path,filename,mimeType})` | API 29+ 走 `MediaStore.Downloads` 静默落盘；≤28 回落公共下载目录 |

- **不引第三方 PDF 库**：复用系统自带 `PdfRenderer`（API 21+，本机 34）。
- **MediaStore 落盘零权限**（API 29+）；manifest 只补了
  `WRITE_EXTERNAL_STORAGE maxSdkVersion=28` 覆盖老系统回落分支。
- 同名文件不覆盖，按 `name (1).pdf` 递增（真机上已观测到 `(1)…(5)`）。
- 预览渲染铺白底（`bitmap.eraseColor(Color.WHITE)`），否则深色 sheet 上是黑底。

前端接线：

| 文件 | 改动 |
|---|---|
| `frontend/src/native/document.ts` | 插件 TS 绑定。**用非 thenable 盒子 `{value}` 装载**（BUG-G 的 thenable 陷阱，注释已写明） |
| `frontend/src/composables/usePdfViewer.ts` | 写 Cache → `pdfInfo` → `renderPdfPage`，带翻页、过期令牌防竞态、单次最多 50 页 |
| `InvoicePreviewSheet.vue` | Android PDF 走 `<img>` 位图 + 翻页条；web/iOS 保留 `<iframe>`。watch 用 `flush:'post'`，否则 Teleport 里的容器还没挂上就量不到宽度 |
| `utils/download.ts` | 导出函数改为返回落盘位置文案；Android 走 `saveToDownloads`，iOS 保留 Share，harmony 仍抛 `DownloadUnsupportedError` |
| `use-invoice-list.ts` / `InvoiceListView.vue` | 预览透传 `blob`（Android 渲染用）与 `docKey`；toast 带上保存位置 |
| `flashcardIo.ts` | 闪卡 JSON 导出在 Android 上改走同一条静默落盘（原 `Share` 同样会弹框） |
| `SessionDetailView.vue` / `SessionDetailDrawer.vue` | 导出 toast 改用返回值 |
| `scripts/android-apk-classes-fast.ps1` | 补 `DocumentPlugin` 针脚，让 DEX 审计能守住这个插件 |

---

## 3. D2 根因：发票文件请求用了相对 URL（既有缺陷）

真机 E2E 第一次跑出：

```
sheet = {"hasSheet":true,"iframes":0,"img":null,
         "msg":"无法预览此文件：pdf open failed: file not in PDF format or corrupted"}
```

把 App 写出去的「PDF」拉下来一看，是 `<!doctype html><html lang="zh-CN">` ——
**Capacitor WebView 自己的 index.html**（1620 字节）。

`frontend/src/api/email.ts` 的三个 blob 取法绕过了 `http()`，直接裸 `fetch('/api/…')`：

```ts
// 修复前
const res = await fetch(`/api/emails/invoices/${id}/file`, { … })
// http() 是拼了 base 的：
fetch(`${resolveRuntimeApiBase()}${path}`, …)   // api/http.ts:99
```

APK 里页面 origin 是 `https://localhost`，相对路径 `/api/*` 落到 WebView 本地资源服务，
被 SPA 兜底成 `index.html`（200, text/html）。这与 `scripts/build-mobile.mjs` 头部
警告的是同一类事故，`api/jsonGuard.ts` 本来就是为它准备的守卫，只是这三个函数没走。

修法：三者都改用 `resolveRuntimeApiBase()` 拼绝对地址，并加 `assertNotHTML(res)`，
让「打包漏注入 API base」这类问题**显式失败**，而不是把 HTML 当发票存进下载目录。
修复后真机落盘文件从 1620 字节变成 **157615 字节的真实 PDF**。

---

## 4. 已验证（真机，红米 2411DRN47C / WebView 126.0.6478.71）

复现脚本与验收脚本都留在仓库里，可重跑：

| 脚本 | 用途 |
|---|---|
| `scripts/cdp-doc-share-shot.mjs` | 弹「打开方式」的**复现器**（回归时用来确认前提还在） |
| `scripts/verify-doc-inapp.mjs` | 插件级验收：`saveToDownloads` / `pdfInfo` / `renderPdfPage` |
| `scripts/verify-doc-inapp-e2e.mjs` | **真实 UI 端到端**：登录 → 解锁 → 发票列表 → 打开预览 → 点下载 |
| `scripts/cdp-doc-visual-proof.mjs` | 把渲染结果挂到页面截图留证 |

### 4.1 真实 UI 端到端（最终结果）

```
route = #/email/invoices | cards = 6
open  = clicked: 杭州创客家投资管理有限公司 ¥3,500.00 …
sheet = {"hasSheet":true,"iframes":0,"img":"720x479","msg":null,"pager":null}
clicked 下载文件
top after = com.kaixuan.opencode.pocket/.MainActivity
>>> PASS：下载未拉起系统选择框
Downloads: 其他-杭州创客家投资管理有限公司-3500.00-2026-10-01 (5).pdf  157615

发票预览内置渲染（无 iframe + 有位图）: PASS
下载不弹系统「打开方式」            : PASS
```

截图：`.scratch/e2e-1-invoice-preview.png` —— 发票在 App 内 bottom sheet 里
直接渲染，二维码/发票号/金额/开户行全部清晰可读。

### 4.2 插件级

```
saveToDownloads -> {"name":"verify-doc-…pdf","uri":"content://media/external/downloads/1000000170",
                    "bytes":157615,"location":"Download"}
on device: -rwxrwx--- 1 u0_a208 media_rw 157615 /sdcard/Download/verify-doc-….pdf
pdfInfo        -> {"pageCount":1}
renderPdfPage  -> {"page":0,"width":1080,"height":719,"bytes":181294,
                   "head":"data:image/png;base64,"}
mounted img    -> {"w":1080,"h":719}      ← 浏览器确实解码出来了
```

### 4.3 静态门禁

| 检查 | 结果 |
|---|---|
| `npx vue-tsc --noEmit` | EXIT=0 |
| `npm run test:native` | 44/44（新增 `document.test.mjs` 6 条） |
| `npm run check:vm-gaps` | 命中 0 = 阈值 |
| `npm run check:icons` | 全部在字体子集内（新增 `chevron_left/right`） |
| `gradlew assembleDebug` | BUILD SUCCESSFUL |
| `android-apk-classes-fast.ps1` | `[+] FOUND …: DocumentPlugin`，14/15（`MainApplication` 缺失为预期） |

---

## 5. 未验证 / 已知边界

- **iOS 与 harmonyOS 未验证**。本轮只按用户确认的「原生 PdfRenderer」路线做了 Android；
  `hasNativeDocumentSupport()` 在这两平台返回 `false`，预览回退 `<iframe>`、
  导出回退系统分享面板（iOS 无 MediaStore 等价物）。
- **多页 PDF 的翻页条未在真机上看到**。在测的这张发票只有 1 页
  （`pageCount: 1`），`pageCount > 1` 才渲染翻页条，那条分支只经过类型检查。
- **导出到公共目录的 ≤28 回落分支未实测**，本机是 API 34，只走了 MediaStore。
- **`cap sync` 在 `build-mobile.mjs` 里偶发失败**（`exit=null`），本轮两次遇到；
  手工 `npx cap sync android` 均成功。Gradle 会在 sync 失败时静默沿用旧资源
  （本轮遇到过一次「401 up-to-date」导致 APK 是旧的），**下轮构建务必确认
  `√ Copying web assets` 出现过再打 APK**，或直接用
  `scripts/apk-assert-scheme.mjs` 那类产物回读断言。
