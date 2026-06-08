#!/bin/bash
# PMG Cloud Production Management Script

COMPOSE_FILE="docker-compose.yml"

show_help() {
    echo "PMG Cloud Production Management"
    echo ""
    echo "Usage: ./manage.sh [command]"
    echo ""
    echo "Commands:"
    echo "  start          - Khởi động services"
    echo "  stop           - Dừng services"
    echo "  restart        - Restart services"
    echo "  status         - Kiểm tra trạng thái"
    echo "  logs           - Xem logs (theo dõi real-time)"
    echo "  logs-tail N    - Xem N dòng logs cuối"
    echo "  backup         - Backup dữ liệu"
    echo "  restore FILE   - Restore từ backup"
    echo "  health         - Kiểm tra health endpoint"
    echo "  update         - Update và rebuild image"
    echo "  clean          - Xóa old event files (>30 days)"
    echo ""
}

start_services() {
    echo "Starting PMG Cloud..."
    docker compose -f $COMPOSE_FILE up -d
    echo "Services started!"
}

stop_services() {
    echo "Stopping PMG Cloud..."
    docker compose -f $COMPOSE_FILE down
    echo "Services stopped!"
}

restart_services() {
    echo "Restarting PMG Cloud..."
    docker compose -f $COMPOSE_FILE restart
    echo "Services restarted!"
}

show_status() {
    echo "=== Service Status ==="
    docker compose -f $COMPOSE_FILE ps
    echo ""
    echo "=== Container Stats ==="
    docker stats --no-stream $(docker compose -f $COMPOSE_FILE ps -q)
}

show_logs() {
    if [ -n "$2" ]; then
        docker compose -f $COMPOSE_FILE logs --tail="$2"
    else
        docker compose -f $COMPOSE_FILE logs -f
    fi
}

backup_data() {
    BACKUP_DIR="backups"
    TIMESTAMP=$(date +%Y%m%d_%H%M%S)
    BACKUP_FILE="$BACKUP_DIR/pmg-cloud-backup-$TIMESTAMP.tar.gz"

    mkdir -p $BACKUP_DIR

    echo "Creating backup: $BACKUP_FILE"
    tar -czf $BACKUP_FILE data/

    echo "Backup created successfully!"
    echo "Size: $(du -h $BACKUP_FILE | cut -f1)"

    # Giữ lại 7 bản backup gần nhất
    ls -t $BACKUP_DIR/pmg-cloud-backup-*.tar.gz | tail -n +8 | xargs -r rm
    echo "Old backups cleaned (keeping last 7)"
}

restore_data() {
    if [ -z "$2" ]; then
        echo "ERROR: Backup file không được chỉ định"
        echo "Usage: ./manage.sh restore <backup-file>"
        exit 1
    fi

    if [ ! -f "$2" ]; then
        echo "ERROR: File $2 không tồn tại"
        exit 1
    fi

    echo "WARNING: Restore sẽ ghi đè dữ liệu hiện tại!"
    read -p "Tiếp tục? (yes/no): " confirm

    if [ "$confirm" != "yes" ]; then
        echo "Restore cancelled"
        exit 0
    fi

    echo "Stopping services..."
    docker compose -f $COMPOSE_FILE down

    echo "Restoring from $2..."
    tar -xzf "$2"

    echo "Starting services..."
    docker compose -f $COMPOSE_FILE up -d

    echo "Restore completed!"
}

check_health() {
    echo "Checking health endpoint..."
    curl -s http://localhost:8080/healthz | jq '.'

    if [ $? -eq 0 ]; then
        echo ""
        echo "✓ Service is healthy"
    else
        echo ""
        echo "✗ Service health check failed"
        exit 1
    fi
}

update_service() {
    echo "Pulling latest changes..."
    git pull

    echo "Backing up current data..."
    backup_data

    echo "Rebuilding image..."
    docker compose -f $COMPOSE_FILE build --no-cache

    echo "Restarting services..."
    docker compose -f $COMPOSE_FILE up -d

    echo "Update completed!"
}

clean_old_events() {
    RETENTION_DAYS=${1:-30}
    echo "Cleaning event files older than $RETENTION_DAYS days..."

    find data/ -name "events-*.jsonl" -mtime +$RETENTION_DAYS -delete

    echo "Cleanup completed!"
}

# Main command handler
case "$1" in
    start)
        start_services
        ;;
    stop)
        stop_services
        ;;
    restart)
        restart_services
        ;;
    status)
        show_status
        ;;
    logs)
        show_logs "$@"
        ;;
    logs-tail)
        show_logs "$@"
        ;;
    backup)
        backup_data
        ;;
    restore)
        restore_data "$@"
        ;;
    health)
        check_health
        ;;
    update)
        update_service
        ;;
    clean)
        clean_old_events "$2"
        ;;
    help|--help|-h)
        show_help
        ;;
    *)
        echo "ERROR: Unknown command '$1'"
        echo ""
        show_help
        exit 1
        ;;
esac
