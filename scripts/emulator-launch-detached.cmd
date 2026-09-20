@echo off
setlocal
set "JAVA_HOME=C:\Program Files\Eclipse Adoptium\jdk-21.0.12.101-hotspot"
set "ANDROID_HOME=%LOCALAPPDATA%\Android"
set "PATH=%JAVA_HOME%\bin;%ANDROID_HOME%\emulator;%ANDROID_HOME%\platform-tools;%PATH%"

REM Launch emulator detached, write all stdout/stderr to a file.
REM Caller should keep this CMD running for as long as the emulator session needs.
"%ANDROID_HOME%\emulator\emulator.exe" ^
  -avd pocket-test ^
  -no-snapshot ^
  -no-window ^
  -no-audio ^
  -no-boot-anim ^
  -gpu swiftshader_indirect ^
  -accel off ^
  -no-snapshot-save ^
  -verbose ^
  > "C:\workspace\openpocket\logs\emulator-detached.log" 2>&1

endlocal
