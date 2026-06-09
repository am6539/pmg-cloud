#!/bin/bash
# PMG Cloud Quick Production Setup
# Simple version without complex heredocs

set -e

echo "╔═══════════════════════════════════════════════════════════════╗"
echo "║         PMG Cloud Quick Production Setup                     ║"
echo "║         Estimated time: 10-15 minutes                        ║"
echo "╚═══════════════════════════════════════════════════════════════╝"
echo ""

# Step 1: Check Docker
echo "📦 [1/5] Checking Docker installation..."
if ! command -v docker &> /dev/null; then
    echo "  → Installing Docker..."
    curl -fsSL https://get.docker.com -o get-docker.sh
    sudo sh get-docker.sh
    sudo usermod -aG docker $USER
    rm get-docker.sh
    echo "  ✓ Docker installed"
else
    echo "  ✓ Docker already installed: $(docker --version)"
fi

# Detect docker compose command
if docker compose version &> /dev/null; then
    DOCKER_COMPOSE="docker compose"
    echo "  ✓ Using: docker compose (v2)"
elif command -v docker-compose &> /dev/null; then
    DOCKER_COMPOSE="docker-compose"
    echo "  ✓ Using: docker-compose (v1)"
else
    echo "  ✗ Docker Compose not found. Installing..."
    sudo apt-get update
    sudo apt-get install -y docker-compose-plugin
    DOCKER_COMPOSE="docker compose"
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

# Generate credentials
API_KEY=$(openssl rand -hex 32)
DASH_PASS=$(openssl rand -base64 16)

# Create .env.local
echo "# PMG Cloud Production Environment" > .env.local
echo "# Generated: $(date)" >> .env.local
echo "" >> .env.local
echo "# API Keys" >> .env.local
echo "PMG_CLOUD_API_KEYS=$API_KEY" >> .env.local
echo "" >> .env.local
echo "# Dashboard Admin Account" >> .env.local
echo "PMG_CLOUD_DASH_USER=admin" >> .env.local
echo "PMG_CLOUD_DASH_PASS=$DASH_PASS" >> .env.local
echo "" >> .env.local
echo "# Optional Settings" >> .env.local
echo "PMG_CLOUD_RETENTION_DAYS=30" >> .env.local
echo "PMG_CLOUD_MALWARE_REFRESH_INTERVAL=6h" >> .env.local

# Save credentials
echo "PMG Cloud Credentials" > credentials.txt
echo "Generated: $(date)" >> credentials.txt
echo "" >> credentials.txt
echo "Dashboard Login:" >> credentials.txt
echo "  URL: http://YOUR_SERVER_IP:8080" >> credentials.txt
echo "  Username: admin" >> credentials.txt
echo "  Password: $DASH_PASS" >> credentials.txt
echo "" >> credentials.txt
echo "API Key (for agents):" >> credentials.txt
echo "  $API_KEY" >> credentials.txt
echo "" >> credentials.txt
echo "IMPORTANT: Save this file securely!" >> credentials.txt

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

# Step 5: Start services
echo ""
echo "🚀 [5/5] Starting PMG Cloud..."
echo "  Note: Starting in --insecure mode (no TLS) for internal network"

# Create docker-compose.override.yml
echo "services:" > docker-compose.override.yml
echo "  pmg-cloud:" >> docker-compose.override.yml
echo "    command: [\"--addr=:8443\", \"--data-dir=/data\", \"--http-addr=:8080\", \"--insecure\"]" >> docker-compose.override.yml
echo "    environment:" >> docker-compose.override.yml
echo "      - PMG_CLOUD_API_KEYS=$API_KEY" >> docker-compose.override.yml
echo "      - PMG_CLOUD_DASH_USER=admin" >> docker-compose.override.yml
echo "      - PMG_CLOUD_DASH_PASS=$DASH_PASS" >> docker-compose.override.yml

# Build and start
echo "  → Building and starting containers..."
$DOCKER_COMPOSE build 2>&1 | grep -v "^#" || true
$DOCKER_COMPOSE up -d

sleep 3
echo "  ✓ PMG Cloud started"

# Step 6: Verify
echo ""
echo "✅ [Verification] Checking service health..."
sleep 5

# Check container
if docker ps | grep -q pmg-cloud; then
    echo "  ✓ Service is running"
else
    echo "  ✗ Service failed to start"
    echo ""
    echo "  Checking logs..."
    $DOCKER_COMPOSE logs --tail 50
    exit 1
fi

# Wait for health
echo "  → Waiting for service to be ready..."
for i in {1..30}; do
    if curl -s http://localhost:8080/healthz > /dev/null 2>&1; then
        echo "  ✓ Service is healthy"
        break
    fi
    if [ $i -lt 30 ]; then
        echo "    Waiting... ($i/30)"
        sleep 2
    else
        echo "  ✗ Service did not become healthy"
        echo ""
        echo "  Logs:"
        $DOCKER_COMPOSE logs --tail 50
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
echo "  - Save credentials.txt to a safe location"
echo "  - Delete credentials.txt from server after saving"
echo ""
echo "🔧 Useful Commands:"
echo "  View logs:    $DOCKER_COMPOSE logs -f"
echo "  Stop service: $DOCKER_COMPOSE down"
echo "  Restart:      $DOCKER_COMPOSE restart"
echo "  Status:       $DOCKER_COMPOSE ps"
echo ""
echo "📖 Next Steps:"
echo "  1. Access dashboard at http://$SERVER_IP:8080"
echo "  2. Deploy agents using the dashboard wizard"
echo ""
echo "💡 For production with TLS, see DEPLOYMENT.md"
echo ""
