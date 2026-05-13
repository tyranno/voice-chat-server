#!/usr/bin/env bash
# NanoPC-T4 재플래시 후 자동 복원 스크립트
# Ubuntu 24.04 Noble core (또는 Debian 13 Trixie core) 기준
#
# 사용법:
#   1. 새 OS 부팅 후 SSH 접속 (tyranno user)
#   2. PC에서 백업 전송:
#      scp -r C:/Users/lab/Downloads/gcp-backup/ tyranno@<IP>:~/
#   3. 이 스크립트 실행:
#      bash ~/voice-chat-server/migration/nanopc-t4-reflash/scripts/post-install.sh
#      (또는 단독 실행: bash post-install.sh)

set -euo pipefail

BACKUP_DIR="${HOME}/gcp-backup"
SERVICE_USER="voicechat"
PASSWORD="1234"  # NanoPi tyranno user password (sudo)

log() { echo "[$(date +%H:%M:%S)] $*"; }
err() { echo "[ERR] $*" >&2; exit 1; }

# ============================================================
log "[1/12] 사전 검증"
# ============================================================
[ -d "$BACKUP_DIR" ] || err "백업 디렉토리 없음: $BACKUP_DIR (PC에서 먼저 scp로 보내야 함)"
[ -f "$BACKUP_DIR/gcp-backup-2026-05-13.tar.gz" ] || err "GCP 백업 파일 없음"
[ -f "$BACKUP_DIR/nanopi-extra/cloudflared-config.tar.gz" ] || err "cloudflared 설정 백업 없음"
log "백업 확인 OK"

# ============================================================
log "[2/12] 시스템 업데이트 + 기본 패키지"
# ============================================================
sudo apt update -qq
sudo apt install -y -qq \
  ca-certificates curl wget unzip jq \
  git ffmpeg \
  python3-pip python3-venv \
  nodejs npm
log "기본 패키지 설치됨. node 버전: $(node --version)"

# ============================================================
log "[3/12] yt-dlp 정적 바이너리 + deno (JS 런타임)"
# ============================================================
sudo curl -L -o /usr/local/bin/yt-dlp \
  https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp_linux_aarch64
sudo chmod +x /usr/local/bin/yt-dlp
log "yt-dlp 버전: $(yt-dlp --version)"

cd /tmp
wget -q https://github.com/denoland/deno/releases/latest/download/deno-aarch64-unknown-linux-gnu.zip -O deno.zip
sudo unzip -o deno.zip -d /tmp/
sudo mv /tmp/deno /usr/local/bin/deno
sudo chmod +x /usr/local/bin/deno
rm deno.zip
log "deno 버전: $(deno --version | head -1)"

# ============================================================
log "[4/12] cloudflared 바이너리"
# ============================================================
sudo curl -L -o /usr/local/bin/cloudflared \
  https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-arm64
sudo chmod +x /usr/local/bin/cloudflared
log "cloudflared 버전: $(cloudflared --version)"

# ============================================================
log "[5/12] voicechat 서비스 user + 디렉토리 구조"
# ============================================================
if ! id "$SERVICE_USER" &>/dev/null; then
  sudo useradd -r -m -d /opt/voicechat -s /usr/sbin/nologin "$SERVICE_USER"
fi
sudo mkdir -p /opt/voicechat/data/{apk,cache,conversations,files}

# ============================================================
log "[6/12] GCP 백업 복원 (configs, scripts, openclaw, claude)"
# ============================================================
cd /
sudo tar xzf "$BACKUP_DIR/gcp-backup-2026-05-13.tar.gz"
log "  - openclaw, claude, lottery, scripts, oc_proxy, google_stt 복원됨"

# ============================================================
log "[7/12] NanoPi 데이터 복원 (voicechat data + 최신 openclaw + cloudflared)"
# ============================================================
sudo tar xzf "$BACKUP_DIR/nanopi-extra/nanopi-voicechat-data.tar.gz" -C /
sudo tar xzf "$BACKUP_DIR/nanopi-extra/nanopi-openclaw-latest.tar.gz" -C / --skip-old-files
sudo tar xzf "$BACKUP_DIR/nanopi-extra/cloudflared-config.tar.gz" -C /
log "  - /opt/voicechat/data, 최신 openclaw, ~/.cloudflared 복원됨"

# ============================================================
log "[8/12] 권한 정리"
# ============================================================
sudo chown -R tyranno:tyranno /home/tyranno
sudo chown -R "$SERVICE_USER:$SERVICE_USER" /opt/voicechat
sudo chmod 600 /opt/voicechat/.env /opt/voicechat/firebase-sa.json 2>/dev/null || true
sudo chmod 700 /home/tyranno/.cloudflared
sudo chmod 600 /home/tyranno/.cloudflared/*.json /home/tyranno/.cloudflared/cert.pem

# ============================================================
log "[9/12] voicechat-server 바이너리 + systemd 등록"
# ============================================================
if [ -f "$BACKUP_DIR/voicechat-server-linux-arm64" ]; then
  sudo cp "$BACKUP_DIR/voicechat-server-linux-arm64" /opt/voicechat/voicechat-server
else
  log "  WARN: voicechat-server 바이너리 없음 — PC에서 따로 scp로 옮겨야 함"
  log "       scp voice-chat-server/migration/voicechat-server-linux-arm64 nanopi:/opt/voicechat/"
fi
sudo chmod +x /opt/voicechat/voicechat-server 2>/dev/null || true
sudo chown "$SERVICE_USER:$SERVICE_USER" /opt/voicechat/voicechat-server 2>/dev/null || true

# voicechat.service는 백업의 /etc/systemd/system/ 안에 있음 (gcp-backup에서 풀림)
# 단, ProtectSystem=strict + PrivateTmp=true 적용된 NanoPi 버전 적용 필요
if [ -f /etc/systemd/system/voicechat.service ]; then
  # PrivateTmp 없으면 추가 (PyInstaller temp dir 문제 해결)
  grep -q "PrivateTmp" /etc/systemd/system/voicechat.service || \
    sudo sed -i "/ReadWritePaths=/a PrivateTmp=true" /etc/systemd/system/voicechat.service
fi
sudo systemctl daemon-reload
sudo systemctl enable voicechat || true

# ============================================================
log "[10/12] cloudflared systemd 서비스"
# ============================================================
# 백업에 oc-proxy/google-stt도 들어 있음 — 일단 voicechat과 cloudflared만 활성화
sudo cloudflared service install 2>/dev/null || true
sudo systemctl enable cloudflared
log "  - cloudflared 활성화됨 (Tunnel UUID 보존됨, DNS 변경 불필요)"

# ============================================================
log "[11/12] crontab 복원"
# ============================================================
if [ -f "$BACKUP_DIR/crontab-tyranno.txt" ]; then
  crontab "$BACKUP_DIR/crontab-tyranno.txt"
  log "  - crontab 4개 작업 복원 (로또/연금/아침브리핑/관심종목)"
fi

# ============================================================
log "[12/12] openclaw 설치 (Node 22 네이티브!)"
# ============================================================
if command -v node &>/dev/null && [ "$(node --version | grep -oE '^v[0-9]+' | grep -oE '[0-9]+')" -ge 22 ]; then
  log "  - Node 22+ 확인. openclaw 설치 시도"
  sudo npm install -g openclaw 2>&1 | tail -5
  log "  - openclaw 버전: $(openclaw --version 2>&1 | head -1)"

  # openclaw 시스템 서비스 (백업에 openclaw-gateway.service 있음)
  if [ -f /etc/systemd/system/openclaw-gateway.service ]; then
    sudo systemctl enable openclaw-gateway || true
  fi
else
  log "  WARN: Node 22+ 미설치 — apt 저장소에 Node 22 없으면 NodeSource로 추가 설치 필요:"
  log "       curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash -"
  log "       sudo apt install -y nodejs"
fi

# ============================================================
log "=== 설치 완료 ==="
log ""
log "서비스 시작:"
log "  sudo systemctl start voicechat cloudflared"
log "  sudo systemctl start openclaw-gateway   # 있으면"
log ""
log "검증:"
log "  curl http://localhost:8090/health"
log "  curl https://voicechat.tyranno.xyz/health   # 외부에서"
log ""
log "로그 확인:"
log "  journalctl -u voicechat -f"
log "  journalctl -u cloudflared -f"
