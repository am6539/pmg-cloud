# Hướng Dẫn Triển Khai PMG Cloud Production

## Tổng Quan Kiến Trúc

```
PMG Agent (máy client)
    │  gRPC (TLS)
    ▼
pmg-cloud Server
    ├── Port 8443: gRPC (agents kết nối)
    ├── Port 8080: HTTP Dashboard (web UI)
    └── Data persisted trong ./data/
```

## Yêu Cầu Hệ Thống

- Docker 24.0+
- Docker Compose 2.0+
- Minimum 2GB RAM
- Minimum 10GB disk space
- Domain name (nếu dùng TLS trực tiếp)
- hoặc Cloudflare Tunnel (config hiện tại)

## Các Bước Triển Khai

### Bước 1: Clone Repository và Chuẩn Bị

```bash
git clone <repo-url>
cd pmg-cloud

# Tạo các thư mục cần thiết
mkdir -p data certs backups

# Set permissions
chmod +x deploy-production.sh manage.sh
```

### Bước 2: Cấu Hình Môi Trường

Tạo file `.env.production`:

```bash
cp .env.production.example .env.production
nano .env.production
```

Nội dung file `.env.production`:

```bash
# API Keys - THAY ĐỔI GIÁ TRỊ NÀY!
PMG_CLOUD_API_KEYS=your-random-strong-api-key-here

# Dashboard Admin - THAY ĐỔI PASSWORD!
PMG_CLOUD_DASH_USER=admin
PMG_CLOUD_DASH_PASS=YourStrongPassword123!

# Optional: Data retention
PMG_CLOUD_RETENTION_DAYS=30

# Optional: Malware feed refresh
PMG_CLOUD_MALWARE_REFRESH_INTERVAL=6h
```

**Lưu ý bảo mật:**
- Generate API key mạnh: `openssl rand -hex 32`
- Sử dụng password phức tạp cho dashboard
- Không commit file `.env.production` vào git

### Bước 3: Chọn Phương Thức TLS

#### Option A: Cloudflare Tunnel (Đơn Giản - Khuyến Nghị)

**Ưu điểm:**
- Không cần certificate
- Tự động HTTPS
- DDoS protection
- Không cần mở port trên firewall

**Cấu hình:**

1. Cài đặt Cloudflare Tunnel:
```bash
# Download cloudflared
wget https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64.deb
sudo dpkg -i cloudflared-linux-amd64.deb

# Authenticate
cloudflared tunnel login

# Tạo tunnel
cloudflared tunnel create pmg-cloud

# Configure tunnel
cat > ~/.cloudflared/config.yml <<EOF
tunnel: <tunnel-id>
credentials-file: /root/.cloudflared/<tunnel-id>.json

ingress:
  - hostname: your-domain.com
    service: http://localhost:8080
  - service: http_status:404
EOF

# Install service
cloudflared service install
```

2. Cập nhật DNS trong Cloudflare dashboard:
   - Thêm CNAME record: `package` → `<tunnel-id>.cfargotunnel.com`

3. Deploy pmg-cloud:
```bash
docker compose up -d
```

#### Option B: TLS Trực Tiếp (Self-Signed hoặc Let's Encrypt)

**Self-Signed (Testing Only):**

```bash
openssl req -x509 -newkey rsa:4096 \
  -keyout certs/tls.key -out certs/tls.crt \
  -days 365 -nodes \
  -subj "/CN=your-domain.com"
```

**Let's Encrypt (Production):**

```bash
# Cài certbot
sudo apt update
sudo apt install certbot

# Lấy certificate (port 80 phải available)
sudo certbot certonly --standalone -d your-domain.com

# Copy certificates
sudo cp /etc/letsencrypt/live/your-domain.com/fullchain.pem certs/tls.crt
sudo cp /etc/letsencrypt/live/your-domain.com/privkey.pem certs/tls.key

# Set permissions
sudo chown $USER:$USER certs/tls.*
```

**Deploy:**

```bash
docker compose -f docker-compose.production.yml up -d
```

### Bước 4: Khởi Động Services

#### Sử dụng script tự động:

```bash
./deploy-production.sh
```

#### Hoặc thủ công:

```bash
# Load environment
export $(cat .env.production | grep -v '^#' | xargs)

# Build và start
docker compose up -d

# Kiểm tra logs
docker compose logs -f
```

### Bước 5: Xác Minh Triển Khai

```bash
# Check service status
./manage.sh status

# Check health endpoint
./manage.sh health

# View logs
./manage.sh logs
```

**Truy cập dashboard:**
- Cloudflare Tunnel: `https://your-domain.com`
- TLS trực tiếp: `http://your-server:8080`

**Login:**
- Username: `admin` (hoặc từ .env.production)
- Password: từ `.env.production`

### Bước 6: Cấu Hình Agents

Sau khi dashboard đã chạy, triển khai agents:

#### Cách 1: Sử dụng Dashboard Wizard (Khuyến Nghị)

1. Đăng nhập dashboard → **Agents** → **+ Deploy New Agent**
2. Chọn OS và architecture
3. Cấu hình token (label, group, expiry, max uses)
4. Copy command và chạy trên máy agent:

```bash
curl -sSfL http://your-server:8080/install.sh | sh -s -- --token=pmgenroll_xxx
```

#### Cách 2: Manual Configuration

Trên máy agent, cài PMG và config:

```bash
# Install PMG
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

# Test connection
pmg cloud sync
```

## Quản Lý Production

### Các Lệnh Thường Dùng

```bash
# Khởi động/dừng services
./manage.sh start
./manage.sh stop
./manage.sh restart

# Xem logs
./manage.sh logs              # Real-time logs
./manage.sh logs-tail 100     # Last 100 lines

# Kiểm tra health
./manage.sh health

# Backup dữ liệu
./manage.sh backup

# Restore từ backup
./manage.sh restore backups/pmg-cloud-backup-20260608_070000.tar.gz

# Update version
./manage.sh update

# Clean old events
./manage.sh clean 30          # Xóa files > 30 ngày
```

### Backup Strategy

**Tự động backup (Crontab):**

```bash
# Edit crontab
crontab -e

# Thêm dòng này để backup hàng ngày lúc 2am
0 2 * * * cd /path/to/pmg-cloud && ./manage.sh backup
```

**Manual backup:**

```bash
./manage.sh backup
```

Backups được lưu trong `backups/` với format:
```
pmg-cloud-backup-YYYYMMDD_HHMMSS.tar.gz
```

Script tự động giữ lại 7 bản backup gần nhất.

### Monitoring

**Health Check:**

```bash
curl -s http://localhost:8080/healthz | jq '.'
```

Response:
```json
{
  "ok": true,
  "uptime": "3h22m10s",
  "components": {
    "data_dir": { "status": "ok" },
    "malware_feed": { 
      "status": "ok", 
      "detail": "npm=1423 pypi=876 entries" 
    }
  }
}
```

**Metrics trong Dashboard:**
- Overview → KPIs (endpoints, sessions, packages, malware detected)
- Events → Event logs với filters
- Endpoints → Agent status (online/offline, last seen)
- Packages → Risk leaderboard

### Xử Lý Sự Cố

**Service không khởi động:**

```bash
# Check logs
./manage.sh logs

# Check docker status
docker compose ps

# Rebuild if needed
docker compose build --no-cache
docker compose up -d
```

**Agents không kết nối được:**

```bash
# Verify network connectivity
telnet your-server 8443

# Check gRPC endpoint
grpcurl -plaintext your-server:8443 list

# Check API key
# Dashboard → Groups → Verify API key exists
```

**Dashboard không accessible:**

```bash
# Check firewall
sudo ufw status
sudo ufw allow 8080/tcp

# Check Cloudflare Tunnel status
cloudflared tunnel info pmg-cloud
systemctl status cloudflared
```

**Disk space đầy:**

```bash
# Clean old events
./manage.sh clean 7           # Giữ lại 7 ngày gần nhất

# Check disk usage
du -sh data/
df -h
```

## Security Best Practices

1. **Thay đổi credentials mặc định:**
   - Đổi API keys trong `.env.production`
   - Đổi dashboard password ngay sau first login

2. **Firewall configuration:**
```bash
# Chỉ mở ports cần thiết
sudo ufw allow 8080/tcp   # Dashboard (hoặc qua Cloudflare Tunnel)
sudo ufw allow 8443/tcp   # gRPC
sudo ufw enable
```

3. **Regular updates:**
```bash
# Update pmg-cloud
./manage.sh update

# Update system packages
sudo apt update && sudo apt upgrade
```

4. **Backup encryption:**
```bash
# Encrypt backups
gpg --symmetric --cipher-algo AES256 backups/pmg-cloud-backup-latest.tar.gz
```

5. **Rotate API keys định kỳ:**
   - Dashboard → Groups → Add new key
   - Update agents với key mới
   - Revoke old key sau khi migrate

## Performance Tuning

### Data Retention

Điều chỉnh retention trong `.env.production`:

```bash
PMG_CLOUD_RETENTION_DAYS=30    # Giữ 30 ngày
# hoặc
PMG_CLOUD_RETENTION_DAYS=0     # Không tự động xóa
```

### Malware Feed Refresh

```bash
PMG_CLOUD_MALWARE_REFRESH_INTERVAL=6h    # 6 giờ
# hoặc
PMG_CLOUD_MALWARE_REFRESH_INTERVAL=0     # Disable auto-refresh
```

### Docker Resource Limits

Thêm vào `docker-compose.yml`:

```yaml
services:
  pmg-cloud:
    # ... existing config ...
    deploy:
      resources:
        limits:
          cpus: '2.0'
          memory: 2G
        reservations:
          cpus: '1.0'
          memory: 1G
```

## CI/CD Integration

### GitHub Actions Example

```yaml
name: PMG Scan

on: [push, pull_request]

jobs:
  scan:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      
      - name: Install PMG
        run: |
          curl -sSfL https://raw.githubusercontent.com/safedep/pmg/main/install.sh | sh
          echo "$HOME/.local/bin" >> $GITHUB_PATH
      
      - name: Configure PMG
        env:
          PMG_API_KEY: ${{ secrets.PMG_API_KEY }}
        run: |
          pmg cloud enroll \
            --endpoint https://your-domain.com:443 \
            --token pmgenroll_xxx
      
      - name: Run dependency scan
        run: npm install
      
      - name: Sync events
        if: always()
        run: pmg cloud sync
```

## Troubleshooting Common Issues

| Vấn đề | Nguyên nhân | Giải pháp |
|--------|-------------|-----------|
| Port 8080 không accessible | Firewall block | `sudo ufw allow 8080/tcp` |
| gRPC connection refused | TLS certificate sai | Verify cert paths, regenerate if needed |
| "invalid API key" | Key không match | Check `.env.production` và dashboard Groups |
| Disk full | Events cũ không được xóa | Chạy `./manage.sh clean` |
| Cloudflare Tunnel down | Service stopped | `systemctl restart cloudflared` |

## Support và Tài Liệu

- **Dashboard built-in docs:** Settings → Help
- **PMG docs:** https://github.com/safedep/pmg
- **Logs location:** `docker compose logs -f`
- **Data directory:** `./data/`
