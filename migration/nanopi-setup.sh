#!/usr/bin/env bash
# NanoPi M4 — VoiceChat server self-hosting setup
# Run on the NanoPi via SSH:  bash nanopi-setup.sh
set -euo pipefail

# === Config ===
SERVICE_USER="voicechat"
INSTALL_DIR="/opt/voicechat"
DATA_DIR="/opt/voicechat/data"
APK_DIR="/opt/voicechat/data/apk"
BIN_NAME="voicechat-server"
PORT=8090

# === Detect arch ===
ARCH=$(uname -m)
echo "[i] arch=$ARCH (expected: aarch64)"
if [[ "$ARCH" != "aarch64" ]]; then
  echo "[!] WARNING: not ARM64. Stop and verify NanoPi model."
  exit 1
fi

# === System packages ===
echo "[1/6] Installing system packages..."
sudo apt update
sudo apt install -y \
  ca-certificates curl wget gnupg lsb-release \
  python3 python3-pip python3-venv \
  ffmpeg \
  ufw \
  jq

# === yt-dlp (latest) ===
echo "[2/6] Installing yt-dlp..."
sudo curl -L https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp_linux_aarch64 -o /usr/local/bin/yt-dlp
sudo chmod +x /usr/local/bin/yt-dlp
yt-dlp --version

# === cloudflared (Cloudflare Tunnel client) ===
echo "[3/6] Installing cloudflared..."
sudo wget -q https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-arm64 -O /usr/local/bin/cloudflared
sudo chmod +x /usr/local/bin/cloudflared
cloudflared --version

# === Service user ===
echo "[4/6] Creating service user + dirs..."
if ! id "$SERVICE_USER" &>/dev/null; then
  sudo useradd -r -m -d "$INSTALL_DIR" -s /usr/sbin/nologin "$SERVICE_USER"
fi
sudo mkdir -p "$INSTALL_DIR" "$DATA_DIR" "$APK_DIR" "$DATA_DIR/cache" "$DATA_DIR/conversations"
sudo chown -R "$SERVICE_USER:$SERVICE_USER" "$INSTALL_DIR"

# === Deploy server binary (expected at ./voicechat-server-linux-arm64 next to this script) ===
echo "[5/6] Deploying server binary..."
BIN_SRC="$(dirname "$0")/voicechat-server-linux-arm64"
if [[ ! -f "$BIN_SRC" ]]; then
  echo "[!] Missing $BIN_SRC — scp the ARM64 binary to this folder first"
  exit 1
fi
sudo cp "$BIN_SRC" "$INSTALL_DIR/$BIN_NAME"
sudo chmod +x "$INSTALL_DIR/$BIN_NAME"
sudo chown "$SERVICE_USER:$SERVICE_USER" "$INSTALL_DIR/$BIN_NAME"

# === systemd unit ===
echo "[6/6] Installing systemd unit..."
sudo tee /etc/systemd/system/voicechat.service > /dev/null <<EOF
[Unit]
Description=VoiceChat Server (self-hosted on NanoPi)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$SERVICE_USER
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/$BIN_NAME
EnvironmentFile=-$INSTALL_DIR/.env
Restart=on-failure
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable voicechat
echo
echo "=== Setup complete ==="
echo "Next steps:"
echo "  1. Copy your .env file to $INSTALL_DIR/.env  (chmod 600, owner=$SERVICE_USER)"
echo "  2. Copy APK + meta.json to $APK_DIR/"
echo "  3. Set up Cloudflare Tunnel (see migration/cloudflare-tunnel.md)"
echo "  4. sudo systemctl start voicechat"
echo "  5. journalctl -u voicechat -f"
