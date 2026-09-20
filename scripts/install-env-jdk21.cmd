@echo off
set JDK21_HOME=C:\Program Files\Eclipse Adoptium\jdk-21.0.12.101-hotspot
set JAVA_HOME=%JDK21_HOME%
setx JAVA_HOME "%JDK21_HOME%"
setx PATH "%PATH%;%JDK21_HOME%\bin"
echo JAVA_HOME=%JDK21_HOME%
"%JDK21_HOME%\bin\java.exe" -version
