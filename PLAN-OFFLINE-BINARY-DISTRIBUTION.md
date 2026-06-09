# PLAN: Offline PMG Agent Distribution System

**Mục tiêu:** Cho phép máy dev không có internet tải PMG binary từ pmg-cloud server

**Ngày:** 2026-06-09

---

## 📋 PHÂN TÍCH HIỆN TẠI

### Cách Hoạt Động Hiện Tại:

```
Agent Installation Flow (CÓ INTERNET):
┌─────────────────────────────────────────────────────────────────┐
│ 1. User click "Deploy Agent" trên dashboard                    │
│ 2. Dashboard generate command:                                 │
│    curl http://pmg-cloud:8080/install.sh | sh -s -- --token=xxx│
│ 3. install.sh script chạy trên máy agent:                      │
│    curl https://raw.githubusercontent.com/am6539/pmg/main/install.sh | sh  │
│ 4. PMG install.sh tải binary từ GitHub releases                │
│ 5. pmg cloud enroll --endpoint http://pmg-cloud:8080           │
└─────────────────────────────────────────────────────────────────┘
```

### Vấn Đề:
- ❌ Bước 3-4: Máy agent **KHÔNG CÓ INTERNET**, không tải được từ GitHub
- ✅ Bước 1-2, 5: OK vì agent kết nối được tới pmg-cloud server

---

## 🎯 GIẢI PHÁP: PMG Binary Mirror System

### Kiến Trúc Mới:

```
┌──────────────────────────────────────────────────────────────────┐
│                     PMG-CLOUD SERVER                             │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │ 1. Update Store (ĐÃ CÓ)                                   │ │
│  │    - Fetch từ GitHub: /api/config/pmg-update/fetch        │ │
│  │    - Lưu binary tại: data/binaries/pmg-{os}-{arch}        │ │
│  │    - Metadata: data/update.json                            │ │
│  └────────────────────────────────────────────────────────────┘ │
│                           ↓                                       │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │ 2. Binary Distribution (CẦN THÊM)                          │ │
│  │    - Endpoint: GET /bin/{os}/{arch}/pmg                    │ │
│  │    - Serve: data/binaries/pmg-{os}-{arch}                 │ │
│  │    - Public access (không cần auth)                        │ │
│  └────────────────────────────────────────────────────────────┘ │
│                           ↓                                       │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │ 3. Install Script Generator (SỬA)                          │ │
│  │    - Endpoint: GET /install.sh                             │ │
│  │    - Script tải binary từ /bin/{os}/{arch}/pmg            │ │
│  │    - Thay vì GitHub                                        │ │
│  └────────────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────────┘
                           ↓
                    (Máy Agent - No Internet)
```

---

## 🔨 IMPLEMENTATION PLAN

### Phase 1: Backend - Binary Distribution Endpoint

**File:** `dashboard/handler.go`

**Thêm vào hàm `Handler()`:**

```go
// GET /bin/{os}/{arch}/pmg — public endpoint, serve PMG binary
mux.HandleFunc("/bin/", func(w http.ResponseWriter, r *http.Request) {
    // Parse: /bin/linux/amd64/pmg → os=linux, arch=amd64
    parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/bin/"), "/")
    if len(parts) < 3 {
        http.Error(w, "invalid path", http.StatusBadRequest)
        return
    }
    
    goos, goarch := parts[0], parts[1]
    
    // Validate os/arch
    validPlatforms := map[string]bool{
        "linux/amd64": true,
        "linux/arm64": true,
        "darwin/amd64": true,
        "darwin/arm64": true,
        "windows/amd64": true,
    }
    if !validPlatforms[goos+"/"+goarch] {
        http.Error(w, "unsupported platform", http.StatusNotFound)
        return
    }
    
    // Get binary path from UpdateStore
    if deps.Updates == nil {
        http.Error(w, "binary distribution not enabled", http.StatusServiceUnavailable)
        return
    }
    
    binPath := deps.Updates.BinaryPath(goos, goarch)
    
    // Check if binary exists
    if _, err := os.Stat(binPath); os.IsNotExist(err) {
        http.Error(w, "binary not available for this platform", http.StatusNotFound)
        return
    }
    
    // Serve binary
    w.Header().Set("Content-Type", "application/octet-stream")
    w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=pmg"))
    http.ServeFile(w, r, binPath)
})
```

**Thêm vào whitelist (không cần auth):**

```go
// Line ~1638: Always pass list
if strings.HasPrefix(r.URL.Path, "/static/") ||
   strings.HasPrefix(r.URL.Path, "/bin/") ||  // ← THÊM DÒNG NÀY
   r.URL.Path == "/login" ||
   r.URL.Path == "/api/auth/login" ||
   r.URL.Path == "/healthz" ||
   r.URL.Path == "/install.sh" ||
   r.URL.Path == "/install.ps1" {
    next.ServeHTTP(w, r)
    return
}
```

---

### Phase 2: Install Script - Point to Local Server

**File:** `dashboard/handler.go`

**Sửa `installScriptTemplate` (Linux/macOS):**

```go
const installScriptTemplate = `#!/bin/sh
set -eu
PMG_SERVER="{{SERVER_URL}}"
PMG_TOKEN="${PMG_TOKEN:-}"

# Parse --token flag
for arg in "$@"; do
  case "$arg" in
    --token=*) PMG_TOKEN="${arg#--token=}" ;;
  esac
done

if [ -z "$PMG_TOKEN" ]; then
  echo "Error: --token=TOKEN is required" >&2
  exit 1
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# THAY ĐỔI: Download PMG binary từ pmg-cloud server
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
echo "Installing PMG from ${PMG_SERVER}..."

# Detect OS and architecture
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

# Install directory
INSTALL_DIR="${HOME}/.local/bin"
mkdir -p "${INSTALL_DIR}"

# Download binary from pmg-cloud server
BINARY_URL="${PMG_SERVER}/bin/${OS}/${ARCH}/pmg"
echo "Downloading PMG binary from ${BINARY_URL}..."

if command -v curl >/dev/null 2>&1; then
  curl -fsSL "${BINARY_URL}" -o "${INSTALL_DIR}/pmg"
elif command -v wget >/dev/null 2>&1; then
  wget -q -O "${INSTALL_DIR}/pmg" "${BINARY_URL}"
else
  echo "Error: curl or wget required" >&2
  exit 1
fi

chmod +x "${INSTALL_DIR}/pmg"

# Add to PATH if not already
case ":${PATH}:" in
  *:${INSTALL_DIR}:*) ;;
  *) 
    echo "export PATH=\"${INSTALL_DIR}:\$PATH\"" >> "${HOME}/.bashrc"
    echo "export PATH=\"${INSTALL_DIR}:\$PATH\"" >> "${HOME}/.zshrc" 2>/dev/null || true
    export PATH="${INSTALL_DIR}:${PATH}"
    ;;
esac

echo "PMG binary installed to ${INSTALL_DIR}/pmg"

# Enroll with server
echo "Enrolling with PMG Cloud..."
"${INSTALL_DIR}/pmg" cloud enroll --endpoint="$PMG_SERVER" --token="$PMG_TOKEN"

# Wire PMG into shell (aliases + shims)
echo "Wiring PMG into your shell..."
"${INSTALL_DIR}/pmg" setup install

echo ""
echo "Done! PMG is installed, enrolled, and active."
echo "Restart your terminal (or run: source ~/.bashrc) for shell integration to take effect."
`
```

**Sửa `installScriptTemplatePS1` (Windows):**

```go
const installScriptTemplatePS1 = `# PMG Windows Install Script
$ErrorActionPreference = 'Stop'
$PMG_SERVER = '{{SERVER_URL}}'
$PMG_TOKEN  = if ($env:PMG_TOKEN) { $env:PMG_TOKEN } else { '' }

foreach ($arg in $args) {
  if ($arg -match '^--token=(.+)$') { $PMG_TOKEN = $Matches[1] }
}

if (-not $PMG_TOKEN) {
  Write-Error '--token=TOKEN is required'; exit 1
}

# Detect architecture
$archRaw = if ([Environment]::Is64BitOperatingSystem) {
  if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
} else { 
  Write-Error 'Only 64-bit Windows is supported'; exit 1 
}

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# THAY ĐỔI: Download PMG binary từ pmg-cloud server
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Write-Host "Installing PMG from ${PMG_SERVER}..."

$dir = "$env:LOCALAPPDATA\pmg"
New-Item -ItemType Directory -Force -Path $dir | Out-Null

$binaryURL = "${PMG_SERVER}/bin/windows/${archRaw}/pmg"
$bin = "$dir\pmg.exe"

Write-Host "Downloading PMG binary from ${binaryURL}..."
Invoke-WebRequest -Uri $binaryURL -OutFile $bin

# Add to PATH (user scope)
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if ($userPath -notlike "*$dir*") {
  [Environment]::SetEnvironmentVariable('Path', "$userPath;$dir", 'User')
  $env:Path += ";$dir"
}

# Enroll with pmg-cloud
Write-Host 'Enrolling with PMG Cloud...'
& $bin cloud enroll --endpoint="$PMG_SERVER" --token="$PMG_TOKEN"

# Wire PMG into shell
Write-Host 'Setting up PMG...'
& $bin setup install

# Refresh PATH
$machinePath = [Environment]::GetEnvironmentVariable('PATH', 'Machine')
$userPath    = [Environment]::GetEnvironmentVariable('PATH', 'User')
$env:PATH    = ($machinePath + ';' + $userPath) -replace ';;+', ';'

Write-Host ''
Write-Host 'Done! PMG is installed, enrolled, and active.'
`
```

---

### Phase 3: Dashboard UI - Fetch Binaries Button

**Vị trí:** Dashboard → Settings → PMG Updates (đã có)

**Đã có chức năng:**
- ✅ "Fetch from GitHub" button → gọi `/api/config/pmg-update/fetch`
- ✅ Upload binary manually
- ✅ Publish version

**Cần thêm:**
- 📝 Hiển thị status của binary distribution
- 📝 Show download URLs cho từng platform
- 📝 Test download links

**UI Example:**

```
┌─────────────────────────────────────────────────────────┐
│ PMG Agent Updates                                       │
├─────────────────────────────────────────────────────────┤
│                                                          │
│ [Fetch Latest from GitHub]  ← Đã có                     │
│                                                          │
│ Available Binaries:                                      │
│  ✓ Linux amd64    v1.2.3   Download: /bin/linux/amd64/pmg│
│  ✓ Linux arm64    v1.2.3   Download: /bin/linux/arm64/pmg│
│  ✓ macOS amd64    v1.2.3   Download: /bin/darwin/amd64/pmg│
│  ✓ macOS arm64    v1.2.3   Download: /bin/darwin/arm64/pmg│
│  ✓ Windows amd64  v1.2.3   Download: /bin/windows/amd64/pmg│
│                                                          │
│ Installation Script:                                     │
│  → /install.sh  (Linux/macOS)                           │
│  → /install.ps1 (Windows)                               │
│                                                          │
└─────────────────────────────────────────────────────────┘
```

---

### Phase 4: Testing & Deployment

**Test Plan:**

1. **Trên pmg-cloud server (có internet):**
   ```bash
   # Fetch binaries từ GitHub
   curl -X POST http://localhost:8080/api/config/pmg-update/fetch \
     -H "Content-Type: application/json" \
     -d '{"repo": "am6539/pmg"}'
   
   # Verify binaries downloaded
   ls -lh data/binaries/
   
   # Test binary endpoints
   curl -I http://localhost:8080/bin/linux/amd64/pmg
   curl -I http://localhost:8080/bin/darwin/arm64/pmg
   ```

2. **Trên máy dev (không internet, chỉ kết nối server):**
   ```bash
   # Test tải binary trực tiếp
   curl -fsSL http://SERVER_IP:8080/bin/linux/amd64/pmg -o /tmp/pmg-test
   chmod +x /tmp/pmg-test
   /tmp/pmg-test --version
   
   # Test install script
   curl -fsSL http://SERVER_IP:8080/install.sh | sh -s -- --token=xxx
   ```

---

## 📝 IMPLEMENTATION CHECKLIST

### Backend Changes:

- [ ] **handler.go**: Add `/bin/{os}/{arch}/pmg` endpoint
- [ ] **handler.go**: Add `/bin/` to auth whitelist
- [ ] **handler.go**: Update `installScriptTemplate` (Linux/macOS)
- [ ] **handler.go**: Update `installScriptTemplatePS1` (Windows)
- [ ] Test binary serving endpoint
- [ ] Test install scripts với binary URLs mới

### Admin Workflow:

- [ ] Dashboard → Settings → PMG Updates
- [ ] Click "Fetch from GitHub"
- [ ] Verify binaries downloaded vào `data/binaries/`
- [ ] Check binary distribution URLs accessible

### Agent Deployment:

- [ ] Dashboard → Agents → Deploy New Agent
- [ ] Copy install command
- [ ] Run trên máy dev (no internet)
- [ ] Verify PMG installed successfully
- [ ] Verify agent enrolled

---

## 🎯 EXPECTED OUTCOME

### Trước (Hiện tại):
```
Agent machine (no internet) → ❌ Cannot download from GitHub → Failed
```

### Sau (Với changes):
```
Agent machine (no internet) → ✅ Download from pmg-cloud server → Success
                               http://server:8080/bin/linux/amd64/pmg
```

---

## 🔐 SECURITY CONSIDERATIONS

1. **Binary integrity:**
   - ✅ Binaries đã được verify bởi UpdateStore (SHA256)
   - ✅ Download từ trusted pmg-cloud server (internal network)

2. **Access control:**
   - ⚠️ `/bin/` endpoint là public (không cần auth)
   - ✅ OK vì: internal network, binaries không chứa secrets
   - ✅ Agent enrollment vẫn cần token

3. **Binary freshness:**
   - 📝 Admin phải manually fetch từ GitHub
   - 📝 Hoặc setup cron job tự động fetch weekly

---

## 📊 EFFORT ESTIMATION

| Phase | Effort | Files Changed |
|-------|--------|---------------|
| Phase 1: Binary endpoint | 30 min | handler.go (1 file) |
| Phase 2: Install scripts | 30 min | handler.go (same file) |
| Phase 3: UI improvements | 15 min | Optional |
| Phase 4: Testing | 30 min | - |
| **Total** | **~2 hours** | **1 main file** |

---

## 🚀 ROLLOUT PLAN

### Step 1: Implement & Test Locally
- Code changes
- Local testing
- Commit

### Step 2: Deploy to Production Server
```bash
git pull
docker compose down
docker compose build
docker compose up -d
```

### Step 3: Fetch Binaries
- Dashboard → Fetch from GitHub
- Verify downloads

### Step 4: Deploy First Agent
- Test on one dev machine
- Verify success

### Step 5: Rollout to All Machines
- Deploy to remaining dev machines
- Document process

---

## ✅ SUCCESS CRITERIA

- [ ] pmg-cloud server có thể serve PMG binaries qua HTTP
- [ ] Install script tải binary từ server thay vì GitHub
- [ ] Máy dev không internet có thể cài PMG thành công
- [ ] Agent enrollment hoạt động bình thường
- [ ] Binary updates có thể fetch từ GitHub khi cần

---

**Status:** ✅ PLAN COMPLETE - Ready for Implementation

**Next Action:** Review plan → Start Phase 1 implementation
