#!/bin/bash
# Voice Chat Server - Ubuntu Setup Script
# Usage: sudo bash setup.sh [domain]
# Example: sudo bash setup.sh voicechat.tyranno.xyz

set -e

APP_DIR="/opt/voicechat"
APP_USER="voicechat"
DOMAIN="${1:-}"

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
    echo "Created $APP_DIR/.env"
fi

# Install certbot if not present
if ! command -v certbot &>/dev/null; then
    echo "Installing certbot..."
    apt-get update
    apt-get install -y certbot
fi

# Request SSL certificate if domain provided
if [ -n "$DOMAIN" ]; then
    CERT_DIR="/etc/letsencrypt/live/$DOMAIN"
    
    if [ ! -d "$CERT_DIR" ]; then
        echo "Requesting SSL certificate for $DOMAIN..."
        echo "Make sure port 80 is temporarily open for verification!"
        certbot certonly --standalone -d "$DOMAIN" --non-interactive --agree-tos -m admin@$DOMAIN || {
            echo "Certbot failed. Try manual DNS challenge:"
            echo "  sudo certbot certonly --manual --preferred-challenges dns -d $DOMAIN"
        }
    fi
    
    if [ -d "$CERT_DIR" ]; then
        # Update .env with TLS settings
        sed -i "s|^PORT=.*|PORT=443|" "$APP_DIR/.env"
        sed -i "s|^TLS_ENABLED=.*|TLS_ENABLED=true|" "$APP_DIR/.env"
        sed -i "s|^TLS_CERT=.*|TLS_CERT=$CERT_DIR/fullchain.pem|" "$APP_DIR/.env"
        sed -i "s|^TLS_KEY=.*|TLS_KEY=$CERT_DIR/privkey.pem|" "$APP_DIR/.env"
        
        # Grant voicechat user access to certs
        usermod -aG ssl-cert "$APP_USER" 2>/dev/null || true
        chmod 750 /etc/letsencrypt/live /etc/letsencrypt/archive
        chgrp -R ssl-cert /etc/letsencrypt/live /etc/letsencrypt/archive
        
        echo "TLS configured for $DOMAIN"
    fi
fi

chown -R "$APP_USER:$APP_USER" "$APP_DIR"

# Install systemd service
cp voicechat.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable voicechat

# Setup certbot auto-renewal hook to restart service
if [ -n "$DOMAIN" ]; then
    HOOK_DIR="/etc/letsencrypt/renewal-hooks/deploy"
    mkdir -p "$HOOK_DIR"
    cat > "$HOOK_DIR/voicechat-restart.sh" << 'HOOK'
#!/bin/bash
systemctl restart voicechat
HOOK
    chmod +x "$HOOK_DIR/voicechat-restart.sh"
fi

echo ""
echo "=== Setup Complete ==="
echo "1. Edit tokens:  sudo nano $APP_DIR/.env"
echo "2. Start:        sudo systemctl start voicechat"
echo "3. Check:        sudo systemctl status voicechat"
echo "4. Logs:         sudo journalctl -u voicechat -f"
if [ -n "$DOMAIN" ]; then
    echo ""
    echo "TLS: Certificates will auto-renew via certbot timer"
fi
