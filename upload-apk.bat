@echo off
REM Upload APK to server and update version
REM Usage: upload-apk.bat <version> [apk-path]

set VERSION=%~1
set APK_PATH=%~2
set KEY=C:\Users\tyranno\.ssh\voicechat-key
set HOST=tyranno@34.64.164.13

if "%VERSION%"=="" (
    echo Usage: upload-apk.bat 0.3.2 [path-to-apk]
    echo Default APK: E:\Project\My\voice-chat\android\app\build\outputs\apk\debug\app-debug.apk
    exit /b 1
)

if "%APK_PATH%"=="" set APK_PATH=E:\Project\My\voice-chat\android\app\build\outputs\apk\debug\app-debug.apk

echo === Uploading APK v%VERSION% ===
scp -i %KEY% "%APK_PATH%" %HOST%:/tmp/app-debug.apk

echo === Creating version.json ===
echo {"version":"%VERSION%","versionCode":1,"updatedAt":"%date:~0,10%"} > %TEMP%\version.json
scp -i %KEY% %TEMP%\version.json %HOST%:/tmp/version.json

echo === Installing on server ===
ssh -i %KEY% %HOST% "sudo cp /tmp/app-debug.apk /opt/voicechat/data/apk/app-debug.apk && sudo cp /tmp/version.json /opt/voicechat/data/apk/version.json && cat /opt/voicechat/data/apk/version.json"

echo === Done! ===
echo Test: curl https://voicechat.tyranno.xyz/api/apk/latest
