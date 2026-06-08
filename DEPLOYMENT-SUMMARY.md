# 📦 PMG Cloud - Production Deployment Package

**Version:** 1.0  
**Date:** 2026-06-08  
**Package Contents:** Scripts, Documentation, Configurations

---

## 🎯 Mục Đích

Package này cung cấp đầy đủ scripts và tài liệu để triển khai pmg-cloud lên production environment một cách an toàn, nhanh chóng và đầy đủ.

## 📚 Tài Liệu (Đọc Theo Thứ Tự)

### 1. ⭐ QUICKSTART.md - BẮT ĐẦU TỪ ĐÂY
**Mục đích:** Triển khai nhanh trong 10 phút  
**Đối tượng:** DevOps, Developers, SysAdmins lần đầu triển khai

**Nội dung:**
- 4 phương thức deployment (Cloudflare Tunnel, TLS Direct, Nginx, Systemd)
- Step-by-step instructions cho mỗi phương thức
- Verification checklist
- Quick troubleshooting

**Khi nào đọc:** Lần đầu tiên triển khai, hoặc cần deploy nhanh

---

### 2. 📖 DEPLOYMENT.md - Chi Tiết Đầy Đủ
**Mục đích:** Hướng dẫn chi tiết và comprehensive  
**Đối tượng:** Production deployment, enterprise setup

**Nội dung:**
- Architecture overview
- System requirements chi tiết
- Security best practices
- Performance tuning
- CI/CD integration examples
- Troubleshooting guide đầy đủ
- Management procedures

**Khi nào đọc:** 
- Setup production environment
- Cần hiểu sâu về architecture
- Troubleshooting các vấn đề phức tạp

---

### 3. 🛠️ SCRIPTS.md - Scripts Reference
**Mục đích:** Documentation cho tất cả scripts  
**Đối tượng:** DevOps operators, automation engineers

**Nội dung:**
- File structure overview
- Chi tiết từng script (usage, features, examples)
- Deployment workflows
- Management commands reference
- Monitoring dashboard

**Khi nào đọc:**
- Cần tham khảo cách dùng scripts
- Setup automation
- Customize workflows

---

### 4. ✅ PRODUCTION-CHECKLIST.md - Readiness Checklist
**Mục đích:** Verify production readiness  
**Đối tượng:** DevOps lead, Security team, QA

**Nội dung:**
- Security checklist (credentials, TLS, firewall)
- Infrastructure checklist (resources, network)
- Deployment checklist (pre/post deployment)
- Monitoring & maintenance checklist
- Testing checklist (functional, performance)
- Disaster recovery checklist
- Sign-off form

**Khi nào dùng:**
- Trước khi go-live
- Pre-production review
- Security audit
- Quarterly review

---

### 5. 📄 README.md - Project Overview
**Mục đích:** Project documentation gốc  
**Đối tượng:** Developers, contributors

**Nội dung:**
- Architecture
- Quick start (dev mode)
- Configuration reference
- API documentation
- CLI flags

**Khi nào đọc:**
- Hiểu về project
- Development mode
- Contributing

---

## 🚀 Scripts & Tools

### Deployment Scripts

#### 1. deploy-production.sh
**Purpose:** Automated deployment với interactive setup  
**When to use:** Initial deployment, quick setup

```bash
./deploy-production.sh
```

**Features:**
- Docker/Compose verification
- TLS certificate generation (self-signed/Let's Encrypt)
- Environment variable loading
- Container build & start
- Deployment verification

**Prompts:**
- Certificate method selection
- Deployment config choice

---

#### 2. manage.sh - Service Management
**Purpose:** Daily operations và maintenance  
**When to use:** Routine service management

```bash
./manage.sh [command]

Commands:
  start              Khởi động services
  stop               Dừng services
  restart            Restart services
  status             Check status + resources
  logs               Real-time logs
  logs-tail N        Last N lines
  backup             Backup data
  restore FILE       Restore from backup
  health             Health check
  update             Update version
  clean [DAYS]       Delete old events
  help               Show help
```

**Features:**
- Docker stats integration
- Automated backup rotation (keep 7)
- Safety confirmations
- Health checks

---

#### 3. monitor.sh - Monitoring & Alerting
**Purpose:** Comprehensive health monitoring  
**When to use:** Automated monitoring (via cron) hoặc manual checks

```bash
./monitor.sh              # Run all checks
./monitor.sh --report     # Generate detailed report

# With alerting
export ALERT_EMAIL="admin@example.com"
export ALERT_WEBHOOK="https://hooks.slack.com/..."
./monitor.sh
```

**Checks:**
- Health endpoint (response time)
- Docker container status
- Disk space (threshold: 1GB)
- Memory usage (threshold: 90%)
- CPU usage (threshold: 90%)
- Event files integrity
- Port connectivity

**Alerting:**
- Email (via mail command)
- Webhook (Slack, Discord, etc.)
- Severity levels (CRITICAL, WARNING, INFO)

---

#### 4. setup-cron.sh - Automated Tasks
**Purpose:** Setup cron jobs cho maintenance  
**When to use:** Post-deployment automation setup

```bash
sudo ./setup-cron.sh      # System-wide
./setup-cron.sh           # User-level
```

**Scheduled Tasks:**
- Every 5min: Health monitoring
- Every 1min: Quick health ping
- Hourly: Disk space check
- Daily 2AM: Automated backup
- Weekly Sun 3AM: Cleanup old events (>30d)
- Monthly 1st 1AM: Detailed report

---

### Infrastructure Scripts

#### 5. setup-nginx.sh - Nginx Reverse Proxy
**Purpose:** Automated nginx + SSL setup  
**When to use:** Production-grade deployment với reverse proxy

```bash
sudo ./setup-nginx.sh
```

**Features:**
- Nginx + Certbot installation
- Config generation
- Let's Encrypt certificate
- Firewall configuration
- Auto-renewal setup

**Prompts:**
- Domain name
- Email for Let's Encrypt

---

#### 6. install-systemd.sh - Systemd Service
**Purpose:** Non-Docker deployment  
**When to use:** Environments without Docker

```bash
sudo ./install-systemd.sh
```

**Features:**
- Create dedicated user (pmg)
- Binary installation to /opt/pmg-cloud
- Systemd service setup
- Logrotate configuration
- Management commands installation

**Created Commands:**
- `pmg-cloud-status`
- `pmg-cloud-logs`

---

## 📁 Configuration Files

### Docker Configurations

#### docker-compose.yml (Default)
**Purpose:** Cloudflare Tunnel setup  
**Ports:** 8080 (HTTP + gRPC muxed)  
**TLS:** Handled by Cloudflare

```bash
docker compose up -d
```

---

#### docker-compose.production.yml
**Purpose:** Direct TLS setup  
**Ports:** 8080 (HTTP), 8443 (gRPC)  
**TLS:** Self-managed certificates

```bash
docker compose -f docker-compose.production.yml up -d
```

---

### Environment Configuration

#### .env.production (Template)
**Purpose:** Production environment variables

**Required:**
```bash
PMG_CLOUD_API_KEYS=<32-byte-hex>
PMG_CLOUD_DASH_USER=admin
PMG_CLOUD_DASH_PASS=<strong-password>
```

**Optional:**
```bash
PMG_CLOUD_RETENTION_DAYS=30
PMG_CLOUD_MALWARE_REFRESH_INTERVAL=6h
```

**Generation:**
```bash
# API key
openssl rand -hex 32

# Password
openssl rand -base64 16
```

---

### Nginx Configuration

#### nginx-pmg-cloud.conf
**Purpose:** Nginx reverse proxy template

**Features:**
- HTTP → HTTPS redirect
- Dashboard proxy (443 → 8080)
- gRPC proxy (8443 → 8443)
- Security headers
- Rate limiting (commented)

**Installation:**
```bash
sudo cp nginx-pmg-cloud.conf /etc/nginx/sites-available/pmg-cloud
sudo ln -s /etc/nginx/sites-available/pmg-cloud /etc/nginx/sites-enabled/
sudo nginx -t
sudo systemctl reload nginx
```

---

### Systemd Service

#### pmg-cloud.service
**Purpose:** Systemd unit file

**Features:**
- Dedicated user (pmg)
- Environment file support
- Security hardening
- Auto-restart policy
- Log management

**Installation:**
```bash
sudo cp pmg-cloud.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable pmg-cloud
sudo systemctl start pmg-cloud
```

---

## 🎓 Getting Started Guide

### For First-Time Deployment

**Recommended Path:**

```
1. Read QUICKSTART.md (10 phút)
   ↓
2. Choose deployment method:
   - Cloudflare Tunnel (easiest) → Section 1 of QUICKSTART
   - Direct TLS → Section 2 of QUICKSTART
   - Nginx Proxy → Section 3 of QUICKSTART
   - Systemd → Section 4 of QUICKSTART
   ↓
3. Run deployment script:
   ./deploy-production.sh
   ↓
4. Setup monitoring:
   ./setup-cron.sh
   ↓
5. Verify with checklist:
   PRODUCTION-CHECKLIST.md (Security + Deployment sections)
   ↓
6. Test agent connection
   ↓
7. Done! ✅
```

**Time Required:** 10-30 minutes (tùy method)

---

### For Existing Deployment Management

**Daily Operations:**

```bash
# Check status
./manage.sh status

# View logs
./manage.sh logs

# Manual backup
./manage.sh backup

# Health check
./manage.sh health
```

**Weekly Tasks:**

```bash
# Review monitoring logs
tail -f /var/log/pmg-cloud-monitor.log

# Check disk usage
./manage.sh status

# Review backups
ls -lh backups/
```

**Monthly Tasks:**

```bash
# Generate report
./monitor.sh --report

# Review checklist
Check PRODUCTION-CHECKLIST.md (Post-Launch section)

# Update system
./manage.sh update
```

---

## 🏗️ Deployment Methods Comparison

| Feature | Cloudflare Tunnel | Direct TLS | Nginx Proxy | Systemd |
|---------|-------------------|------------|-------------|---------|
| **Setup Time** | 10 min | 20 min | 30 min | 15 min |
| **Difficulty** | Easy | Medium | Hard | Medium |
| **TLS Management** | Automatic | Manual | Manual | Manual |
| **DDoS Protection** | Yes | No | No | No |
| **Behind NAT** | Yes | No | No | No |
| **Docker Required** | Yes | Yes | Yes | No |
| **Best For** | Quick setup, behind firewall | VPS with public IP | Multi-service production | No Docker env |

---

## 📊 File Structure Summary

```
pmg-cloud/
│
├── 📚 Documentation
│   ├── QUICKSTART.md              ⭐ Start here
│   ├── DEPLOYMENT.md              📖 Full guide
│   ├── SCRIPTS.md                 🛠️ Scripts reference
│   ├── PRODUCTION-CHECKLIST.md    ✅ Readiness checklist
│   ├── DEPLOYMENT-SUMMARY.md      📦 This file
│   └── README.md                  📄 Project overview
│
├── 🚀 Deployment Scripts
│   ├── deploy-production.sh       Auto deployment
│   └── install-systemd.sh         Systemd installation
│
├── 🛠️ Management Scripts
│   ├── manage.sh                  Service management
│   ├── monitor.sh                 Health monitoring
│   └── setup-cron.sh              Cron jobs setup
│
├── 🔧 Infrastructure Scripts
│   └── setup-nginx.sh             Nginx reverse proxy
│
├── 🐳 Docker Configs
│   ├── docker-compose.yml         Cloudflare Tunnel
│   ├── docker-compose.production.yml  Direct TLS
│   └── Dockerfile                 Image definition
│
├── ⚙️ Configuration Files
│   ├── .env.production            Environment variables
│   ├── nginx-pmg-cloud.conf       Nginx config
│   └── pmg-cloud.service          Systemd unit
│
└── 📁 Data Directories (runtime)
    ├── data/                      Events + config
    ├── certs/                     TLS certificates
    └── backups/                   Automated backups
```

---

## 🔑 Key Features

### Security
- ✅ TLS/SSL encryption
- ✅ API key authentication
- ✅ Firewall configuration
- ✅ Security headers (via nginx)
- ✅ Rate limiting (optional)
- ✅ Credentials management

### Reliability
- ✅ Automated backups (daily)
- ✅ Health monitoring (every 5min)
- ✅ Auto-restart on failure
- ✅ Backup retention (7 days)
- ✅ Disaster recovery procedures

### Observability
- ✅ Health endpoint (/healthz)
- ✅ Comprehensive logging
- ✅ Resource monitoring
- ✅ Alerting (email + webhook)
- ✅ Detailed reports

### Automation
- ✅ One-command deployment
- ✅ Automated monitoring
- ✅ Scheduled backups
- ✅ Auto cleanup old events
- ✅ Certificate auto-renewal

---

## 🎯 Use Cases

### Startup / Small Team
**Recommended:** Cloudflare Tunnel + Docker

```bash
./deploy-production.sh
# Choose Cloudflare Tunnel option
./setup-cron.sh
```

**Why:**
- Fastest setup
- No cert management
- Free DDoS protection
- Works behind NAT

---

### Enterprise / Regulated Environment
**Recommended:** Direct TLS + Nginx + Systemd monitoring

```bash
sudo ./setup-nginx.sh
docker compose -f docker-compose.production.yml up -d
sudo ./setup-cron.sh
```

**Why:**
- Full control
- No external dependencies
- Production-grade
- Audit compliance

---

### Development / Testing
**Recommended:** Dev mode (from README.md)

```bash
go run . --insecure --addr=:8443 --http-addr=:8080 --data-dir=./data
```

**Why:**
- No setup needed
- Fast iteration
- No TLS overhead

---

## 🆘 Quick Troubleshooting

| Symptom | Command | Documentation |
|---------|---------|---------------|
| Service won't start | `./manage.sh status` | DEPLOYMENT.md § Troubleshooting |
| Agents can't connect | `telnet host 8443` | QUICKSTART.md § Troubleshooting |
| Dashboard 502 | `docker compose logs` | SCRIPTS.md § Troubleshooting |
| Disk full | `./manage.sh clean 7` | DEPLOYMENT.md § Maintenance |
| High memory | `docker stats` | DEPLOYMENT.md § Performance |
| Certificate error | `ls -la certs/` | QUICKSTART.md § TLS Setup |

**Full Troubleshooting:** See DEPLOYMENT.md § Troubleshooting Common Issues

---

## 📞 Support Resources

### Documentation
- **Quick Start:** QUICKSTART.md
- **Full Guide:** DEPLOYMENT.md
- **Scripts Ref:** SCRIPTS.md
- **Checklist:** PRODUCTION-CHECKLIST.md

### Project Links
- **PMG Agent:** https://github.com/safedep/pmg
- **Issues:** [Repository issues page]
- **Dashboard Help:** Settings → Help (in-app)

### Community
- GitHub Discussions
- Issue tracker

---

## 🔄 Version History

### v1.0 (2026-06-08)
**Initial Release**

**Package Contents:**
- 5 documentation files
- 6 deployment/management scripts
- 4 configuration templates
- Production readiness checklist

**Deployment Methods:**
- Cloudflare Tunnel
- Direct TLS
- Nginx Reverse Proxy
- Systemd Service

**Features:**
- Automated deployment
- Health monitoring
- Automated backups
- Alert notifications
- Comprehensive documentation

---

## ✅ Package Verification

Verify package integrity:

```bash
# Check all documentation files
ls -1 *.md
# Should list:
# - QUICKSTART.md
# - DEPLOYMENT.md
# - SCRIPTS.md
# - PRODUCTION-CHECKLIST.md
# - DEPLOYMENT-SUMMARY.md
# - README.md

# Check all scripts
ls -1 *.sh
# Should list:
# - deploy-production.sh
# - manage.sh
# - monitor.sh
# - setup-cron.sh
# - setup-nginx.sh
# - install-systemd.sh

# Check configurations
ls -1 *.yml *.conf *.service
# Should list:
# - docker-compose.yml
# - docker-compose.production.yml
# - nginx-pmg-cloud.conf
# - pmg-cloud.service

# Verify script permissions
ls -l *.sh
# All should have execute permission (x)
```

---

## 📅 Maintenance Schedule

### Daily (Automated via Cron)
- Health monitoring (every 5min)
- Backups (2:00 AM)

### Weekly
- Review logs
- Check disk space
- Verify agent connectivity

### Monthly
- Generate detailed report
- Review security
- Update system packages

### Quarterly
- Full checklist review (PRODUCTION-CHECKLIST.md)
- Security audit
- Performance optimization

---

## 🎓 Learning Path

**For DevOps Engineers:**
```
1. QUICKSTART.md → Deploy first instance
2. SCRIPTS.md → Learn management commands
3. DEPLOYMENT.md → Deep dive into production setup
4. PRODUCTION-CHECKLIST.md → Master production readiness
```

**For Security Teams:**
```
1. PRODUCTION-CHECKLIST.md § Security
2. DEPLOYMENT.md § Security Best Practices
3. nginx-pmg-cloud.conf → Review security headers
4. Review firewall configurations
```

**For SysAdmins:**
```
1. QUICKSTART.md → Quick deployment
2. manage.sh --help → Daily operations
3. monitor.sh → Monitoring setup
4. SCRIPTS.md → Automation reference
```

---

## 📦 Package Checksum

**Package Contents:** 19 files  
**Total Scripts:** 6  
**Documentation Files:** 6  
**Configuration Files:** 4  
**Service Files:** 1  
**Docker Files:** 2

**Created:** 2026-06-08  
**Version:** 1.0

---

**Ready to deploy?** → Start with **QUICKSTART.md** 🚀

**Need help?** → Check **DEPLOYMENT.md** troubleshooting section 🔧

**Going to production?** → Review **PRODUCTION-CHECKLIST.md** first ✅
