# 🚀 Hướng Dẫn Triển Khai Nhanh PMG Cloud

**Dành cho:** Server sau NAT/firewall, không có domain, internal network  
**Thời gian:** 10-15 phút  
**Date:** 2026-06-08

---

## ✅ Phương Pháp Được Recommend: Quick Setup (Insecure Mode)

**Phù hợp cho:**
- Server trong internal network
- Không cần TLS/SSL (trusted network)
- Setup nhanh nhất
- Dev/staging environment

**Không phù hợp cho:**
- Public-facing servers
- Production với sensitive data
- Environments cần TLS

---

## 📋 Yêu Cầu

- Ubuntu/Debian Linux server
- SSH access với sudo privileges
- Port 8080 và 8443 available
- Internet connection (để cài Docker và pull images)
- ~2GB RAM minimum
- ~10GB disk space

---

## 🚀 Cách 1: Automated Setup (Khuyến Nghị)

### Trên Server Production:

```bash
# 1. SSH vào server
ssh user@your-server-ip

# 2. Download và chạy quick setup script
curl -sSL https://raw.githubusercontent.com/am6539/pmg-cloud/master/quick-setup.sh -o quick-setup.sh
chmod +x quick-setup.sh
./quick-setup.sh
```

Script sẽ tự động:
1. ✅ Cài Docker và Docker Compose
2. ✅ Clone repository
3. ✅ Generate credentials mạnh
4. ✅ Tạo directories
5. ✅ Start services
6. ✅ Verify health

**Thời gian:** ~10 phút

---

## 🔧 Cách 2: Manual Setup (Chi Tiết)

Nếu muốn control từng bước:

### Bước 1: Cài Docker (5 phút)

```bash
# Update packages
sudo apt update

# Install Docker
curl -fsSL https://get.docker.com -o get-docker.sh
sudo sh get-docker.sh

# Add user to docker group
sudo usermod -aG docker $USER

# Logout và login lại để apply group changes
# Hoặc chạy:
newgrp docker

# Verify
docker --version
docker compose version
```

### Bước 2: Clone Repository (1 phút)

```bash
# Clone repo
git clone https://github.com/am6539/pmg-cloud.git
cd pmg-cloud

# Verify files
ls -la
```

### Bước 3: Configure Environment (2 phút)

```bash
# Generate credentials
API_KEY=$(openssl rand -hex 32)
DASH_PASS=$(openssl rand -base64 16)

# Create .env.local
cat > .env.local <<EOF
PMG_CLOUD_API_KEYS=$API_KEY
PMG_CLOUD_DASH_USER=admin
PMG_CLOUD_DASH_PASS=$DASH_PASS
PMG_CLOUD_RETENTION_DAYS=30
PMG_CLOUD_MALWARE_REFRESH_INTERVAL=6h
EOF

# Save credentials
echo "Dashboard: admin / $DASH_PASS" | tee credentials.txt
echo "API Key: $API_KEY" | tee -a credentials.txt

# ⚠️ IMPORTANT: Save these credentials now!
cat credentials.txt
```

### Bước 4: Create Directories (1 phút)

```bash
mkdir -p data certs backups
```

### Bước 5: Start Services (3 phút)

```bash
# Create override for insecure mode
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

# View logs
docker compose logs -f
# Press Ctrl+C to exit logs
```

### Bước 6: Verify (2 phút)

```bash
# Check service status
docker compose ps

# Check health endpoint
curl http://localhost:8080/healthz | jq

# Get server IP
hostname -I | awk '{print $1}'
```

---

## ✅ Truy Cập Dashboard

### Từ Browser:

```
URL: http://YOUR_SERVER_IP:8080
Username: admin
Password: [password từ credentials.txt]
```

**Thay YOUR_SERVER_IP bằng IP thực của server.**

### Test từ local machine:

```bash
# Test connection
curl http://YOUR_SERVER_IP:8080/healthz

# Nếu không connect được, check firewall:
# Trên server:
sudo ufw allow 8080/tcp
sudo ufw allow 8443/tcp
```

---

## 📱 Deploy Agents

Sau khi dashboard đã chạy:

### Cách 1: Dashboard Wizard (Dễ nhất)

1. Login dashboard → **Agents** → **+ Deploy New Agent**
2. Chọn OS và architecture
3. Configure token
4. Copy command và chạy trên agent machine:

```bash
curl -sSfL http://YOUR_SERVER_IP:8080/install.sh | sh -s -- --token=pmgenroll_xxx
```

### Cách 2: Manual Configuration

Trên agent machine:

```bash
# Install PMG
curl -sSfL https://raw.githubusercontent.com/safedep/pmg/main/install.sh | sh

# Configure
mkdir -p ~/.pmg
cat > ~/.pmg/config.yml <<EOF
cloud:
  enabled: true
  addr: "YOUR_SERVER_IP:8443"
  api_key: "YOUR_API_KEY"
  insecure: true
EOF

# Test connection
pmg cloud sync
```

**Note:** `insecure: true` vì server đang chạy ở insecure mode.

---

## 🛠️ Management Commands

### Service Management

```bash
# Start
docker compose up -d

# Stop
docker compose down

# Restart
docker compose restart

# Status
docker compose ps

# Logs
docker compose logs -f
docker compose logs --tail 100
```

### Health Checks

```bash
# Quick check
curl http://localhost:8080/healthz

# Detailed check
curl http://localhost:8080/healthz | jq '.'
```

### Backup

```bash
# Manual backup
tar -czf backup-$(date +%Y%m%d).tar.gz data/

# List backups
ls -lh backup-*.tar.gz
```

### Cleanup

```bash
# Clean old event files (>30 days)
find data/ -name "events-*.jsonl" -mtime +30 -delete

# Check disk usage
du -sh data/
df -h
```

---

## 🔒 Security Notes

⚠️ **IMPORTANT:** Setup này chạy ở **insecure mode** (không có TLS).

**An toàn khi:**
- Server trong internal network
- Trusted network only
- Dev/staging environment
- Behind VPN/firewall

**KHÔNG an toàn cho:**
- Public internet
- Untrusted networks
- Production với sensitive data

**Để upgrade lên TLS/SSL:**
- Xem `DEPLOYMENT.md` cho production setup
- Sử dụng Cloudflare Tunnel (không cần domain)
- Hoặc setup nginx với Let's Encrypt

---

## 🆘 Troubleshooting

### Service không start

```bash
# Check logs
docker compose logs

# Check Docker running
sudo systemctl status docker

# Restart Docker
sudo systemctl restart docker

# Rebuild
docker compose down
docker compose build --no-cache
docker compose up -d
```

### Không connect được từ browser

```bash
# Check firewall
sudo ufw status

# Allow ports
sudo ufw allow 8080/tcp
sudo ufw allow 8443/tcp

# Check service listening
sudo netstat -tlnp | grep -E '8080|8443'

# Check from server
curl http://localhost:8080/healthz
```

### Agents không connect được

```bash
# From agent machine, test connection
telnet YOUR_SERVER_IP 8443

# Check firewall on server
sudo ufw allow 8443/tcp

# Verify API key
# Dashboard → Groups → Check API key exists
```

### Port already in use

```bash
# Find what's using the port
sudo lsof -i :8080
sudo lsof -i :8443

# Kill the process
sudo kill -9 <PID>

# Or use different ports in docker-compose.override.yml
```

---

## 📊 Monitoring (Optional)

### Setup automated monitoring:

```bash
# Install monitoring script
chmod +x monitor.sh

# Run manual check
./monitor.sh

# Setup cron (runs every 5 minutes)
chmod +x setup-cron.sh
./setup-cron.sh
```

### Configure alerts:

```bash
# Email alerts
export ALERT_EMAIL="admin@yourdomain.com"

# Slack/Discord webhook
export ALERT_WEBHOOK="https://hooks.slack.com/services/YOUR/WEBHOOK"

# Run with alerts
./monitor.sh
```

---

## 📈 Upgrade Path

Khi cần upgrade lên production-grade setup:

### Option 1: Cloudflare Tunnel (Easiest)
- Không cần domain hoặc IP public
- Tự động HTTPS
- Xem `DEPLOYMENT.md` § Cloudflare Tunnel

### Option 2: Get a Domain + Let's Encrypt
- Mua domain ($10/năm)
- Point DNS to server
- Run `./setup-nginx.sh`
- Xem `DEPLOYMENT.md` § TLS Setup

### Option 3: VPN Access
- Keep insecure mode
- Access qua VPN only
- Wireguard hoặc OpenVPN

---

## 💰 Cost Estimation

**Hardware:**
- Minimum: 2GB RAM, 2 CPU, 10GB disk (~$5-10/month VPS)
- Recommended: 4GB RAM, 2 CPU, 20GB disk (~$10-20/month)

**Services:**
- Docker: Free
- PMG Cloud: Open source (free)
- Domain (optional): ~$10/năm
- Cloudflare (optional): Free tier available

**Total:** $5-20/month cho infrastructure

---

## ✅ Post-Deployment Checklist

- [ ] Service running: `docker compose ps`
- [ ] Health check: `curl http://localhost:8080/healthz`
- [ ] Dashboard accessible từ browser
- [ ] Credentials saved securely
- [ ] credentials.txt deleted from server
- [ ] Firewall ports opened (8080, 8443)
- [ ] At least 1 agent deployed và connected
- [ ] Test package scan works
- [ ] Monitoring setup (optional)
- [ ] Backup tested (optional)

---

## 📚 Further Documentation

- **Full Production Guide:** `DEPLOYMENT.md`
- **Scripts Reference:** `SCRIPTS.md`
- **Security Checklist:** `PRODUCTION-CHECKLIST.md`
- **Quick Start:** `QUICKSTART.md`

---

## 📞 Support

- **GitHub Issues:** https://github.com/am6539/pmg-cloud/issues
- **PMG Docs:** https://github.com/safedep/pmg
- **Dashboard Help:** Settings → Help (in-app)

---

**Setup time:** ~10-15 minutes  
**Difficulty:** Easy  
**Status:** Production-ready for internal networks  

**Happy deploying! 🚀**
