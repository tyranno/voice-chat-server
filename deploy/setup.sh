#!/bin/bash
# Voice Chat Server - Ubuntu Setup Script
# Usage: sudo bash setup.sh

set -e

APP_DIR="/opt/voicechat"
APP_USER="voicechat"

echo "=== Voice Chat Server Setup ==="

# Create system user
if ! id "$APP_USER" &>/dev/null; then
    useradd --system --no-create-home --shell /usr/sbin/nologin "$APP_USER"
    echo "Created user: $APP_USER"
fi

# Create app directory
mkdir -p "$APP_DIR"

# Copy binary
cp voicechat-server "$APP_DIR/"
chmod +x "$APP_DIR/voicechat-server"

# Create .env if not exists
if [ ! -f "$APP_DIR/.env" ]; then
    cp .env.example "$APP_DIR/.env"
    chmod 600 "$APP_DIR/.env"
    echo "Created $APP_DIR/.env — edit tokens before starting!"
fi

chown -R "$APP_USER:$APP_USER" "$APP_DIR"

# Install systemd service
cp voicechat.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable voicechat

echo ""
echo "=== Setup Complete ==="
echo "1. Edit tokens:  sudo nano $APP_DIR/.env"
echo "2. Start:        sudo systemctl start voicechat"
echo "3. Check:        sudo systemctl status voicechat"
echo "4. Logs:         sudo journalctl -u voicechat -f"
