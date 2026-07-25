# Android 模拟器操作手册

## SDK 与模拟器路径

```bash
SDK=/Users/xutaohuang/Library/Android/sdk
ADB=$SDK/platform-tools/adb
EMULATOR=$SDK/emulator/emulator
```

当前 shell 的 `PATH` 不包含 Android SDK，必须使用绝对路径。

## 可用 AVD

```bash
$EMULATOR -list-avds
```

当前有：
- `Medium_Phone_API_36.1`（Google Play API 36.1 arm64）
- `pocket_test`（Android 11/API 30 arm64，本次使用）

## 启动模拟器

```bash
nohup $EMULATOR -avd pocket_test -no-snapshot -no-boot-anim \
  -gpu swiftshader_indirect -netdelay none -netspeed full \
  >/tmp/opencode-pocket-pocket-test-emulator.log 2>&1 </dev/null &
```

后台启动，日志写入 `/tmp/opencode-pocket-pocket-test-emulator.log`。

## 等待模拟器完成启动

```bash
for i in $(seq 1 90); do
  device=$($ADB devices | awk 'NR>1 && $1 ~ /^emulator-/ && $2 == "device" {print $1; exit}')
  if [ -n "$device" ]; then
    boot=$($ADB -s "$device" shell getprop sys.boot_completed 2>/dev/null | tr -d '\r')
    if [ "$boot" = "1" ]; then
      echo "device=$device boot_completed=1"
      exit 0
    fi
  fi
  sleep 2
done
$ADB devices -l
exit 1
```

超时 180 秒。

## 查看在线设备

```bash
$ADB devices -l
```

示例输出：
```
emulator-5554          device product:sdk_phone_arm64 model:Android_SDK_built_for_arm64 device:emulator_arm64 transport_id:5
```

## 构建并安装 APK

```bash
cd /path/to/opencode-pocket/frontend
npm run typecheck
npm run build
npx cap sync android

cd android
JAVA_HOME=/Library/Java/JavaVirtualMachines/jdk-21.jdk/Contents/Home \
  ./gradlew assembleDebug --offline --no-daemon

$ADB -s emulator-5554 install -r app/build/outputs/apk/debug/app-debug.apk
```

`-r` 覆盖安装已存在的应用。

## 启动应用

```bash
$ADB -s emulator-5554 shell am start -n com.kaixuan.opencode.pocket/.MainActivity
```

## 清理应用数据

```bash
$ADB -s emulator-5554 shell pm clear com.kaixuan.opencode.pocket
```

清理后首次启动会初始化本地数据库。

## 模拟点击

```bash
$ADB -s emulator-5554 shell input tap <x> <y>
```

坐标从屏幕左上角计算（1080x2340）。

## 截图

```bash
$ADB -s emulator-5554 exec-out screencap -p > screenshot.png
```

## 查看 UI hierarchy

```bash
$ADB -s emulator-5554 shell uiautomator dump /sdcard/ui.xml
$ADB -s emulator-5554 shell cat /sdcard/ui.xml
```

## 查看日志

```bash
# 实时日志
$ADB -s emulator-5554 logcat

# 过滤 Capacitor/SQLite
$ADB -s emulator-5554 logcat -s Capacitor:V CapacitorSQLite:V chromium:V

# 保存最近日志
$ADB -s emulator-5554 logcat -d > logcat.txt

# 清理日志
$ADB -s emulator-5554 logcat -c
```

## 网络验证

模拟器使用 `10.0.2.2` 访问宿主机：

```bash
$ADB -s emulator-5554 shell ping -c 1 10.0.2.2
```

前端编译时需配置：

```bash
# frontend/.env.local
VITE_API_BASE=http://10.0.2.2:8088
```

## 停止模拟器

```bash
$ADB -s emulator-5554 emu kill
```

或直接关闭模拟器窗口。

## JDK 选择

离线构建避免使用 GraalVM JDK，优先标准 Oracle/OpenJDK：

```bash
JAVA_HOME=/Library/Java/JavaVirtualMachines/jdk-21.jdk/Contents/Home
```

查看可用 JDK：

```bash
/usr/libexec/java_home -V
```

## 故障排查

### ADB 找不到设备

```bash
$ADB kill-server
$ADB start-server
$ADB devices -l
```

### 模拟器启动慢

检查日志：
```bash
tail -f /tmp/opencode-pocket-pocket-test-emulator.log
```

### APK 安装失败

卸载旧版本：
```bash
$ADB -s emulator-5554 uninstall com.kaixuan.opencode.pocket
```

### WebView 空白

检查 Capacitor 日志和网络权限：
```bash
$ADB -s emulator-5554 logcat -s Capacitor:V chromium:V
```

确认 `AndroidManifest.xml` 有 `INTERNET` 权限和 `usesCleartextTraffic`。
