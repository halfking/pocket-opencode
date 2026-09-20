@echo off
set JAVA_HOME=C:\Program Files\Eclipse Adoptium\jdk-21.0.12.101-hotspot
set ANDROID_HOME=%LOCALAPPDATA%\Android
set PATH=%JAVA_HOME%\bin;%ANDROID_HOME%\cmdline-tools\latest\bin;%ANDROID_HOME%\platform-tools;%ANDROID_HOME%\emulator;%PATH%
echo --- list AVD ---
"%ANDROID_HOME%\cmdline-tools\latest\bin\avdmanager.bat" list avd
echo --- start pocket-test emulator ---
"%ANDROID_HOME%\emulator\emulator.exe" -avd pocket-test -no-snapshot -no-window -no-audio -no-boot-anim -gpu swiftshader_indirect -accel off
