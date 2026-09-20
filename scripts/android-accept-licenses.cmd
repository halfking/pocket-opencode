@echo off
set JAVA_HOME=C:\Program Files\AdoptOpenJDK\jdk-17.0.0.20-hotspot
set ANDROID_HOME=%LOCALAPPDATA%\Android
set PATH=%JAVA_HOME%\bin;%ANDROID_HOME%\cmdline-tools\latest\bin;%PATH%
call "%ANDROID_HOME%\cmdline-tools\latest\bin\sdkmanager.bat" --licenses < C:\Users\86133\AppData\Local\Temp\yes.txt
