#!/bin/bash
set -e

echo "=== PMG Cloud Production Deployment ==="

# Kiểm tra Docker
if ! command -v docker &> /dev/null; then
    echo "ERROR: Docker chưa được cài đặt"
    exit 1
fi

if ! command -v docker compose &> /dev/null; then
    echo "ERROR: Docker Compose chưa được cài đặt"
    exit 1
fi

# Tạo thư mục cần thiết
mkdir -p data certs

# Kiểm tra TLS certificates (Option A - TLS trực tiếp)
if [ ! -f "certs/tls.crt" ] || [ ! -f "certs/tls.key" ]; then
    echo "WARNING: TLS certificates không tồn tại trong certs/"
    echo "Chọn phương thức tạo certificate:"
    echo "1. Self-signed (testing only)"
    echo "2. Let's Encrypt (production)"
    echo "3. Skip (sử dụng Cloudflare Tunnel)"
    read -p "Chọn option (1/2/3): " cert_option

    case $cert_option in
        1)
            read -p "Nhập domain name: " domain
            openssl req -x509 -newkey rsa:4096 \
                -keyout certs/tls.key -out certs/tls.crt \
                -days 365 -nodes -subj "/CN=$domain"
            echo "Self-signed certificate đã được tạo"
            ;;
        2)
            echo "Chạy certbot để lấy certificate từ Let's Encrypt:"
            read -p "Nhập domain name: " domain
            echo "sudo certbot certonly --standalone -d $domain"
            echo "Sau đó copy cert:"
            echo "sudo cp /etc/letsencrypt/live/$domain/fullchain.pem certs/tls.crt"
            echo "sudo cp /etc/letsencrypt/live/$domain/privkey.pem certs/tls.key"
            exit 0
            ;;
        3)
            echo "Bỏ qua TLS certificate - sử dụng Cloudflare Tunnel"
            ;;
    esac
fi

# Load environment variables
if [ -f ".env.production" ]; then
    echo "Loading .env.production..."
    export $(cat .env.production | grep -v '^#' | xargs)
else
    echo "WARNING: .env.production không tồn tại"
    echo "Tạo file .env.production với nội dung:"
    echo "PMG_CLOUD_API_KEYS=your-api-key"
    echo "PMG_CLOUD_DASH_USER=admin"
    echo "PMG_CLOUD_DASH_PASS=your-password"
    exit 1
fi

# Chọn docker-compose file
echo ""
echo "Chọn cấu hình triển khai:"
echo "1. TLS trực tiếp (docker-compose.production.yml)"
echo "2. Cloudflare Tunnel (docker-compose.yml)"
read -p "Chọn option (1/2): " deploy_option

case $deploy_option in
    1)
        COMPOSE_FILE="docker-compose.production.yml"
        ;;
    2)
        COMPOSE_FILE="docker-compose.yml"
        ;;
    *)
        echo "Invalid option"
        exit 1
        ;;
esac

# Build và start services
echo ""
echo "Building Docker image..."
docker compose -f $COMPOSE_FILE build

echo ""
echo "Starting services..."
docker compose -f $COMPOSE_FILE up -d

echo ""
echo "=== Deployment Complete ==="
echo ""
echo "Services:"
docker compose -f $COMPOSE_FILE ps

echo ""
echo "Logs:"
echo "docker compose -f $COMPOSE_FILE logs -f"

echo ""
echo "Dashboard:"
if [ "$deploy_option" == "1" ]; then
    echo "  http://localhost:8080"
    echo "  gRPC: localhost:8443"
else
    echo "  https://your-domain.com"
fi

echo ""
echo "Default login: $PMG_CLOUD_DASH_USER / [password from .env.production]"
