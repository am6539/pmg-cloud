# PMG Cloud - Quick Start Guide

Hướng dẫn nhanh triển khai pmg-cloud lên production trong 10 phút.

## Tóm Tắt Các Phương Thức Triển Khai

| Phương thức | Độ khó | Thời gian | Use case |
|-------------|--------|-----------|----------|
| **Docker + Cloudflare Tunnel** | Dễ | 10 phút | Khuyến nghị - Không cần setup TLS |
| **Docker + TLS trực tiếp** | Trung bình | 20 phút | VPS với IP public |
| **Docker + Nginx Reverse Proxy** | Khó | 30 phút | Production grade, nhiều services |
| **Systemd service** | Trung bình | 15 phút | Không dùng Docker |

---

## 🚀 Phương Thức 1: Docker + Cloudflare Tunnel (Khuyến Nghị)

**Ưu điểm:**
- Không cần certificate
- Tự động HTTPS
- DDoS protection miễn phí
- Hoạt động ngay cả sau NAT/firewall

### Bước 1: Clone và chuẩn bị

```bash
git clone <repo-url>
cd pmg-cloud

# Tạo thư mục và set permissions
mkdir -p data backups
chmod +x *.sh
```

### Bước 2: Cấu hình môi trường

```bash
# Tạo .env.production
cat > .env.production <<EOF
PMG_CLOUD_API_KEYS=$(openssl rand -hex 32)
PMG_CLOUD_DASH_USER=admin
PMG_CLOUD_DASH_PASS=$(openssl rand -base64 16)
EOF

# Lưu credentials
echo "Dashboard credentials:" | tee credentials.txt
echo "Username: admin" | tee -a credentials.txt
grep DASH_PASS .env.production | tee -a credentials.txt
```

### Bước 3: Cài đặt Cloudflare Tunnel

```bash
# Download cloudflared
wget https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64.deb
sudo dpkg -i cloudflared-linux-amd64.deb

# Login và tạo tunnel
cloudflared tunnel login
cloudflared tunnel create pmg-cloud

# Lưu tunnel ID
TUNNEL_ID=$(cloudflared tunnel list | grep pmg-cloud | awk '{print $1}')
echo "Tunnel ID: $TUNNEL_ID"

# Tạo config
mkdir -p ~/.cloudflared
cat > ~/.cloudflared/config.yml <<EOF
tunnel: $TUNNEL_ID
credentials-file: $HOME/.cloudflared/$TUNNEL_ID.json

ingress:
  - hostname: package.yourdomain.com
    service: http://localhost:8080
  - service: http_status:404
EOF

# Install as service
sudo cloudflared service install
sudo systemctl start cloudflared
sudo systemctl enable cloudflared
```

### Bước 4: Cập nhật DNS trong Cloudflare

1. Vào Cloudflare Dashboard → DNS
2. Thêm CNAME record:
   - Name: `package`
   - Target: `<tunnel-id>.cfargotunnel.com`
   - Proxy: Enabled (orange cloud)

### Bước 5: Deploy pmg-cloud

```bash
# Load environment
export $(cat .env.production | grep -v '^#' | xargs)

# Start services
docker compose up -d

# Verify
docker compose ps
docker compose logs -f
```

### Bước 6: Truy cập và test

```bash
# Health check
curl https://package.yourdomain.com/healthz

# Truy cập dashboard
# URL: https://package.yourdomain.com
# Username: admin
# Password: (xem trong credentials.txt)
```

**✅ Hoàn tất! PMG Cloud đã chạy với HTTPS tự động.**

---

## 🔧 Phương Thức 2: Docker + TLS Trực Tiếp

Dành cho VPS có IP public và domain đã trỏ về server.

### Bước 1-2: Giống Phương thức 1

### Bước 3: Generate TLS Certificate

**Option A: Let's Encrypt (Production)**

```bash
# Cài certbot
sudo apt update
sudo apt install -y certbot

# Lấy certificate (đảm bảo port 80 available)
sudo certbot certonly --standalone -d package.yourdomain.com

# Copy certificates
sudo cp /etc/letsencrypt/live/package.yourdomain.com/fullchain.pem certs/tls.crt
sudo cp /etc/letsencrypt/live/package.yourdomain.com/privkey.pem certs/tls.key
sudo chown $USER:$USER certs/tls.*

# Auto-renewal
echo "0 0 * * * certbot renew --quiet --deploy-hook 'docker compose restart pmg-cloud'" | crontab -
```

**Option B: Self-signed (Testing Only)**

```bash
mkdir -p certs
openssl req -x509 -newkey rsa:4096 \
  -keyout certs/tls.key -out certs/tls.crt \
  -days 365 -nodes \
  -subj "/CN=package.yourdomain.com"
```

### Bước 4: Deploy với production config

```bash
# Load environment
export $(cat .env.production | grep -v '^#' | xargs)

# Deploy
docker compose -f docker-compose.production.yml up -d

# Configure firewall
sudo ufw allow 8080/tcp
sudo ufw allow 8443/tcp
sudo ufw enable
```

### Bước 5: Verify

```bash
# Health check
curl http://your-server:8080/healthz

# Test gRPC port
telnet your-server 8443
```

**✅ Dashboard:** `http://your-server:8080`  
**✅ gRPC:** `your-server:8443`

---

## 🏢 Phương Thức 3: Production Grade với Nginx

### Prerequisites

```bash
sudo apt update
sudo apt install -y nginx certbot python3-certbot-nginx
```

### Quick Setup

```bash
# Clone và setup
git clone <repo-url>
cd pmg-cloud

# Cài đặt tự động
chmod +x setup-nginx.sh
sudo ./setup-nginx.sh

# Script sẽ hỏi:
# - Domain name
# - Email cho Let's Encrypt
# - Tự động cấu hình nginx + SSL + auto-renewal
```

### Manual Setup

```bash
# Copy nginx config
sudo cp nginx-pmg-cloud.conf /etc/nginx/sites-available/pmg-cloud

# Edit config thay YOUR_DOMAIN
sudo nano /etc/nginx/sites-available/pmg-cloud

# Enable site
sudo ln -s /etc/nginx/sites-available/pmg-cloud /etc/nginx/sites-enabled/

# Get SSL cert
sudo certbot --nginx -d package.yourdomain.com

# Start services
docker compose up -d

# Verify nginx
sudo nginx -t
sudo systemctl reload nginx
```

**✅ Dashboard:** `https://package.yourdomain.com`  
**✅ gRPC:** `package.yourdomain.com:8443`

---

## 💻 Phương Thức 4: Systemd Service (No Docker)

Dành cho môi trường không có Docker.

### Quick Setup

```bash
# Build binary
go build -o pmg-cloud .

# Install as service
chmod +x install-systemd.sh
sudo ./install-systemd.sh

# Script sẽ:
# - Tạo user pmg
# - Copy binary to /opt/pmg-cloud
# - Install systemd service
# - Create logrotate config
```

### Start Service

```bash
# Edit credentials
sudo nano /etc/pmg-cloud/env

# Start service
sudo systemctl start pmg-cloud
sudo systemctl enable pmg-cloud

# Check status
sudo systemctl status pmg-cloud

# View logs
sudo journalctl -u pmg-cloud -f
```

---

## 📊 Post-Deployment Setup

### 1. Setup Monitoring

```bash
# Install monitoring
chmod +x setup-cron.sh monitor.sh
sudo ./setup-cron.sh

# Configure alerts (optional)
export ALERT_EMAIL="admin@yourdomain.com"
export ALERT_WEBHOOK="https://hooks.slack.com/services/YOUR/WEBHOOK/URL"

# Test monitoring
./monitor.sh
```

### 2. Deploy First Agent

**Via Dashboard (Khuyến nghị):**

1. Login dashboard → **Agents** → **+ Deploy New Agent**
2. Select OS và architecture
3. Configure token (label, group, expiry)
4. Copy command và run trên target machine:

```bash
curl -sSfL http://your-server:8080/install.sh | sh -s -- --token=pmgenroll_xxx
```

**Manual Configuration:**

```bash
# Trên máy agent
curl -sSfL https://raw.githubusercontent.com/safedep/pmg/main/install.sh | sh

# Configure
mkdir -p ~/.pmg
cat > ~/.pmg/config.yml <<EOF
cloud:
  enabled: true
  addr: "your-server:8443"
  api_key: "your-api-key"
  insecure: false
EOF

# Test
pmg cloud sync
```

### 3. Setup Backup

```bash
# Manual backup
./manage.sh backup

# List backups
ls -lh backups/

# Auto backup đã được setup trong cron jobs
```

---

## 🔍 Verification Checklist

```bash
# ✓ Service running
docker compose ps
# hoặc
sudo systemctl status pmg-cloud

# ✓ Health endpoint
curl http://localhost:8080/healthz | jq

# ✓ Dashboard accessible
curl -I https://your-domain/

# ✓ gRPC port open
telnet your-server 8443

# ✓ Logs normal
./manage.sh logs

# ✓ Disk space OK
df -h ./data/

# ✓ Monitoring working
./monitor.sh
```

---

## 🛠️ Management Commands

```bash
# Service control
./manage.sh start
./manage.sh stop
./manage.sh restart
./manage.sh status

# Monitoring
./manage.sh health
./monitor.sh

# Logs
./manage.sh logs              # Real-time
./manage.sh logs-tail 100     # Last 100 lines

# Backup/Restore
./manage.sh backup
./manage.sh restore backups/pmg-cloud-backup-20260608_070000.tar.gz

# Maintenance
./manage.sh clean 30          # Delete events older than 30 days
./manage.sh update            # Update to latest version
```

---

## 🚨 Troubleshooting

### Service không start

```bash
# Check logs
docker compose logs
# hoặc
sudo journalctl -u pmg-cloud -n 50

# Check ports
sudo netstat -tlnp | grep -E '8080|8443'

# Rebuild
docker compose down
docker compose build --no-cache
docker compose up -d
```

### Agents không kết nối

```bash
# Verify network
telnet your-server 8443

# Check API key
# Dashboard → Groups → Verify key exists

# Agent debug
pmg cloud sync --debug
```

### Dashboard không accessible

```bash
# Check firewall
sudo ufw status
sudo ufw allow 8080/tcp

# Check nginx (nếu dùng)
sudo nginx -t
sudo systemctl status nginx

# Check Cloudflare Tunnel (nếu dùng)
sudo systemctl status cloudflared
cloudflared tunnel info pmg-cloud
```

### Disk full

```bash
# Check usage
du -sh data/
df -h

# Clean old events
./manage.sh clean 7

# Manual cleanup
find data/ -name "events-*.jsonl" -mtime +7 -delete
```

---

## 📚 Next Steps

1. **Security Hardening:**
   - Đổi default passwords ngay
   - Setup firewall rules
   - Enable rate limiting
   - Regular security updates

2. **CI/CD Integration:**
   - Xem `DEPLOYMENT.md` phần CI/CD Integration
   - Setup GitHub Actions / GitLab CI
   - Configure agents trong pipelines

3. **Monitoring & Alerts:**
   - Configure email/webhook alerts
   - Setup external monitoring (UptimeRobot, etc.)
   - Review logs định kỳ

4. **Backup Strategy:**
   - Verify daily backups hoạt động
   - Test restore procedure
   - Off-site backup storage (optional)

---

## 📖 Documentation

- **Full Deployment Guide:** `DEPLOYMENT.md`
- **Project README:** `README.md`
- **Management Commands:** `./manage.sh help`
- **Monitoring Guide:** `./monitor.sh --help`

---

## 🆘 Support

- GitHub Issues: `<repo-url>/issues`
- PMG Documentation: https://github.com/safedep/pmg
- Dashboard built-in help: Settings → Help

**Installation Time: ~10 minutes**  
**Current Date: 2026-06-08**
