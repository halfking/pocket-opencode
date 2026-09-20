@echo off
set JAVA_HOME=C:\Program Files\AdoptOpenJDK\jdk-17.0.0.20-hotspot
set ANDROID_HOME=%LOCALAPPDATA%\Android
set PATH=%JAVA_HOME%\bin;%ANDROID_HOME%\cmdline-tools\latest\bin;%ANDROID_HOME%\platform-tools;%ANDROID_HOME%\emulator;%PATH%
"%ANDROID_HOME%\cmdline-tools\latest\bin\sdkmanager.bat" system-images;android-34;google_apis;x86_64
