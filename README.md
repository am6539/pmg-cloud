# pmg-cloud

Self-hosted backend for [PMG (Package Manager Guard)](https://github.com/safedep/pmg).  
Receives audit events from PMG agents via gRPC, stores them as rotated JSONL files, and serves a web dashboard.

## Architecture

```
PMG agent (npm / pip / etc.)
    │  gRPC SyncEvents
    ▼
pmg-cloud  (:8443 gRPC  +  :8080 HTTP dashboard)
    │
    ├── data/events-YYYYMMDD.jsonl   ← daily-rotated event log
    ├── data/groups.json             ← group → API key mappings
    ├── data/enrollment.json         ← enrollment tokens + registered agents
    ├── data/users.json              ← dashboard user accounts
    ├── data/config.json             ← webhook / retention settings
    └── data/aikido-mirror/          ← offline malware feed cache
```

## Quick Start (dev, no TLS)

**Requirements:** Go 1.21+

```bash
git clone <repo>
cd pmg-cloud

go run . \
  --insecure \
  --addr=:8443 \
  --http-addr=:8080 \
  --data-dir=./data
```

Dashboard: `http://localhost:8080`  
Default login: **admin / admin** (prompted to change password on first login).

### WSL2 (Windows browser)

The server binds IPv4 explicitly so `http://127.0.0.1:8080` works.  
If the browser cannot connect, add a firewall rule once (Admin PowerShell):

```powershell
New-NetFirewallRule -DisplayName "PMG Cloud Dashboard" -Direction Inbound -Protocol TCP -LocalPort 8080 -Action Allow
```

If `127.0.0.1` still fails, use the WSL2 IP:

```bash
ip addr show eth0 | grep "inet " | awk '{print $2}' | cut -d/ -f1
# then open http://<that-ip>:8080
```

## Production (Docker + TLS)

**1. Generate a TLS certificate**

```bash
mkdir -p certs
# Self-signed (testing only):
openssl req -x509 -newkey rsa:4096 -keyout certs/tls.key -out certs/tls.crt \
  -days 365 -nodes -subj "/CN=your-domain.com"
```

**2. Configure**

```bash
export PMG_CLOUD_API_KEYS=changeme      # fallback static key (optional if using groups)
export PMG_CLOUD_DASH_USER=admin
export PMG_CLOUD_DASH_PASS=your-pass
```

**3. Start**

```bash
docker compose up -d
```

| Port | Purpose |
|------|---------|
| 8443 | gRPC (TLS) — PMG agents connect here |
| 8080 | HTTP dashboard — browser access |

Data is persisted in `./data/` (Docker volume mount).

## Deploying agents (recommended)

Use the **Deploy New Agent** wizard in the dashboard for the easiest setup:

1. Open the dashboard → **Agents** → **+ Deploy New Agent**
2. Select OS (Linux / macOS) and architecture
3. Configure token settings (label, group, expiry, max uses)
4. Copy the generated one-liner and run it on the target machine:
   ```bash
   curl -sSfL http://your-server:8080/install.sh | sh -s -- --token=pmgenroll_xxx
   ```

The script installs PMG, enrolls the machine, and wires PMG into the shell automatically. Restart the terminal to activate.

The agent appears in the **Agents** table once it has enrolled.

### Interactive enrollment (PMG already installed)

```bash
pmg cloud enroll
# Prompts for server address and token interactively
```

Or pass flags for scripted use:

```bash
pmg cloud enroll --endpoint http://your-server:8080 --token pmgenroll_xxx
```

## Air-gapped agents (SafeDep relay)

pmg-cloud acts as a relay for SafeDep malware analysis, so agents on machines with no direct internet access can still check packages:

```yaml
# ~/.pmg/config.yml on the agent machine
malysis:
  addr: "your-server:8443"              # pmg-cloud gRPC address
  insecure: false                        # true if running --insecure
aikido_intel:
  base_url: "http://your-server:8080"   # pmg-cloud Aikido mirror
```

pmg-cloud forwards `QueryPackageAnalysis` requests to SafeDep and caches responses for 1 hour. Agents need no outbound internet access at all.

## Configuring PMG manually

Edit `~/.pmg/config.yml`:

```yaml
cloud:
  enabled: true
  addr: "your-server:8443"
  api_key: "your-api-key"      # from dashboard Groups page
  insecure: false               # true only when server runs --insecure
```

Equivalent env vars (useful in CI):

```bash
PMG_CLOUD_ENABLED=true
PMG_CLOUD_ADDR=your-server:8443
PMG_CLOUD_API_KEY=your-api-key
PMG_CLOUD_INSECURE=false
PMG_CLOUD_ENDPOINT_ID=my-machine    # optional stable identifier
```

## CI/CD integration

PMG auto-detects GitHub Actions and GitLab CI and attaches repository / branch / commit metadata to events. Set these env vars in your pipeline:

```bash
PMG_CLOUD_ENABLED=true
PMG_CLOUD_ADDR=your-server:8443
PMG_CLOUD_API_KEY=${{ secrets.PMG_API_KEY }}
PMG_CLOUD_INSECURE=true               # remove if TLS is configured
PMG_CLOUD_ENDPOINT_ID=github-actions/${{ github.repository }}
PMG_CLOUD_AUTO_SYNC_ENABLED=false     # ephemeral runners — flush manually at job end
```

Add a final step to flush events:

```yaml
- name: Sync PMG events
  if: always()
  run: pmg cloud sync
```

Full snippets (GitHub Actions + GitLab CI) are also available in the dashboard under **CI / CD → Show guide**.

## Dashboard pages

| Page | Description |
|------|-------------|
| Overview | KPI summary: endpoints, sessions, packages analyzed, malicious/blocked |
| Events | Full event log with filters (type, ecosystem, period, action) |
| Endpoints | All registered agents — online/offline status, IP address, last seen |
| Packages | Top packages, risk leaderboard, ecosystem breakdown |
| CI / CD | Per-repository and per-branch pipeline stats + setup guide |
| Malware Feed | Aikido malware intelligence mirror — status and manual refresh |
| Agents | Enrollment tokens, registered agents, group assignment |
| Groups | API key groups — create groups, add/revoke keys |
| Audit Log | Dashboard admin actions (user changes, key operations, config edits) |
| Settings | Webhook URL, data retention days, dashboard preferences |

## CLI flags

| Flag | Default | Env override | Description |
|------|---------|-------------|-------------|
| `--addr` | `:8443` | — | gRPC listen address |
| `--http-addr` | `:8080` | — | Dashboard HTTP address (empty to disable) |
| `--data-dir` | `data` | — | Directory for all persistent storage |
| `--tls-cert` | _(required)_ | — | TLS certificate PEM |
| `--tls-key` | _(required)_ | — | TLS private key PEM |
| `--insecure` | `false` | — | Disable TLS — dev only |
| `--api-keys` | _(none)_ | `PMG_CLOUD_API_KEYS` | Comma-separated static API keys (fallback when no groups exist) |
| `--dash-user` | _(none)_ | `PMG_CLOUD_DASH_USER` | Bootstrap admin username |
| `--dash-pass` | _(none)_ | `PMG_CLOUD_DASH_PASS` | Bootstrap admin password |
| `--retention-days` | `30` | — | Delete event files older than N days (0 = disabled) |
| `--malware-refresh-interval` | `6h` | — | Auto-refresh interval for Aikido malware feed (0 = disabled) |

## Auth model

| Layer | Mechanism |
|-------|-----------|
| PMG agent → gRPC | API key in `authorization` metadata. Resolved via group store first, then static `--api-keys` list. |
| Browser → dashboard | Session cookie (`pmg_session`, 8 h TTL). Login rate-limited to 10 attempts/min per IP. |
| Dashboard users | Stored in `data/users.json`. Roles: `admin` (full access) and `viewer` (read-only). |

## Health check

`GET /healthz` — unauthenticated. Suitable for Docker `HEALTHCHECK` and load balancer probes.

```json
{
  "ok": true,
  "uptime": "3h22m10s",
  "components": {
    "data_dir":     { "status": "ok" },
    "malware_feed": { "status": "ok", "detail": "npm=1423 pypi=876 entries" }
  }
}
```

## Data format

Events are appended to `data/events-YYYYMMDD.jsonl` (one JSON per line, rotated daily).  
Key fields: `received_at`, `endpoint_id`, `remote_ip`, `event_type`, `package_name`, `ecosystem`, `action`, `is_malware`, `group_id`, `ci_repository`, `ci_branch`, `ci_provider`.

## Notes

- The module path `github.com/yourorg/pmg-cloud` in `go.mod` is a placeholder — rename to your actual org before publishing.
- `certs/` and `data/` are gitignored.
- Dashboard read cache: 5-second TTL to avoid repeated JSONL disk reads under load.
- The `aikido-mirror/` subdirectory can be safely deleted and will be rebuilt on the next refresh.
