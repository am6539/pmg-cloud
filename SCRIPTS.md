# PMG Cloud - Production Deployment Files

Tài liệu và scripts hỗ trợ triển khai pmg-cloud lên production.

## 📁 Cấu Trúc Files

```
pmg-cloud/
├── QUICKSTART.md                  # ⭐ BẮT ĐẦU TỪ ĐÂY - Hướng dẫn nhanh 10 phút
├── DEPLOYMENT.md                  # Hướng dẫn chi tiết đầy đủ
├── README.md                      # Project overview (original)
│
├── Docker Deployment
│   ├── docker-compose.yml              # Cloudflare Tunnel config (hiện tại)
│   ├── docker-compose.production.yml   # TLS trực tiếp config
│   ├── Dockerfile                      # Image definition
│   └── .env.production                 # Environment variables template
│
├── Deployment Scripts
│   ├── deploy-production.sh       # 🚀 Auto deployment script
│   ├── manage.sh                  # 🛠️ Service management (start/stop/backup/logs)
│   └── install-systemd.sh         # Systemd service installation
│
├── Monitoring & Maintenance
│   ├── monitor.sh                 # 📊 Health checks + alerting
│   ├── setup-cron.sh              # Cron jobs setup (auto backup/cleanup)
│   └── .env.production            # Config cho monitoring alerts
│
├── Reverse Proxy (Optional)
│   ├── nginx-pmg-cloud.conf       # Nginx config template
│   └── setup-nginx.sh             # 🔧 Nginx auto setup
│
├── Systemd Service (Optional)
│   ├── pmg-cloud.service          # Systemd unit file
│   └── install-systemd.sh         # Installation script
│
└── Data Directories (gitignored)
    ├── data/                      # Event logs + config
    ├── certs/                     # TLS certificates
    └── backups/                   # Automated backups
```

## 🎯 Quick Actions

### 🚀 Deploy lần đầu (10 phút)

```bash
# 1. Read quick start guide
cat QUICKSTART.md

# 2. Run deployment script
./deploy-production.sh

# 3. Setup monitoring
./setup-cron.sh
```

### 🛠️ Quản lý hàng ngày

```bash
# Check service status
./manage.sh status

# View logs
./manage.sh logs

# Manual backup
./manage.sh backup

# Health check
./manage.sh health
```

### 📊 Monitoring

```bash
# Run health checks
./monitor.sh

# Generate report
./monitor.sh --report

# View monitoring logs
tail -f /var/log/pmg-cloud-monitor.log
```

## 📋 Scripts Chi Tiết

### 🚀 deploy-production.sh

Auto deployment script với interactive prompts.

**Chức năng:**
- Kiểm tra Docker và Docker Compose
- Tạo TLS certificates (self-signed hoặc Let's Encrypt)
- Load environment variables
- Build và start Docker containers
- Verify deployment

**Usage:**
```bash
./deploy-production.sh
```

**Prompts:**
- Certificate generation method (self-signed / Let's Encrypt / skip)
- Deployment config (TLS direct / Cloudflare Tunnel)

---

### 🛠️ manage.sh

Service management script cho production.

**Commands:**
```bash
./manage.sh start          # Khởi động services
./manage.sh stop           # Dừng services
./manage.sh restart        # Restart services
./manage.sh status         # Kiểm tra status + resource usage
./manage.sh logs           # Real-time logs
./manage.sh logs-tail N    # Last N lines
./manage.sh backup         # Backup data/ directory
./manage.sh restore FILE   # Restore từ backup
./manage.sh health         # Health check endpoint
./manage.sh update         # Git pull + rebuild + restart
./manage.sh clean [DAYS]   # Xóa events cũ hơn N ngày (default: 30)
./manage.sh help           # Show help
```

**Features:**
- Auto cleanup old backups (giữ 7 bản gần nhất)
- Docker stats integration
- Safety confirmations cho restore

---

### 📊 monitor.sh

Comprehensive monitoring với alerting.

**Features:**
- Health endpoint check
- Docker container status
- Disk space monitoring
- Memory và CPU usage
- Event files verification
- Port connectivity
- Automated alerting (email + webhook)

**Usage:**
```bash
./monitor.sh              # Run all checks
./monitor.sh --report     # Generate detailed report
```

**Configuration via environment:**
```bash
export ALERT_EMAIL="admin@example.com"
export ALERT_WEBHOOK="https://hooks.slack.com/services/YOUR/WEBHOOK"
./monitor.sh
```

**Thresholds (edit trong script):**
- Response time: 5000ms
- Disk space: 1000MB minimum
- Memory: 90% max
- CPU: 90% max

---

### ⏰ setup-cron.sh

Setup automated cron jobs cho maintenance.

**Scheduled tasks:**
- **Every 5 minutes:** Health monitoring
- **Every minute:** Quick health ping
- **Every hour:** Disk space check
- **Daily 2:00 AM:** Automated backup
- **Weekly Sunday 3:00 AM:** Cleanup old events (>30 days)
- **Monthly 1st 1:00 AM:** Detailed report

**Usage:**
```bash
sudo ./setup-cron.sh      # System-wide installation
./setup-cron.sh           # User-level installation
```

---

### 🔧 setup-nginx.sh

Automated nginx reverse proxy setup.

**Features:**
- Install nginx + certbot
- Generate nginx config
- Obtain Let's Encrypt certificate
- Configure firewall
- Setup auto-renewal

**Usage:**
```bash
sudo ./setup-nginx.sh
```

**Prompts:**
- Domain name
- Email for Let's Encrypt

---

### 💻 install-systemd.sh

Install pmg-cloud as systemd service (non-Docker).

**Features:**
- Create dedicated user (pmg)
- Build and install binary to /opt/pmg-cloud
- Setup systemd service
- Configure logrotate
- Install management commands

**Usage:**
```bash
sudo ./install-systemd.sh
```

**Created commands:**
- `pmg-cloud-status` - Check service status
- `pmg-cloud-logs` - View logs

## 📖 Documentation Files

### ⭐ QUICKSTART.md

**BẮT ĐẦU TỪ ĐÂY** - Hướng dẫn nhanh triển khai trong 10 phút.

**Nội dung:**
- 4 phương thức deployment
- Step-by-step instructions
- Quick verification checklist
- Common troubleshooting

**Recommended for:** First-time deployment

---

### 📚 DEPLOYMENT.md

Hướng dẫn chi tiết và đầy đủ cho production deployment.

**Nội dung:**
- Architecture overview
- System requirements
- Detailed deployment steps
- Security best practices
- Performance tuning
- CI/CD integration examples
- Troubleshooting guide
- Management procedures

**Recommended for:** Production setup, advanced configuration

---

### 📄 README.md (Original)

Project overview và basic usage.

**Nội dung:**
- Architecture
- Quick start (dev mode)
- Agent deployment
- Configuration
- API reference

## 🔐 Configuration Files

### .env.production (Template)

Environment variables cho production.

**Required variables:**
```bash
PMG_CLOUD_API_KEYS=your-api-key
PMG_CLOUD_DASH_USER=admin
PMG_CLOUD_DASH_PASS=your-password
```

**Optional:**
```bash
PMG_CLOUD_RETENTION_DAYS=30
PMG_CLOUD_MALWARE_REFRESH_INTERVAL=6h
```

**Security notes:**
- File này không được commit vào git (.gitignore)
- Generate strong API key: `openssl rand -hex 32`
- Use strong password: `openssl rand -base64 16`

---

### docker-compose.yml

Cloudflare Tunnel configuration (current setup).

**Features:**
- Single port 8080 (HTTP + gRPC muxed)
- No TLS needed (Cloudflare handles it)
- Suitable for: Behind firewall/NAT

---

### docker-compose.production.yml

Direct TLS configuration.

**Features:**
- Dedicated gRPC port 8443
- HTTP dashboard port 8080
- TLS certificates required
- Suitable for: VPS with public IP

---

### nginx-pmg-cloud.conf

Nginx reverse proxy template.

**Features:**
- HTTP to HTTPS redirect
- Dashboard proxy (443 → 8080)
- gRPC proxy (8443 → 8443)
- Security headers
- Rate limiting (commented, uncomment if needed)

## 🚦 Deployment Workflows

### Workflow 1: Cloudflare Tunnel (Khuyến nghị)

```bash
1. ./deploy-production.sh
   → Choose Cloudflare Tunnel option
   
2. Setup Cloudflare Tunnel externally
   cloudflared tunnel create pmg-cloud
   
3. Update docker-compose.yml with tunnel config

4. Start: docker compose up -d

5. Setup monitoring: ./setup-cron.sh
```

**Pros:** Easiest, no cert management, DDoS protection  
**Cons:** Depends on Cloudflare

---

### Workflow 2: Direct TLS

```bash
1. Generate certificates (Let's Encrypt or self-signed)

2. ./deploy-production.sh
   → Choose TLS direct option
   
3. Start: docker compose -f docker-compose.production.yml up -d

4. Setup firewall:
   sudo ufw allow 8080/tcp
   sudo ufw allow 8443/tcp
   
5. Setup monitoring: ./setup-cron.sh
```

**Pros:** Full control, no external dependencies  
**Cons:** Cert management, exposed ports

---

### Workflow 3: Nginx Reverse Proxy

```bash
1. sudo ./setup-nginx.sh
   → Interactive setup (domain, email, SSL)
   
2. Update docker-compose.yml:
   Bind to localhost only:
   ports:
     - "127.0.0.1:8080:8080"
     - "127.0.0.1:8443:8443"
   
3. docker compose up -d

4. Verify nginx: sudo nginx -t

5. Setup monitoring: ./setup-cron.sh
```

**Pros:** Production-grade, centralized SSL, rate limiting  
**Cons:** More complex, additional layer

---

### Workflow 4: Systemd Service

```bash
1. Build binary: go build -o pmg-cloud .

2. sudo ./install-systemd.sh

3. Edit credentials: sudo nano /etc/pmg-cloud/env

4. Start: sudo systemctl start pmg-cloud

5. Enable auto-start: sudo systemctl enable pmg-cloud

6. Setup monitoring: sudo ./setup-cron.sh
```

**Pros:** Native service, no Docker overhead  
**Cons:** Manual updates, less portable

## 🔍 Verification Commands

After deployment, verify với commands sau:

```bash
# Service status
./manage.sh status
# hoặc
docker compose ps
# hoặc
sudo systemctl status pmg-cloud

# Health endpoint
curl http://localhost:8080/healthz | jq

# Full health check
./monitor.sh

# View logs
./manage.sh logs

# Test gRPC port
telnet localhost 8443

# Disk space
df -h ./data/

# Dashboard access
curl -I http://localhost:8080/
```

## 🆘 Troubleshooting Quick Reference

| Issue | Command | Fix |
|-------|---------|-----|
| Service won't start | `docker compose logs` | Check `.env.production` |
| Port already in use | `sudo netstat -tlnp \| grep 8080` | Kill conflicting process |
| Certificate error | `ls -la certs/` | Regenerate certificates |
| Disk full | `du -sh data/` | Run `./manage.sh clean 7` |
| High memory | `docker stats` | Restart container |
| Agents can't connect | `telnet host 8443` | Check firewall |
| Dashboard 502 | `./manage.sh status` | Check backend running |

## 📊 Monitoring Dashboard

Access monitoring logs:

```bash
# Main monitoring log
tail -f /var/log/pmg-cloud-monitor.log

# Backup log
tail -f /var/log/pmg-cloud-backup.log

# Cleanup log
tail -f /var/log/pmg-cloud-cleanup.log

# Health check log
tail -f /var/log/pmg-cloud-health.log

# Disk usage log
tail -f /var/log/pmg-cloud-disk.log
```

## 🔄 Update Procedures

### Update pmg-cloud

```bash
# Automated
./manage.sh update

# Manual
git pull
./manage.sh backup
docker compose build --no-cache
docker compose up -d
```

### Update dependencies

```bash
# Update Docker images
docker compose pull
docker compose up -d

# Update system packages
sudo apt update && sudo apt upgrade
```

### Update scripts

```bash
git pull
chmod +x *.sh
```

## 📝 Notes

- Tất cả scripts đều có `--help` option
- Logs được rotate tự động qua logrotate
- Backups tự động giữ 7 bản gần nhất
- Monitoring alerts qua email và webhook
- Scripts an toàn với error handling (`set -e`)

## 🔗 Related Documentation

- **[QUICKSTART.md](QUICKSTART.md)** - Bắt đầu ở đây
- **[DEPLOYMENT.md](DEPLOYMENT.md)** - Chi tiết đầy đủ
- **[README.md](README.md)** - Project overview
- **[PMG Documentation](https://github.com/safedep/pmg)** - Agent setup

## 📅 Last Updated

2026-06-08

---

**Ready to deploy?** → Start with [QUICKSTART.md](QUICKSTART.md) 🚀
