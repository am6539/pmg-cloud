#!/bin/bash
# PMG Cloud Quick Production Setup - FIXED VERSION
# For: Server behind NAT, no domain, fresh install
# Time: ~10-15 minutes

set -e

echo "╔═══════════════════════════════════════════════════════════════╗"
echo "║         PMG Cloud Quick Production Setup                     ║"
echo "║         Estimated time: 10-15 minutes                        ║"
echo "╚═══════════════════════════════════════════════════════════════╝"
echo ""

# Step 1: Check/Install Docker
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
