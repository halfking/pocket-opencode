@echo off
setlocal
set "JAVA_HOME=C:\Program Files\Eclipse Adoptium\jdk-21.0.12.101-hotspot"
set "ANDROID_HOME=%LOCALAPPDATA%\Android"
set "PATH=%JAVA_HOME%\bin;%ANDROID_HOME%\emulator;%ANDROID_HOME%\platform-tools;%PATH%"

REM WHPX-accelerated launch (NOT -accel off — let hardware acceleration handle it).
"%ANDROID_HOME%\emulator\emulator.exe" ^
  -avd pocket-test ^
  -no-snapshot ^
  -no-window ^
  -no-audio ^
  -no-boot-anim ^
  -gpu swiftshader_indirect ^
  -no-snapshot-save ^
  > "C:\workspace\openpocket\logs\emulator-whpx.log" 2>&1

endlocal
