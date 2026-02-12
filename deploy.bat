@echo off
REM Build + Deploy to Ubuntu VM via SCP
REM Usage: deploy.bat [user@host] [ssh-key] [domain]

set HOST=%~1
set KEY=%~2
set DOMAIN=%~3

if "%HOST%"=="" (
    echo Usage: deploy.bat user@host [ssh-key-path] [domain]
    echo Example: deploy.bat tyranno@34.64.164.13 C:\Users\tyranno\.ssh\voicechat-key voicechat.tyranno.xyz
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
if "%DOMAIN%"=="" (
    echo   cd /tmp ^&^& sudo bash setup.sh
) else (
    echo   cd /tmp ^&^& sudo bash setup.sh %DOMAIN%
)
echo   sudo nano /opt/voicechat/.env
echo   sudo systemctl start voicechat
