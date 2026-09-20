@echo off
REM ===========================================================================
REM  Real-device 30 min capture + summary (Windows, single shot)
REM    Run AFTER running preflight.cmd and pressing Home.
REM    Captures logcat for 30 min, then dumps a backfill-ready summary to
REM    logs/real-device-summary-YYYYMMDD-HHMMSS.txt so the user can paste it.
REM ===========================================================================

setlocal

if not defined LOCALAPPDATA set "LOCALAPPDATA=%USERPROFILE%\AppData\Local"
set "PATH=%LOCALAPPDATA%\Android\platform-tools;%PATH%"

cd /d "C:\workspace\openpocket"

set "STAMP=%DATE:~0,4%%DATE:~5,2%%DATE:~8,2%-%TIME:~0,2%%TIME:~3,2%%TIME:~6,2%"
set "STAMP=%STAMP: =0%"
set "OUT=logs\real-device-summary-%STAMP%.txt"
set "RAW=logs\real-device-rawcat-%STAMP%.txt"

mkdir logs >nul 2>&1

echo === Real-device capture @ %STAMP% ===
echo Logging to: %OUT% and %RAW%

echo ---------------------------------------- >  "%OUT%"
echo Real-device capture summary             >> "%OUT%"
echo Generated: %STAMP%                     >> "%OUT%"
echo Captured for 30 minutes                 >> "%OUT%"
echo ---------------------------------------- >> "%OUT%"

echo.
echo [1/3] Clearing logcat buffer...
adb logcat -c

echo [2/3] Capturing 30 minutes of logcat (Ctrl+C to abort)...
adb logcat -v time > "%RAW%" 2>&1

echo [3/3] Summarising key signals...
echo. >> "%OUT%"
echo -- Key signals (grep on %RAW%) ------ >> "%OUT%"
findstr /c:"AiStreamService onStartCommand" "%RAW%" | find /c /v "" > "%OUT%.tmp"
echo AiStreamService onStartCommand hits:    >> "%OUT%"
type "%OUT%.tmp"                              >> "%OUT%"
findstr /c:"keepalive sent" "%RAW%" | find /c /v "" > "%OUT%.tmp"
echo.                                         >> "%OUT%"
echo AiStreamKeepalive keepalive hits:        >> "%OUT%"
type "%OUT%.tmp"                              >> "%OUT%"
findstr /c:"Watchdog triggered" "%RAW%" | find /c /v "" > "%OUT%.tmp"
echo.                                         >> "%OUT%"
echo Watchdog triggered hits:                 >> "%OUT%"
type "%OUT%.tmp"                              >> "%OUT%"
findstr /C:"Killed.*com.kaixuan.opencode.pocket" "%RAW%" | find /c /v "" > "%OUT%.tmp"
echo.                                         >> "%OUT%"
echo "OEM Killed (com.kaixuan...) hits:       " >> "%OUT%"
type "%OUT%.tmp"                              >> "%OUT%"
del "%OUT%.tmp" 2>nul

echo. >> "%OUT%"
echo -- Process state at capture end -------- >> "%OUT%"
adb shell ps -A | findstr kaixuan             >> "%OUT%" 2>nul
adb shell dumpsys activity services | findstr kaixuan >> "%OUT%" 2>nul
adb shell dumpsys deviceidle | findstr "m. whitelist" >> "%OUT%" 2>nul
adb shell getprop ro.product.model            >> "%OUT%" 2>nul
adb shell getprop ro.build.version.release    >> "%OUT%" 2>nul

echo.
echo DONE - paste contents of %OUT% into the chat
echo Raw logcat retained at %RAW%
endlocal
