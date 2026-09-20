@echo off
REM ===========================================================================
REM  Real-device preflight (Windows, single shot)
REM    Run on the host with phone plugged in + USB debugging on.
REM    One-shot does: PATH setup + adb devices + install + start + battery WL.
REM    Stream-of-logcat tag auto-summary is in real-device-capture.cmd.
REM ===========================================================================

setlocal

if not defined LOCALAPPDATA set "LOCALAPPDATA=%USERPROFILE%\AppData\Local"
set "ANDROID_TOOLS=%LOCALAPPDATA%\Android\platform-tools"
set "PATH=%ANDROID_TOOLS%;%PATH%"

cd /d "C:\workspace\openpocket"

echo === Real-device preflight @ %DATE% %TIME% ===
echo.

echo [1/5] adb devices...
adb devices
adb start-server >nul 2>&1
if errorlevel 1 (
  echo FAIL: adb not responsive
  exit /b 1
)

echo.
echo [2/5] install app-debug.apk (28.9 MB)...
adb install -r "frontend\android\app\build\outputs\apk\debug\app-debug.apk"
if errorlevel 1 (
  echo FAIL: install
  exit /b 1
)

echo.
echo [3/5] start MainActivity...
adb shell am start -n com.kaixuan.opencode.pocket/.MainActivity
timeout /t 2 /nobreak >nul

echo.
echo [4/5] open battery optimization whitelist page...
adb shell am start -a android.settings.IGNORE_BATTERY_OPTIMIZATION_SETTINGS
echo (please tap "Allow" for OpenCode Pocket in the dialog)

echo.
echo [5/5] clear logcat and listen to ai-stream keepalive (Ctrl+C to stop)...
adb logcat -c
adb logcat -v time AiStreamService:V AiStreamKeepalive:V *:S

endlocal
