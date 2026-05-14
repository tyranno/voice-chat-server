@echo off
REM Legacy: build for amd64 Linux (GCP, generic VPS)
REM 현재 운영은 NanoPC-T4 (ARM64) — build-linux.bat 사용 권장
set GOOS=linux
set GOARCH=amd64
set CGO_ENABLED=0
C:\Users\lab\scoop\apps\go\current\bin\go.exe build -o voicechat-server-linux-amd64 .
echo BUILD EXIT: %ERRORLEVEL%
