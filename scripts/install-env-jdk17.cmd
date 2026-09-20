@echo off
set JDK_HOME=C:\Program Files\AdoptOpenJDK\jdk-17.0.0.20-hotspot
setx JAVA_HOME "%JDK_HOME%"
setx PATH "%PATH%;%JDK_HOME%\bin"
echo JAVA_HOME=%JDK_HOME%
set PATH=%JAVA_HOME%\bin;%PATH%
where java
java -version
