# Hướng Dẫn Setup Binary Distribution Cho Máy Không Internet

**Status:** ✅ Code đã được implement và push lên GitHub

**Commit:** 942bb69

---

## 🎯 Vấn Đề Đã Giải Quyết

Máy dev của công ty không có internet, chỉ kết nối được tới pmg-cloud server.  
→ Không thể tải PMG binary từ GitHub như trước.

**Giải pháp:** pmg-cloud server làm binary mirror.

---

## 📋 SETUP TRÊN SERVER (Admin - 1 lần)

### Bước 1: Deploy Code Mới

SSH vào pmg-cloud server và update:

```bash
cd ~/pmg-cloud
git pull
docker compose down
docker compose build
docker compose up -d
```

Verify service running:
```bash
docker compose ps
curl http://localhost:8080/healthz
```

---

### Bước 2: Fetch PMG Binaries Từ GitHub

**Option A: Qua Dashboard (Recommended)**

1. Mở browser: `http://SERVER_IP:8080`
2. Login với admin credentials
3. Vào **Settings** → **PMG Updates**
4. Click button **[Fetch from GitHub]**
5. Đợi download hoàn tất (vài phút)

**Option B: Qua API**

```bash
curl -X POST http://localhost:8080/api/config/pmg-update/fetch \
  -H "Content-Type: application/json" \
  -H "Cookie: pmg_session=YOUR_SESSION_COOKIE" \
  -d '{"repo": "am6539/pmg"}'
```

---

### Bước 3: Verify Binaries Downloaded

```bash
ls -lh data/binaries/

# Expected output:
# pmg-linux-amd64
# pmg-linux-arm64
# pmg-darwin-amd64
# pmg-darwin-arm64
# pmg-windows-amd64.exe
```

Verify metadata:
```bash
cat data/update.json
```

---

### Bước 4: Test Binary Distribution Endpoint

```bash
# Test từ server
curl -I http://localhost:8080/bin/linux/amd64/pmg
# Expected: HTTP/1.1 200 OK

curl -I http://localhost:8080/bin/windows/amd64/pmg
# Expected: HTTP/1.1 200 OK

# Test download actual binary
curl -fsSL http://localhost:8080/bin/linux/amd64/pmg -o /tmp/pmg-test
chmod +x /tmp/pmg-test
/tmp/pmg-test --version
```

---

## 🖥️ DEPLOY AGENT TRÊN MÁY DEV (Không Internet)

### Prerequisite

- Máy dev có thể kết nối tới pmg-cloud server qua HTTP/HTTPS
- Máy dev có `curl` hoặc `wget`

---

### Cách 1: Dashboard Wizard (Recommended)

1. Admin mở dashboard → **Agents** → **+ Deploy New Agent**
2. Chọn OS và Architecture
3. Configure enrollment token (label, group, expiry)
4. Copy command được generate, ví dụ:
   ```bash
   curl -sSfL http://192.168.1.100:8080/install.sh | sh -s -- --token=pmgenroll_xxxxx
   ```
5. Paste command vào terminal của máy dev
6. Enter → Script sẽ tự động:
   - Detect OS/architecture
   - Download binary từ `http://SERVER:8080/bin/linux/amd64/pmg`
   - Install vào `~/.local/bin/pmg`
   - Enroll với pmg-cloud
   - Setup shell integration

---

### Cách 2: Manual

Trên máy dev:

```bash
# Set variables
SERVER_URL="http://192.168.1.100:8080"
TOKEN="pmgenroll_xxxxxxxxxx"

# Download and run install script
curl -sSfL ${SERVER_URL}/install.sh | sh -s -- --token=${TOKEN}
```

**Windows (PowerShell):**
```powershell
$SERVER_URL = "http://192.168.1.100:8080"
$env:PMG_TOKEN = "pmgenroll_xxxxxxxxxx"

Invoke-WebRequest -Uri "${SERVER_URL}/install.ps1" -UseBasicParsing | Invoke-Expression
```

---

### Verify Installation

```bash
# Check PMG installed
pmg --version

# Check enrolled
pmg cloud status

# Test package scan
npm install express
# Should see event in dashboard
```

---

## 🔧 TROUBLESHOOTING

### Issue: Binary không tải được (404)

**Symptom:**
```
curl http://SERVER:8080/bin/linux/amd64/pmg
404 Not Found: binary not available for linux/amd64
```

**Fix:**
```bash
# Check binaries directory
ls -la ~/pmg-cloud/data/binaries/

# Re-fetch from GitHub
# Dashboard → Settings → PMG Updates → [Fetch from GitHub]
```

---

### Issue: "binary distribution not enabled"

**Symptom:**
```
503 Service Unavailable: binary distribution not enabled on this server
```

**Fix:**

UpdateStore chưa được init. Check `main.go`:

```bash
# Verify UpdateStore is initialized
docker compose logs | grep -i update

# Rebuild with updates enabled
docker compose down
docker compose build
docker compose up -d
```

---

### Issue: Install script fails on agent

**Symptom:**
```
Failed to download PMG binary from server: Connection refused
```

**Fix:**

1. Verify network connectivity:
   ```bash
   # From agent machine
   ping SERVER_IP
   curl http://SERVER_IP:8080/healthz
   ```

2. Check firewall:
   ```bash
   # On server
   sudo ufw allow 8080/tcp
   sudo ufw status
   ```

3. Verify service running:
   ```bash
   # On server
   docker compose ps
   docker compose logs
   ```

---

### Issue: Unsupported platform

**Symptom:**
```
unsupported platform: linux/armv7
```

**Supported platforms:**
- ✅ `linux/amd64`
- ✅ `linux/arm64`
- ✅ `darwin/amd64` (Intel Mac)
- ✅ `darwin/arm64` (M1/M2 Mac)
- ✅ `windows/amd64`

**Unsupported:**
- ❌ `linux/armv7`, `linux/386`, `windows/arm64`

---

## 🔄 UPDATE BINARIES (Khi Có Version Mới)

### Khi Nào Cần Update?

- PMG có release mới
- Fix security vulnerabilities
- Thêm features mới

### Cách Update:

1. **Fetch binaries mới:**
   ```bash
   # Dashboard → Settings → PMG Updates → [Fetch from GitHub]
   ```

2. **Verify version:**
   ```bash
   cat data/update.json | grep version
   ls -lh data/binaries/
   ```

3. **Agents sẽ tự động update** (nếu auto-update enabled)
   - Hoặc manual update trên agent:
     ```bash
     pmg self update
     ```

---

## 📊 MONITORING

### Check Binary Distribution Status

**Dashboard:**
- Settings → PMG Updates
- Show available binaries
- Show download URLs

**CLI:**
```bash
# List available binaries
ls -lh data/binaries/

# Check metadata
cat data/update.json

# View download logs
docker compose logs | grep "/bin/"
```

---

### Check Agent Installations

**Dashboard:**
- Agents → View agent list
- Check Last Seen timestamp
- Verify PMG version

**Server logs:**
```bash
# View enrollment events
docker compose logs | grep enroll

# View binary downloads
docker compose logs | grep "GET /bin/"
```

---

## 🔐 SECURITY NOTES

1. **Binary Integrity:**
   - ✅ Binaries fetched từ GitHub releases (trusted source)
   - ✅ SHA256 checksums stored in `update.json`
   - ✅ Server là trusted internal network

2. **Access Control:**
   - `/bin/` endpoint là **public** (no authentication)
   - ⚠️ OK vì: internal network only
   - Agent enrollment vẫn cần token

3. **Network Security:**
   - pmg-cloud server nên chỉ accessible từ internal network
   - Không expose port 8080 ra internet public

---

## ✅ SUCCESS CRITERIA

- [x] Code implemented và compiled
- [x] Pushed to GitHub
- [ ] Deployed to production server
- [ ] Binaries fetched from GitHub
- [ ] Binary endpoints tested
- [ ] First agent deployed successfully
- [ ] Agent can install without internet
- [ ] Agent enrolled và reporting events

---

## 📚 REFERENCE

**Documentation:**
- Full plan: `PLAN-OFFLINE-BINARY-DISTRIBUTION.md`
- Code changes: `dashboard/handler.go`

**Endpoints:**
- Binary distribution: `GET /bin/{os}/{arch}/pmg`
- Install script: `GET /install.sh`
- PowerShell script: `GET /install.ps1`
- Fetch binaries: `POST /api/config/pmg-update/fetch`

**Binary paths:**
- Linux amd64: `data/binaries/pmg-linux-amd64`
- Linux arm64: `data/binaries/pmg-linux-arm64`
- macOS amd64: `data/binaries/pmg-darwin-amd64`
- macOS arm64: `data/binaries/pmg-darwin-arm64`
- Windows: `data/binaries/pmg-windows-amd64.exe`

---

**Date:** 2026-06-09  
**Version:** 1.0  
**Status:** ✅ Ready for deployment
