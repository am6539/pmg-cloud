# Nginx Reverse Proxy Setup for PMG Cloud

## Architecture

```
Agent (remote) 
  ↓ HTTPS
Nginx (reverse proxy, TLS termination)
  ↓ HTTP/1.1 h2c upgrade
PMG Cloud Container (8080)
```

## Setup Steps

### 1. Update docker-compose.yml

The container exposes:
- **Port 8080**: HTTP/1.1 with h2c mux (HTTP/2 over unencrypted connection)
  - Serves both dashboard AND gRPC traffic
  - Bind to `127.0.0.1:8080` so it's only accessible from localhost

Do NOT expose port 8443 or any gRPC port externally. Nginx handles all external traffic.

### 2. Nginx Configuration

Update `/etc/nginx/conf.d/vgpmg.ovp.vn.conf`:

```nginx
server {
    server_name vgpmg.ovp.vn;

    location / {
        proxy_pass http://127.0.0.1:8080;

        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # CRITICAL: Allow HTTP/2 upgrades for gRPC
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";

        # Timeouts for long-lived connections (gRPC streams)
        proxy_connect_timeout 60s;
        proxy_send_timeout 300s;
        proxy_read_timeout 300s;
    }

    listen 443 ssl http2;  # Enable HTTP/2
    ssl_certificate /etc/letsencrypt/live/vgpmg.ovp.vn/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/vgpmg.ovp.vn/privkey.pem;
    include /etc/letsencrypt/options-ssl-nginx.conf;
    ssl_dhparam /etc/letsencrypt/ssl-dhparams.pem;
}

server {
    if ($host = vgpmg.ovp.vn) {
        return 301 https://$host$request_uri;
    }

    listen 80;
    server_name vgpmg.ovp.vn;
    return 404;
}
```

Key settings:
- `listen 443 ssl http2;` — Enable HTTP/2 so gRPC works
- `proxy_http_version 1.1;` — Keep connection to backend as HTTP/1.1
- `Upgrade: $http_upgrade` + `Connection: upgrade` — Required for h2c
- `proxy_read_timeout 300s;` — gRPC streams can be long-lived

### 3. Verify SSL/TLS

```bash
# Check certificate validity
certbot certificates

# Test SSL
curl -v https://vgpmg.ovp.vn/
```

### 4. Configure PMG Cloud

In `docker-compose.yml`, set `--grpc-public-addr`:

```yaml
command: ["--addr=", "--data-dir=/data", "--http-addr=:8080", "--grpc-public-addr=vgpmg.ovp.vn:443"]
```

This tells enrolling agents:
- Connect to `vgpmg.ovp.vn:443` (your public domain)
- Use TLS (via Nginx)
- The backend will accept gRPC traffic

### 5. Reload and Test

```bash
# Reload Nginx
sudo nginx -t && sudo systemctl reload nginx

# Restart PMG Cloud container
docker-compose down
docker-compose up -d

# Watch logs
docker-compose logs -f pmg-cloud

# Check enrollment data
docker exec pmg-cloud_pmg-cloud_1 cat /data/enrollment.json
```

### 6. Enroll an Agent

Use the enrollment command with the public domain:

```bash
pmg cloud enroll --endpoint=https://vgpmg.ovp.vn --token=pmgenroll_XXXXX
```

## Troubleshooting

### Agents not appearing in dashboard

1. **Check enrollment file**:
   ```bash
   docker exec pmg-cloud_pmg-cloud_1 cat /data/enrollment.json
   ```
   Should show `"agents": [...]` with your enrolled agents.

2. **Check logs for heartbeat errors**:
   ```bash
   docker-compose logs pmg-cloud | grep -i "heartbeat\|enroll\|error"
   ```

3. **Verify gRPC public address**:
   ```bash
   docker exec pmg-cloud_pmg-cloud_1 curl http://127.0.0.1:8080/api/config
   ```
   Look for `grpc_public_addr` in the response.

4. **Test gRPC connectivity**:
   ```bash
   # From agent machine
   curl -v --http2 https://vgpmg.ovp.vn/
   ```

### Timeouts or 502 errors

- Increase Nginx `proxy_read_timeout` to 300s+
- Ensure Nginx has `listen 443 ssl http2;`
- Check that gRPC streams aren't being buffered by Nginx

## Security Notes

- Container binds to `127.0.0.1:8080` only — not exposed externally
- Nginx terminates TLS and forwards cleartext HTTP/1.1 internally
- All gRPC traffic goes through Nginx's SSL/TLS layer
- Agents connect to the public domain with valid certificates
