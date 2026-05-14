@echo off
REM Build + Deploy to NanoPC-T4 (ARM64) — current production server
REM Usage: deploy.bat [ssh-host]
REM   ssh-host: SSH config alias or user@host (default: nanopi)
REM
REM Examples:
REM   deploy.bat             — uses 'nanopi' from ~/.ssh/config (Cloudflare Tunnel)
REM   deploy.bat nanopi-lan  — uses LAN IP 192.168.123.200
REM   deploy.bat user@host   — custom host

set HOST=%~1
if "%HOST%"=="" set HOST=nanopi

echo === Building ARM64 Linux binary ===
call build-linux.bat
if %ERRORLEVEL% NEQ 0 (
    echo Build failed
    exit /b 1
)

echo === Uploading to %HOST% ===
scp voicechat-server-linux-arm64 %HOST%:/tmp/voicechat-server-new
if %ERRORLEVEL% NEQ 0 (
    echo SCP failed — check SSH access: ssh %HOST% 'echo ok'
    exit /b 1
)

echo === Deploying on %HOST% ===
REM 기존 binary 소유자를 자동 검출해 그대로 유지 (서비스 user가 환경마다 다를 수 있음)
ssh %HOST% "OWNER=$(stat -c %%U:%%G /opt/voicechat/voicechat-server) && sudo systemctl stop voicechat && sudo cp /tmp/voicechat-server-new /opt/voicechat/voicechat-server && sudo chmod +x /opt/voicechat/voicechat-server && sudo chown $OWNER /opt/voicechat/voicechat-server && sudo systemctl start voicechat && sleep 2 && sudo systemctl is-active voicechat && curl -s http://localhost:8090/health"

echo.
echo === Done ===
echo Verify external: curl https://voicechat.tyranno.xyz/health
