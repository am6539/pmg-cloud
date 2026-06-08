#!/bin/bash
# PMG Cloud Quick Production Setup
# For: Server behind NAT, no domain, fresh install
# Time: ~10-15 minutes

set -e

echo "╔═══════════════════════════════════════════════════════════════╗"
echo "║         PMG Cloud Quick Production Setup                     ║"
echo "║         Estimated time: 10-15 minutes                        ║"
echo "╚═══════════════════════════════════════════════════════════════╝"
echo ""

# Step 1: Install Docker
echo "📦 [1/5] Installing Docker and Docker Compose..."
if ! command -v docker &> /dev/null; then
    echo "  → Installing Docker..."
    curl -fsSL https://get.docker.com -o get-docker.sh
    sudo sh get-docker.sh
    sudo usermod -aG docker $USER
    rm get-docker.sh
    echo "  ✓ Docker installed"
else
    echo "  ✓ Docker already installed"
fi

if ! command -v docker compose &> /dev/null; then
    echo "  → Docker Compose not found, but 'docker compose' should work"
fi

# Step 2: Clone repository
echo ""
echo "📥 [2/5] Cloning PMG Cloud repository..."
if [ -d "pmg-cloud" ]; then
    echo "  → Directory exists, pulling latest..."
    cd pmg-cloud
    git pull
else
    git clone https://github.com/am6539/pmg-cloud.git
    cd pmg-cloud
fi
echo "  ✓ Repository ready"

# Step 3: Setup environment
echo ""
echo "⚙️  [3/5] Configuring environment..."

# Generate strong credentials
API_KEY=$(openssl rand -hex 32)
DASH_PASS=$(openssl rand -base64 16)

cat > .env.local <<EOF
# PMG Cloud Production Environment
# Generated: $(date)

# API Keys
PMG_CLOUD_API_KEYS=$API_KEY

# Dashboard Admin Account
PMG_CLOUD_DASH_USER=admin
PMG_CLOUD_DASH_PASS=$DASH_PASS

# Optional Settings
PMG_CLOUD_RETENTION_DAYS=30
PMG_CLOUD_MALWARE_REFRESH_INTERVAL=6h
EOF

# Save credentials
cat > credentials.txt <<EOF
PMG Cloud Credentials
Generated: $(date)

Dashboard Login:
  URL: http://YOUR_SERVER_IP:8080
  Username: admin
  Password: $DASH_PASS

API Key (for agents):
  $API_KEY

IMPORTANT: Save this file securely and delete it from the server after copying!
EOF

echo "  ✓ Credentials generated and saved to credentials.txt"
echo ""
echo "  ⚠️  IMPORTANT: Your credentials:"
echo "  Dashboard: admin / $DASH_PASS"
echo "  API Key: $API_KEY"
echo ""
read -p "Press Enter after you've saved these credentials..."

# Step 4: Create directories
echo ""
echo "📁 [4/5] Creating data directories..."
mkdir -p data certs backups
echo "  ✓ Directories created"

# Step 5: Start services (insecure mode for internal network)
echo ""
echo "🚀 [5/5] Starting PMG Cloud..."
echo "  Note: Starting in --insecure mode (no TLS) for internal network"

# Create docker-compose.override.yml for insecure mode
cat > docker-compose.override.yml <<EOF
services:
  pmg-cloud:
    command: ["--addr=:8443", "--data-dir=/data", "--http-addr=:8080", "--insecure"]
    environment:
      - PMG_CLOUD_API_KEYS=$API_KEY
      - PMG_CLOUD_DASH_USER=admin
      - PMG_CLOUD_DASH_PASS=$DASH_PASS
EOF

# Start services
docker compose up -d

echo "  ✓ PMG Cloud started"

# Step 6: Verify
echo ""
echo "✅ [Verification] Checking service health..."
sleep 5

if docker compose ps | grep -q "running"; then
    echo "  ✓ Service is running"
else
    echo "  ✗ Service failed to start. Check logs:"
    echo "    docker compose logs"
    exit 1
fi

# Wait for health endpoint
echo "  → Waiting for service to be ready..."
for i in {1..30}; do
    if curl -s http://localhost:8080/healthz > /dev/null 2>&1; then
        echo "  ✓ Service is healthy"
        break
    fi
    sleep 2
    if [ $i -eq 30 ]; then
        echo "  ✗ Service did not become healthy. Check logs:"
        echo "    docker compose logs"
        exit 1
    fi
done

# Get server IP
SERVER_IP=$(hostname -I | awk '{print $1}')

echo ""
echo "╔═══════════════════════════════════════════════════════════════╗"
echo "║                   🎉 SETUP COMPLETE! 🎉                      ║"
echo "╚═══════════════════════════════════════════════════════════════╝"
echo ""
echo "✅ PMG Cloud is running!"
echo ""
echo "📊 Access Information:"
echo "  Dashboard: http://$SERVER_IP:8080"
echo "  gRPC Port: $SERVER_IP:8443"
echo ""
echo "🔑 Login Credentials:"
echo "  Username: admin"
echo "  Password: $DASH_PASS"
echo ""
echo "📝 Important Files:"
echo "  Credentials: $(pwd)/credentials.txt"
echo "  Environment: $(pwd)/.env.local"
echo ""
echo "⚠️  Security Notes:"
echo "  - Running in INSECURE mode (no TLS)"
echo "  - Only use on trusted internal network"
echo "  - Credentials saved in credentials.txt - SAVE IT NOW!"
echo ""
echo "🔧 Useful Commands:"
echo "  View logs:    docker compose logs -f"
echo "  Stop service: docker compose down"
echo "  Restart:      docker compose restart"
echo "  Status:       docker compose ps"
echo ""
echo "📖 Next Steps:"
echo "  1. Save credentials.txt to safe location"
echo "  2. Delete credentials.txt from server"
echo "  3. Access dashboard at http://$SERVER_IP:8080"
echo "  4. Deploy agents using the dashboard wizard"
echo ""
echo "💡 For production with TLS/domain, see DEPLOYMENT.md"
echo ""
