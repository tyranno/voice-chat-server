@echo off
REM Build + Deploy to Ubuntu VM via SCP
REM Usage: deploy.bat [user@host] [ssh-key]

set HOST=%~1
set KEY=%~2

if "%HOST%"=="" (
    echo Usage: deploy.bat user@host [ssh-key-path]
    exit /b 1
)

echo === Building Linux binary ===
call build-linux.bat
if %ERRORLEVEL% NEQ 0 exit /b 1

echo === Uploading to %HOST% ===
if "%KEY%"=="" (
    scp voicechat-server deploy\voicechat.service deploy\.env.example deploy\setup.sh %HOST%:/tmp/
) else (
    scp -i %KEY% voicechat-server deploy\voicechat.service deploy\.env.example deploy\setup.sh %HOST%:/tmp/
)

echo === Done ===
echo On the server run:
echo   cd /tmp ^&^& sudo bash setup.sh
echo   sudo nano /opt/voicechat/.env
echo   sudo systemctl start voicechat
