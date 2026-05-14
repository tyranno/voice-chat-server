@echo off
REM Bridge TLS deployment to NanoPC-T4 (ARM64)
REM Builds ARM64, uploads, restarts, adds BRIDGE_TLS_ENABLED=true if missing
REM
REM Usage: deploy-bridge-tls.bat [ssh-host]
REM   default: nanopi (via Cloudflare Tunnel)

set HOST=%~1
if "%HOST%"=="" set HOST=nanopi

echo [1/4] Building ARM64 binary...
call build-linux.bat
if %ERRORLEVEL% NEQ 0 exit /b 1

echo [2/4] SCP upload to %HOST%...
scp voicechat-server-linux-arm64 %HOST%:/tmp/voicechat-server-new
if %ERRORLEVEL% NEQ 0 exit /b 1

echo [3/4] Deploy + restart...
ssh %HOST% "sudo systemctl stop voicechat; sudo cp /tmp/voicechat-server-new /opt/voicechat/voicechat-server; sudo chmod +x /opt/voicechat/voicechat-server; sudo chown voicechat:voicechat /opt/voicechat/voicechat-server; sudo systemctl start voicechat"

echo [4/4] Ensure BRIDGE_TLS_ENABLED=true in .env...
ssh %HOST% "sudo grep -q '^BRIDGE_TLS_ENABLED=true' /opt/voicechat/.env || (echo 'BRIDGE_TLS_ENABLED=true' | sudo tee -a /opt/voicechat/.env && sudo systemctl restart voicechat)"

echo DONE
ssh %HOST% "sudo systemctl is-active voicechat; curl -s http://localhost:8090/health"
