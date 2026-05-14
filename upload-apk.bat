@echo off
REM Upload APK to NanoPC-T4 and update version metadata
REM Usage: upload-apk.bat <version> [apk-path] [ssh-host]
REM
REM Examples:
REM   upload-apk.bat 0.10.27
REM   upload-apk.bat 0.10.27 C:\Project\88.MyProject\voice-chat\android\app\build\outputs\apk\debug\app-debug.apk
REM   upload-apk.bat 0.10.27 . nanopi-lan

set VERSION=%~1
set APK_PATH=%~2
set HOST=%~3
if "%HOST%"=="" set HOST=nanopi

if "%VERSION%"=="" (
    echo Usage: upload-apk.bat ^<version^> [apk-path] [ssh-host]
    echo.
    echo Default APK path:
    echo   C:\Project\88.MyProject\voice-chat\android\app\build\outputs\apk\debug\app-debug.apk
    echo Default host: nanopi
    exit /b 1
)

if "%APK_PATH%"=="" set APK_PATH=C:\Project\88.MyProject\voice-chat\android\app\build\outputs\apk\debug\app-debug.apk
if "%APK_PATH%"=="." set APK_PATH=C:\Project\88.MyProject\voice-chat\android\app\build\outputs\apk\debug\app-debug.apk

if not exist "%APK_PATH%" (
    echo APK not found: %APK_PATH%
    exit /b 1
)

REM Calculate APK size
for %%I in ("%APK_PATH%") do set APK_SIZE=%%~zI

echo === Uploading APK v%VERSION% (%APK_SIZE% bytes) to %HOST% ===
scp "%APK_PATH%" %HOST%:/tmp/app-debug.apk
if %ERRORLEVEL% NEQ 0 (
    echo SCP failed
    exit /b 1
)

echo === Creating version metadata ===
echo {"version":"%VERSION%","versionCode":1,"size":%APK_SIZE%,"updatedAt":"%date:~0,10%T%time:~0,2%:%time:~3,2%:%time:~6,2%Z"} > "%TEMP%\version.json"
echo {"version":"%VERSION%","versionCode":1,"size":%APK_SIZE%,"downloadUrl":"/api/apk/download"} > "%TEMP%\meta.json"
scp "%TEMP%\version.json" "%TEMP%\meta.json" %HOST%:/tmp/

echo === Installing on server ===
ssh %HOST% "sudo cp /tmp/app-debug.apk /opt/voicechat/data/apk/app-debug.apk && sudo cp /tmp/version.json /opt/voicechat/data/apk/version.json && sudo cp /tmp/meta.json /opt/voicechat/data/apk/meta.json && sudo chmod 644 /opt/voicechat/data/apk/* && cat /opt/voicechat/data/apk/meta.json"

echo === Done ===
echo Test: curl https://voicechat.tyranno.xyz/api/apk/latest
