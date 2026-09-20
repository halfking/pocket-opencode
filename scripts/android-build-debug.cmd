@echo off
set JDK21_HOME=C:\Program Files\Eclipse Adoptium\jdk-21.0.12.101-hotspot
set JAVA_HOME=%JDK21_HOME%
set ANDROID_HOME=%LOCALAPPDATA%\Android
set PATH=%JAVA_HOME%\bin;%ANDROID_HOME%\cmdline-tools\latest\bin;%ANDROID_HOME%\platform-tools;%ANDROID_HOME%\emulator;%PATH%
cd C:\workspace\openpocket\frontend\android
echo --- JAVA ---
"%JAVA_HOME%\bin\java.exe" -version
echo --- gradle assembleDebug (5-15 min cold cache) ---
call gradlew.bat assembleDebug --no-daemon --console=plain
