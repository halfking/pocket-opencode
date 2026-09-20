@echo off
set JAVA_HOME=C:\Program Files\AdoptOpenJDK\jdk-17.0.0.20-hotspot
set ANDROID_HOME=%LOCALAPPDATA%\Android
set PATH=%JAVA_HOME%\bin;%ANDROID_HOME%\cmdline-tools\latest\bin;%ANDROID_HOME%\platform-tools;%ANDROID_HOME%\emulator;%PATH%
set SDKMANAGER=%ANDROID_HOME%\cmdline-tools\latest\bin\sdkmanager.bat
echo --- platform-tools ---
call "%SDKMANAGER%" "platform-tools"
echo --- platforms;android-34 ---
call "%SDKMANAGER%" "platforms;android-34"
echo --- build-tools;34.0.0 ---
call "%SDKMANAGER%" "build-tools;34.0.0"
echo --- emulator ---
call "%SDKMANAGER%" "emulator"
echo --- system-images;android-34;google_apis;x86_64 ---
call "%SDKMANAGER%" "system-images;android-34;google_apis;x86_64"
echo --- done ---
