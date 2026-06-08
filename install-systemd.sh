#!/bin/bash
# PMG Cloud Systemd Installation Script

set -e

echo "=== PMG Cloud Systemd Service Installation ==="

# Check if running as root
if [ "$EUID" -ne 0 ]; then
    echo "ERROR: Script này cần chạy với quyền root"
    echo "Chạy: sudo ./install-systemd.sh"
    exit 1
fi

# Variables
INSTALL_DIR="/opt/pmg-cloud"
SERVICE_USER="pmg"
SERVICE_FILE="/etc/systemd/system/pmg-cloud.service"

# Create user if not exists
if ! id "$SERVICE_USER" &>/dev/null; then
    echo "Creating user $SERVICE_USER..."
    useradd -r -s /bin/false -d $INSTALL_DIR $SERVICE_USER
fi

# Create installation directory
echo "Creating installation directory..."
mkdir -p $INSTALL_DIR/{data,certs,logs}

# Build binary
echo "Building pmg-cloud binary..."
go build -o $INSTALL_DIR/pmg-cloud .

# Copy necessary files
echo "Copying files..."
cp -r certs/* $INSTALL_DIR/certs/ 2>/dev/null || echo "No certs found, skipping..."

# Set ownership
echo "Setting permissions..."
chown -R $SERVICE_USER:$SERVICE_USER $INSTALL_DIR
chmod +x $INSTALL_DIR/pmg-cloud

# Create environment file
ENV_FILE="/etc/pmg-cloud/env"
mkdir -p /etc/pmg-cloud

if [ ! -f "$ENV_FILE" ]; then
    echo "Creating environment file: $ENV_FILE"
    cat > $ENV_FILE <<EOF
# PMG Cloud Environment Variables
PMG_CLOUD_API_KEYS=changeme-$(openssl rand -hex 16)
PMG_CLOUD_DASH_USER=admin
PMG_CLOUD_DASH_PASS=changeme-$(openssl rand -hex 12)
EOF
    chmod 600 $ENV_FILE
    echo ""
    echo "WARNING: Default credentials created in $ENV_FILE"
    echo "PLEASE EDIT THIS FILE BEFORE STARTING THE SERVICE!"
    echo ""
fi

# Install systemd service
echo "Installing systemd service..."

# Update service file with environment file
cat > $SERVICE_FILE <<EOF
[Unit]
Description=PMG Cloud Server
After=network.target

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_USER
WorkingDirectory=$INSTALL_DIR

# Load environment from file
EnvironmentFile=$ENV_FILE

# Binary và arguments
ExecStart=$INSTALL_DIR/pmg-cloud \\
  --addr=:8443 \\
  --http-addr=:8080 \\
  --data-dir=$INSTALL_DIR/data \\
  --tls-cert=$INSTALL_DIR/certs/tls.crt \\
  --tls-key=$INSTALL_DIR/certs/tls.key \\
  --retention-days=30 \\
  --malware-refresh-interval=6h

# Logging
StandardOutput=append:$INSTALL_DIR/logs/pmg-cloud.log
StandardError=append:$INSTALL_DIR/logs/pmg-cloud-error.log

# Restart policy
Restart=always
RestartSec=10

# Security hardening
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=$INSTALL_DIR/data
ReadWritePaths=$INSTALL_DIR/logs

# Limits
LimitNOFILE=65536
LimitNPROC=4096

[Install]
WantedBy=multi-user.target
EOF

# Reload systemd
echo "Reloading systemd..."
systemctl daemon-reload

# Create logrotate config
echo "Creating logrotate config..."
cat > /etc/logrotate.d/pmg-cloud <<EOF
$INSTALL_DIR/logs/*.log {
    daily
    rotate 7
    compress
    delaycompress
    missingok
    notifempty
    create 0640 $SERVICE_USER $SERVICE_USER
    sharedscripts
    postrotate
        systemctl reload pmg-cloud.service > /dev/null 2>&1 || true
    endscript
}
EOF

echo ""
echo "=== Installation Complete ==="
echo ""
echo "Next steps:"
echo "1. Edit environment file:"
echo "   sudo nano $ENV_FILE"
echo ""
echo "2. Ensure TLS certificates exist:"
echo "   ls -la $INSTALL_DIR/certs/"
echo ""
echo "3. Start service:"
echo "   sudo systemctl start pmg-cloud"
echo ""
echo "4. Enable auto-start on boot:"
echo "   sudo systemctl enable pmg-cloud"
echo ""
echo "5. Check status:"
echo "   sudo systemctl status pmg-cloud"
echo ""
echo "6. View logs:"
echo "   sudo journalctl -u pmg-cloud -f"
echo "   tail -f $INSTALL_DIR/logs/pmg-cloud.log"
echo ""

# Create management commands
cat > /usr/local/bin/pmg-cloud-status <<'EOF'
#!/bin/bash
systemctl status pmg-cloud
EOF

cat > /usr/local/bin/pmg-cloud-logs <<'EOF'
#!/bin/bash
if [ "$1" == "error" ]; then
    tail -f /opt/pmg-cloud/logs/pmg-cloud-error.log
else
    tail -f /opt/pmg-cloud/logs/pmg-cloud.log
fi
EOF

chmod +x /usr/local/bin/pmg-cloud-status
chmod +x /usr/local/bin/pmg-cloud-logs

echo "Management commands installed:"
echo "  pmg-cloud-status  - Check service status"
echo "  pmg-cloud-logs    - View logs (pmg-cloud-logs error for errors)"
