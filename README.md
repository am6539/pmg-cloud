# pmg-cloud

Self-hosted backend for [PMG (Package Manager Guard)](https://github.com/safedep/pmg).  
Receives audit events from PMG agents via gRPC, stores them as JSONL, and serves a web dashboard.

## Architecture

```
PMG agent (npm/pip install)
    │  gRPC SyncEvents
    ▼
pmg-cloud (gRPC :8443 + HTTP dashboard :8080)
    │  JSONL files
    ▼
data/events-YYYYMMDD.jsonl
```

## Quick Start (local / dev)

**Requirements:** Go 1.21+

```bash
git clone <repo>
cd pmg-cloud

# Run without TLS (dev only)
go run . \
  --insecure \
  --addr=:8443 \
  --api-keys=your-secret-key \
  --data-dir=./data
```

Dashboard opens at `http://localhost:8080`.

### WSL2 users (Windows browser)

The server binds IPv4 explicitly so `http://127.0.0.1:8080` should work.  
If the browser can't connect, add a Windows Firewall rule once (Admin PowerShell):

```powershell
New-NetFirewallRule -DisplayName "PMG Cloud Dashboard" -Direction Inbound -Protocol TCP -LocalPort 8080 -Action Allow
```

If `127.0.0.1` still fails, use the WSL2 IP directly:

```bash
# In WSL — get IP
ip addr show eth0 | grep "inet " | awk '{print $2}' | cut -d/ -f1
```

Then open `http://<that-ip>:8080`.

## Production (Docker + TLS)

**1. Generate or obtain a TLS certificate**

```bash
mkdir -p certs
# Self-signed (testing only):
openssl req -x509 -newkey rsa:4096 -keyout certs/tls.key -out certs/tls.crt \
  -days 365 -nodes -subj "/CN=your-domain.com"
```

**2. Set your API key**

```bash
export PMG_CLOUD_API_KEYS=your-secret-key
```

**3. Start with Docker Compose**

```bash
docker compose up -d
```

This starts pmg-cloud on port 8443 (gRPC/TLS) and 8080 (HTTP dashboard).  
Data is persisted in `./data/`.

### docker-compose.yml ports

| Port | Purpose |
|------|---------|
| 8443 | gRPC (TLS) — PMG agents connect here |
| 8080 | HTTP dashboard — browser access |

## Configuring PMG to use this backend

In your PMG config (`~/.pmg/config.yml` or `config.yml`):

```yaml
cloud:
  addr: "your-server:8443"       # gRPC address
  api_key: "your-secret-key"     # must match --api-keys
  tenant_id: "your-org"          # any identifier
  insecure: false                # set true only for local dev (no TLS)
```

For local dev (insecure):

```yaml
cloud:
  addr: "localhost:8443"
  api_key: "your-secret-key"
  tenant_id: "dev"
  insecure: true
```

## CLI flags

| Flag | Default | Description |
|------|---------|-------------|
| `--addr` | `:8443` | gRPC listen address |
| `--http-addr` | `:8080` | Dashboard HTTP address (empty to disable) |
| `--data-dir` | `data` | Directory for JSONL event storage |
| `--api-keys` | _(none)_ | Comma-separated API keys (or `PMG_CLOUD_API_KEYS` env) |
| `--tls-cert` | _(required)_ | TLS certificate PEM file |
| `--tls-key` | _(required)_ | TLS private key PEM file |
| `--insecure` | `false` | Disable TLS — plaintext gRPC, dev only |

## Dashboard pages

| Page | Description |
|------|-------------|
| Dashboard | KPI summary: endpoints, sessions, packages analyzed, malicious/blocked |
| Events | Full event log with filters (event type, period) |
| Endpoints | All PMG agents that have reported in |
| Malicious Packages | Events where `is_malware=true` |
| Policy Violations | Events where action is `BLOCKED` or `COOLDOWN_BLOCKED` |
| Vulnerabilities | N/A — PMG tracks supply-chain malware, not CVEs |

## Data format

Events are appended to `data/events-YYYYMMDD.jsonl` (one JSON object per line, rotated daily).  
Key fields: `event_type`, `package_name`, `ecosystem`, `action`, `is_malware`, `endpoint_id`, `tenant_id`, `received_at`.

## Notes

- Module path `github.com/yourorg/pmg-cloud` in `go.mod` is a placeholder — rename to your actual org before publishing.
- `certs/` and `data/` are gitignored.
- The dashboard uses a 5-second read cache to avoid repeated JSONL disk reads under load.
