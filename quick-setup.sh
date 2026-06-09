#!/bin/bash
# PMG Cloud Production Setup - Robust Version
# Works on: Ubuntu 20.04+, Debian 10+, CentOS 8+, RHEL 8+, Fedora
# Handles: Docker v1/v2, old/new docker-compose, all edge cases

set -e

VERSION="2.0"
SCRIPT_NAME="PMG Cloud Setup"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log_info() { echo -e "${BLUE}[INFO]${NC} $1"; }
log_success() { echo -e "${GREEN}[✓]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[!]${NC} $1"; }
log_error() { echo -e "${RED}[✗]${NC} $1"; }

echo "╔═══════════════════════════════════════════════════════════════╗"
echo "║         PMG Cloud Production Setup v${VERSION}                   ║"
echo "║         Robust installation for all Linux distributions      ║"
echo "╚═══════════════════════════════════════════════════════════════╝"
echo ""

# =============================================================================
# STEP 1: System Check
# =============================================================================
log_info "[1/6] Checking system requirements..."

# Check OS
if [ -f /etc/os-release ]; then
    . /etc/os-release
    OS_NAME="$NAME"
    OS_VERSION="$VERSION_ID"
    log_success "OS: $OS_NAME $OS_VERSION"
else
    log_warn "Cannot detect OS version, continuing anyway..."
fi

# Check if root or sudo available
if [ "$EUID" -ne 0 ]; then
    if ! command -v sudo &> /dev/null; then
        log_error "This script needs root or sudo access"
        exit 1
    fi
    SUDO="sudo"
else
    SUDO=""
fi

# =============================================================================
# STEP 2: Install Docker
# =============================================================================
log_info "[2/6] Installing Docker..."

if command -v docker &> /dev/null; then
    DOCKER_VERSION=$(docker version --format '{{.Server.Version}}' 2>/dev/null || echo "unknown")
    log_success "Docker already installed: $DOCKER_VERSION"
else
    log_info "Installing Docker via official script..."
    curl -fsSL https://get.docker.com -o /tmp/get-docker.sh
    $SUDO sh /tmp/get-docker.sh
    rm /tmp/get-docker.sh

    # Add current user to docker group
    if [ "$EUID" -ne 0 ]; then
        $SUDO usermod -aG docker $USER
        log_warn "Added $USER to docker group. You may need to logout/login for this to take effect."
        log_warn "If you get permission errors, run: newgrp docker"
    fi

    log_success "Docker installed: $(docker version --format '{{.Server.Version}}')"
fi

# Start Docker service
if command -v systemctl &> /dev/null; then
    $SUDO systemctl enable docker 2>/dev/null || true
    $SUDO systemctl start docker 2>/dev/null || true
fi

# =============================================================================
# STEP 3: Install Docker Compose v2 (Critical!)
# =============================================================================
log_info "[3/6] Installing Docker Compose v2..."

# Remove old docker-compose v1 if exists
if command -v docker-compose &> /dev/null; then
    V1_VERSION=$(docker-compose version --short 2>/dev/null || echo "unknown")
    log_warn "Found old docker-compose v1 ($V1_VERSION), will prioritize v2"
fi

# Check if docker compose v2 is available
if docker compose version &> /dev/null 2>&1; then
    V2_VERSION=$(docker compose version --short 2>/dev/null || echo "unknown")
    log_success "Docker Compose v2 already installed: $V2_VERSION"
else
    log_info "Installing Docker Compose v2 plugin..."

    # Method 1: Package manager (preferred)
    if command -v apt-get &> /dev/null; then
        log_info "Using apt-get to install docker-compose-plugin..."
        $SUDO apt-get update -qq
        $SUDO apt-get install -y docker-compose-plugin || {
            log_warn "apt-get install failed, trying alternative method..."
        }
    elif command -v dnf &> /dev/null; then
        log_info "Using dnf to install docker-compose-plugin..."
        $SUDO dnf install -y docker-compose-plugin || true
    elif command -v yum &> /dev/null; then
        log_info "Using yum to install docker-compose-plugin..."
        $SUDO yum install -y docker-compose-plugin || true
    fi

    # Method 2: Direct binary install (fallback)
    if ! docker compose version &> /dev/null 2>&1; then
        log_info "Package install failed or unavailable, installing binary directly..."

        # Create plugin directory
        DOCKER_CONFIG=${DOCKER_CONFIG:-$HOME/.docker}
        mkdir -p $DOCKER_CONFIG/cli-plugins

        # Download latest compose
        COMPOSE_VERSION=$(curl -s https://api.github.com/repos/docker/compose/releases/latest | grep '"tag_name":' | sed -E 's/.*"v([^"]+)".*/\1/')
        if [ -z "$COMPOSE_VERSION" ]; then
            COMPOSE_VERSION="2.24.5"
            log_warn "Could not detect latest version, using $COMPOSE_VERSION"
        fi

        COMPOSE_URL="https://github.com/docker/compose/releases/download/v${COMPOSE_VERSION}/docker-compose-$(uname -s)-$(uname -m)"
        log_info "Downloading Docker Compose v${COMPOSE_VERSION}..."

        curl -fsSL "$COMPOSE_URL" -o $DOCKER_CONFIG/cli-plugins/docker-compose
        chmod +x $DOCKER_CONFIG/cli-plugins/docker-compose

        # Also install system-wide for sudo access
        if [ "$EUID" -ne 0 ]; then
            $SUDO mkdir -p /usr/local/lib/docker/cli-plugins
            $SUDO curl -fsSL "$COMPOSE_URL" -o /usr/local/lib/docker/cli-plugins/docker-compose
            $SUDO chmod +x /usr/local/lib/docker/cli-plugins/docker-compose
        fi
    fi

    # Final verification
    if docker compose version &> /dev/null 2>&1; then
        V2_VERSION=$(docker compose version --short 2>/dev/null || echo "unknown")
        log_success "Docker Compose v2 installed: $V2_VERSION"
    else
        log_error "Failed to install Docker Compose v2"
        log_error "Please install manually: https://docs.docker.com/compose/install/"
        exit 1
    fi
fi

DOCKER_COMPOSE="docker compose"

# =============================================================================
# STEP 4: Clone Repository
# =============================================================================
log_info "[4/6] Setting up PMG Cloud repository..."

if [ -d "pmg-cloud" ]; then
    log_info "Repository exists, updating..."
    cd pmg-cloud
    git pull --quiet || {
        log_warn "Git pull failed, repository might have local changes"
        cd ..
        mv pmg-cloud pmg-cloud.backup.$(date +%s)
        git clone https://github.com/am6539/pmg-cloud.git
        cd pmg-cloud
    }
else
    git clone https://github.com/am6539/pmg-cloud.git
    cd pmg-cloud
fi

log_success "Repository ready: $(pwd)"

# =============================================================================
# STEP 5: Configure Environment
# =============================================================================
log_info "[5/6] Generating configuration..."

# Generate secure credentials
API_KEY=$(openssl rand -hex 32)
DASH_PASS=$(openssl rand -base64 16)

# Create .env.local
cat > .env.local << EOF
# PMG Cloud Production Environment
# Generated: $(date -u +"%Y-%m-%d %H:%M:%S UTC")

# API Keys
PMG_CLOUD_API_KEYS=$API_KEY

# Dashboard Admin Account
PMG_CLOUD_DASH_USER=admin
PMG_CLOUD_DASH_PASS=$DASH_PASS

# Optional Settings
PMG_CLOUD_RETENTION_DAYS=30
PMG_CLOUD_MALWARE_REFRESH_INTERVAL=6h
EOF

# Create credentials file
cat > credentials.txt << EOF
╔═══════════════════════════════════════════════════════════════╗
║                    PMG CLOUD CREDENTIALS                      ║
╚═══════════════════════════════════════════════════════════════╝

Generated: $(date -u +"%Y-%m-%d %H:%M:%S UTC")

Dashboard Login:
  URL: http://YOUR_SERVER_IP:8080
  Username: admin
  Password: $DASH_PASS

API Key (for agents):
  $API_KEY

IMPORTANT:
  1. Save these credentials to a secure location NOW
  2. Delete this file after saving: rm credentials.txt
  3. Change the password after first login

Security Notes:
  - This setup runs in INSECURE mode (no TLS)
  - Only use on trusted internal networks
  - See DEPLOYMENT.md for TLS/SSL setup

EOF

# Create directories
mkdir -p data certs backups

log_success "Configuration generated"
echo ""
echo "╔═══════════════════════════════════════════════════════════════╗"
echo "║                   ⚠️  SAVE THESE CREDENTIALS ⚠️               ║"
echo "╚═══════════════════════════════════════════════════════════════╝"
echo ""
echo "  Dashboard: admin / $DASH_PASS"
echo "  API Key:   $API_KEY"
echo ""
echo "  📝 Also saved in: $(pwd)/credentials.txt"
echo ""
read -p "Press Enter after you've saved these credentials..."

# =============================================================================
# STEP 6: Deploy Services
# =============================================================================
log_info "[6/6] Deploying PMG Cloud..."

# Create docker-compose.override.yml
cat > docker-compose.override.yml << EOF
services:
  pmg-cloud:
    command: ["--addr=:8443", "--data-dir=/data", "--http-addr=:8080", "--insecure"]
    environment:
      - PMG_CLOUD_API_KEYS=$API_KEY
      - PMG_CLOUD_DASH_USER=admin
      - PMG_CLOUD_DASH_PASS=$DASH_PASS
    restart: unless-stopped
EOF

# Stop any existing containers
log_info "Stopping existing containers..."
$DOCKER_COMPOSE down 2>/dev/null || true

# Build and start
log_info "Building Docker image (this may take a few minutes)..."
$DOCKER_COMPOSE build --quiet 2>&1 | grep -E "^(Step|Successfully|ERROR)" || true

log_info "Starting services..."
$DOCKER_COMPOSE up -d

# Wait for service to be ready
log_info "Waiting for service to start..."
sleep 5

# Verify container is running
if docker ps | grep -q pmg-cloud; then
    log_success "Container is running"
else
    log_error "Container failed to start"
    echo ""
    log_info "Showing container logs:"
    $DOCKER_COMPOSE logs --tail 50
    exit 1
fi

# Wait for health endpoint
log_info "Waiting for service to be healthy..."
MAX_RETRIES=60
RETRY_COUNT=0

while [ $RETRY_COUNT -lt $MAX_RETRIES ]; do
    if curl -sf http://localhost:8080/healthz > /dev/null 2>&1; then
        log_success "Service is healthy!"
        break
    fi
    RETRY_COUNT=$((RETRY_COUNT + 1))
    if [ $RETRY_COUNT -eq $MAX_RETRIES ]; then
        log_error "Service did not become healthy after ${MAX_RETRIES} seconds"
        echo ""
        log_info "Showing logs:"
        $DOCKER_COMPOSE logs --tail 100
        echo ""
        log_warn "The service might still be starting. Check logs with:"
        log_warn "  $DOCKER_COMPOSE logs -f"
        exit 1
    fi
    sleep 1
done

# Get server IP
SERVER_IP=$(hostname -I | awk '{print $1}' || echo "localhost")

# =============================================================================
# SUCCESS!
# =============================================================================
echo ""
echo "╔═══════════════════════════════════════════════════════════════╗"
echo "║                   🎉 SETUP COMPLETE! 🎉                      ║"
echo "╚═══════════════════════════════════════════════════════════════╝"
echo ""
log_success "PMG Cloud is running!"
echo ""
echo "📊 Access Information:"
echo "  Dashboard: http://$SERVER_IP:8080"
echo "  gRPC Port: $SERVER_IP:8443"
echo ""
echo "🔑 Login Credentials:"
echo "  Username: admin"
echo "  Password: $DASH_PASS"
echo ""
echo "📁 Files:"
echo "  Working dir:  $(pwd)"
echo "  Credentials:  $(pwd)/credentials.txt"
echo "  Environment:  $(pwd)/.env.local"
echo "  Compose file: $(pwd)/docker-compose.yml"
echo ""
echo "⚠️  Security Notes:"
echo "  • Running in INSECURE mode (no TLS)"
echo "  • Only use on trusted internal networks"
echo "  • Save credentials.txt then delete it"
echo "  • For TLS setup, see DEPLOYMENT.md"
echo ""
echo "🔧 Useful Commands:"
echo "  View logs:    $DOCKER_COMPOSE logs -f"
echo "  Stop:         $DOCKER_COMPOSE down"
echo "  Restart:      $DOCKER_COMPOSE restart"
echo "  Status:       $DOCKER_COMPOSE ps"
echo "  Health:       curl http://localhost:8080/healthz"
echo ""
echo "📖 Next Steps:"
echo "  1. Access http://$SERVER_IP:8080 in your browser"
echo "  2. Login with the credentials above"
echo "  3. Go to Agents → Deploy New Agent"
echo "  4. Follow the wizard to deploy agents"
echo ""
echo "💡 Documentation:"
echo "  Quick Start:  cat QUICK-DEPLOY.md"
echo "  Full Guide:   cat DEPLOYMENT.md"
echo "  Scripts:      cat SCRIPTS.md"
echo ""
log_success "Installation completed successfully!"
