@echo off
echo Building voice-chat-server for Linux (amd64)...
set GOOS=linux
set GOARCH=amd64
set CGO_ENABLED=0
"C:\Users\lab\scoop\apps\go\current\bin\go.exe" build -o voicechat-server -ldflags="-s -w" .
if %ERRORLEVEL% EQU 0 (
    echo Build success: voicechat-server
) else (
    echo Build failed!
)
