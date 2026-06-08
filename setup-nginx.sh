#!/bin/bash
# Setup Nginx Reverse Proxy cho PMG Cloud

set -e

echo "=== Nginx Reverse Proxy Setup cho PMG Cloud ==="

# Kiểm tra quyền root
if [ "$EUID" -ne 0 ]; then
    echo "ERROR: Script này cần chạy với quyền root"
    echo "Chạy: sudo ./setup-nginx.sh"
    exit 1
fi

# Kiểm tra nginx đã cài chưa
if ! command -v nginx &> /dev/null; then
    echo "Nginx chưa được cài đặt. Cài đặt nginx..."
    apt update
    apt install -y nginx
fi

# Kiểm tra certbot
if ! command -v certbot &> /dev/null; then
    echo "Certbot chưa được cài đặt. Cài đặt certbot..."
    apt install -y certbot python3-certbot-nginx
fi

# Nhập thông tin domain
read -p "Nhập domain name (vd: your-domain.com): " DOMAIN

if [ -z "$DOMAIN" ]; then
    echo "ERROR: Domain name không được để trống"
    exit 1
fi

# Tạo nginx config
NGINX_CONF="/etc/nginx/sites-available/pmg-cloud"

echo "Tạo nginx configuration..."

cat > "$NGINX_CONF" <<EOF
# HTTP to HTTPS redirect
server {
    listen 80;
    listen [::]:80;
    server_name $DOMAIN;

    # Let's Encrypt challenge
    location /.well-known/acme-challenge/ {
        root /var/www/certbot;
    }

    location / {
        return 301 https://\$server_name\$request_uri;
    }
}

# HTTPS - Dashboard
server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name $DOMAIN;

    # SSL configuration (will be added by certbot)
    ssl_certificate /etc/letsencrypt/live/$DOMAIN/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/$DOMAIN/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers HIGH:!aNULL:!MD5;
    ssl_prefer_server_ciphers on;
    ssl_session_cache shared:SSL:10m;
    ssl_session_timeout 10m;

    # Security headers
    add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;
    add_header X-Frame-Options "DENY" always;
    add_header X-Content-Type-Options "nosniff" always;
    add_header X-XSS-Protection "1; mode=block" always;

    # Logging
    access_log /var/log/nginx/pmg-cloud-access.log;
    error_log /var/log/nginx/pmg-cloud-error.log;

    # Client body size
    client_max_body_size 10M;

    # Proxy to dashboard
    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;

        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;

        # WebSocket support
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "upgrade";

        # Timeouts
        proxy_connect_timeout 60s;
        proxy_send_timeout 60s;
        proxy_read_timeout 60s;
    }

    # Health check endpoint
    location /healthz {
        proxy_pass http://127.0.0.1:8080/healthz;
        access_log off;
    }
}

# gRPC endpoint
server {
    listen 8443 ssl http2;
    listen [::]:8443 ssl http2;
    server_name $DOMAIN;

    # SSL configuration
    ssl_certificate /etc/letsencrypt/live/$DOMAIN/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/$DOMAIN/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers HIGH:!aNULL:!MD5;
    ssl_prefer_server_ciphers on;

    # Logging
    access_log /var/log/nginx/pmg-cloud-grpc-access.log;
    error_log /var/log/nginx/pmg-cloud-grpc-error.log;

    # gRPC proxy
    location / {
        grpc_pass grpc://127.0.0.1:8443;

        grpc_set_header Host \$host;
        grpc_set_header X-Real-IP \$remote_addr;
        grpc_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;

        # Timeouts
        grpc_read_timeout 3600s;
        grpc_send_timeout 3600s;
    }
}
EOF

# Enable site
ln -sf "$NGINX_CONF" /etc/nginx/sites-enabled/pmg-cloud

# Test nginx config
echo "Testing nginx configuration..."
nginx -t

if [ $? -ne 0 ]; then
    echo "ERROR: Nginx configuration test failed"
    exit 1
fi

# Tạo directory cho Let's Encrypt
mkdir -p /var/www/certbot

# Reload nginx
echo "Reloading nginx..."
systemctl reload nginx

# Setup SSL certificate
echo ""
echo "Setup SSL certificate với Let's Encrypt..."
read -p "Email cho Let's Encrypt notifications: " EMAIL

if [ -z "$EMAIL" ]; then
    echo "WARNING: Email không được cung cấp, bỏ qua Let's Encrypt setup"
else
    certbot certonly --nginx -d "$DOMAIN" --email "$EMAIL" --agree-tos --non-interactive

    if [ $? -eq 0 ]; then
        echo "✓ SSL certificate obtained successfully"

        # Setup auto-renewal
        echo "Setting up auto-renewal..."
        (crontab -l 2>/dev/null; echo "0 0,12 * * * certbot renew --quiet --deploy-hook 'systemctl reload nginx'") | crontab -

        # Reload nginx với SSL
        systemctl reload nginx

        echo "✓ Auto-renewal configured"
    else
        echo "ERROR: Failed to obtain SSL certificate"
        echo "You can run certbot manually later:"
        echo "  sudo certbot certonly --nginx -d $DOMAIN"
    fi
fi

# Configure firewall
echo ""
echo "Configuring firewall..."

if command -v ufw &> /dev/null; then
    ufw allow 'Nginx Full'
    ufw allow 8443/tcp
    echo "✓ UFW rules added"
fi

# Final test
echo ""
echo "=== Setup Complete ==="
echo ""
echo "Nginx config: $NGINX_CONF"
echo "Dashboard URL: https://$DOMAIN"
echo "gRPC endpoint: $DOMAIN:8443"
echo ""
echo "Test commands:"
echo "  curl https://$DOMAIN/healthz"
echo "  nginx -t"
echo "  systemctl status nginx"
echo ""
echo "View logs:"
echo "  tail -f /var/log/nginx/pmg-cloud-access.log"
echo "  tail -f /var/log/nginx/pmg-cloud-error.log"
echo ""

# Update docker-compose to bind to localhost only
echo "RECOMMENDATION: Update docker-compose.yml to bind to localhost:"
echo "  ports:"
echo "    - \"127.0.0.1:8080:8080\""
echo "    - \"127.0.0.1:8443:8443\""
echo ""
echo "This ensures traffic only goes through nginx."
