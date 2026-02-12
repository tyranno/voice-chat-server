@echo off
echo Building voice-chat-server for Linux (amd64)...
set GOOS=linux
set GOARCH=amd64
set CGO_ENABLED=0
go build -o voicechat-server -ldflags="-s -w" .
if %ERRORLEVEL% EQU 0 (
    echo Build success: voicechat-server
) else (
    echo Build failed!
)
