# OpenCode Pocket — Capacitor 原生壳

> **本目录不存放原生工程。** 原生工程由 Capacitor CLI 生成在
> **`../frontend/android/`**，即 `appId = com.kaixuan.opencode.pocket`。
>
> 这样做的原因：`frontend/` 才是本项目唯一装过 `@capacitor/*` 的地方
> （CLI 8.4.1 / android 8.5.0 已在 `frontend/node_modules`）。若把
> `android/` 当项目根，就要在这里维护**第二套** npm 依赖树，两套 CLI 版本
> 迟早漂移；而且 `cap add android` 会生成 `android/android/` 这种嵌套目录。
>
> 常用命令（在 `../frontend/` 下执行）：
> ```bash
> npx cap add android     # 首次生成原生工程
> npx cap sync android    # 改完 web/ 或插件后同步（含复制 dist 与原生插件）
> npx cap open android    # 用 Android Studio 打开
> ```

## 原生侧职责

- **后台录音前台服务**（`BackgroundMic` 插件）——Android 14+ 必须声明
  `foregroundServiceType="microphone"` 并申请 `FOREGROUND_SERVICE_MICROPHONE`，
  否则切后台录音会被系统直接掐断。
- 推送通知下发
- 生物识别解锁桥
- 折叠屏姿态 / window metrics 桥
- deep link 派发
- 离线队列恢复

## 现状

- `../frontend/src/native/background-mic.ts` 是**已写完的 TS 桥**
  （`listInputs/start/stop/partReady/error`）。
- 原生实现已落在 `../frontend/android/app/src/main/java/.../backgroundmic/`。
- 两者靠 `registerPlugin('BackgroundMic')` 的插件名对齐——**改名字要两边一起改**，
  否则 `start()` 会 reject，调用方静默退回 `getUserMedia`，切后台即断录。
