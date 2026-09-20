@echo off
set JAVA_HOME=C:\Program Files\AdoptOpenJDK\jdk-17.0.0.20-hotspot
set ANDROID_HOME=%LOCALAPPDATA%\Android
set PATH=%JAVA_HOME%\bin;%ANDROID_HOME%\cmdline-tools\latest\bin;%ANDROID_HOME%\platform-tools;%ANDROID_HOME%\emulator;%PATH%
echo --- create Pixel 6 AVD (force overwrite if exists) ---
"%ANDROID_HOME%\cmdline-tools\latest\bin\avdmanager.bat" create avd -n pocket-test -k "system-images;android-34;google_apis;x86_64" --device "pixel_6" --abi google_apis/x86_64 --force < C:\Users\86133\AppData\Local\Temp\yes.txt
echo --- list AVDs ---
"%ANDROID_HOME%\cmdline-tools\latest\bin\avdmanager.bat" list avd
