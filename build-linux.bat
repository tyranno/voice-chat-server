@echo off
set GOOS=linux
set GOARCH=amd64
C:\Users\lab\scoop\apps\go\current\bin\go.exe build -o voicechat-server-linux .
echo BUILD EXIT: %ERRORLEVEL%
