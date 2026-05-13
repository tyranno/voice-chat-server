#!/usr/bin/env bash
# NanoPC-T4 재플래시 후 SSH Tunnel 라우팅 복구
#
# 사용법 (집에서 LAN으로 NanoPi 접속한 상태):
#   1. ssh tyranno@192.168.123.200  (비번: tyranno1q2w3e4r)
#   2. wget -O /tmp/fix.sh https://raw.githubusercontent.com/tyranno/voice-chat-server/master/migration/nanopc-t4-reflash/scripts/fix-ssh-tunnel.sh
#      OR git clone + 스크립트 실행
#      OR 이 파일 내용 직접 복붙
#   3. bash /tmp/fix.sh
#
# 또는 빠른 한 줄 (집에서 NanoPi SSH 세션에서):
#   curl -sSL https://raw.githubusercontent.com/tyranno/voice-chat-server/master/migration/nanopc-t4-reflash/scripts/fix-ssh-tunnel.sh | bash

set -euo pipefail

CONFIG_PATH="/etc/cloudflared/config.yml"
SSH_HOSTNAME="ssh.tyranno.xyz"
TUNNEL_NAME="voicechat"

echo "=== [1/6] 현재 cloudflared config 확인 ==="
sudo cat "$CONFIG_PATH"
echo

# Check if SSH route already exists
if sudo grep -q "$SSH_HOSTNAME" "$CONFIG_PATH"; then
  echo "✅ $SSH_HOSTNAME 라우팅이 이미 config에 있음. 단계 [3]은 skip"
  ADDED=0
else
  echo "=== [2/6] config.yml에 ssh.tyranno.xyz 라우팅 추가 ==="
  # Backup
  sudo cp "$CONFIG_PATH" "${CONFIG_PATH}.bak-$(date +%Y%m%d-%H%M%S)"

  # Insert ssh route BEFORE the http_status:404 fallback line
  sudo sed -i "/service: http_status:404/i\\  - hostname: $SSH_HOSTNAME\\n    service: ssh:\\/\\/localhost:22" "$CONFIG_PATH"

  echo "--- 수정 후 config ---"
  sudo cat "$CONFIG_PATH"
  ADDED=1
fi

echo
echo "=== [3/6] Cloudflare DNS에 ssh.tyranno.xyz CNAME 등록 ==="
# tyranno 사용자의 cloudflared 인증서를 써야 함
sudo -u tyranno cloudflared tunnel route dns "$TUNNEL_NAME" "$SSH_HOSTNAME" 2>&1 || \
  echo "(이미 등록되어 있으면 무시 OK)"

echo
echo "=== [4/6] cloudflared 재시작 ==="
sudo systemctl restart cloudflared
sleep 3
sudo systemctl is-active cloudflared

echo
echo "=== [5/6] 로그 확인 (ssh 라우팅 인식?) ==="
sudo journalctl -u cloudflared --since "30 sec ago" --no-pager | tail -15

echo
echo "=== [6/6] 회사 PC에서 다음 명령 시도 ==="
echo "  ssh nanopi                     # 비번 묻는지 확인 (tyranno1q2w3e4r)"
echo "  ssh-copy-id nanopi              # 공개키 등록 (한 번만)"
echo "  ssh nanopi 'whoami; hostname'   # 키 인증 검증"
echo
echo "⚠️  fail2ban: SSH 3회 실패 시 24h 차단. 비번 한 번에 정확히!"
echo "    192.168.123.x 로컬망은 fail2ban 예외 (집에서 시도는 안전)"
echo
echo "=== 완료 ==="
