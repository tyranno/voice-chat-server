@echo off
REM Build for NanoPC-T4 (ARM64) — current production server
REM Output: voicechat-server-linux-arm64
REM
REM For amd64 (legacy GCP / dev VM), use build-linux-amd64.bat
set GOOS=linux
set GOARCH=arm64
set CGO_ENABLED=0
C:\Users\lab\scoop\apps\go\current\bin\go.exe build -o voicechat-server-linux-arm64 .
echo BUILD EXIT: %ERRORLEVEL%
if exist voicechat-server-linux-arm64 (
    for %%I in (voicechat-server-linux-arm64) do echo Size: %%~zI bytes
)
