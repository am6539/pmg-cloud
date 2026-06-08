#!/bin/bash
# PMG Cloud Monitoring Script với Alerting

set -e

# Configuration
HEALTH_ENDPOINT="http://localhost:8080/healthz"
ALERT_EMAIL="${ALERT_EMAIL:-admin@example.com}"
ALERT_WEBHOOK="${ALERT_WEBHOOK:-}"
LOG_FILE="/var/log/pmg-cloud-monitor.log"
STATUS_FILE="/tmp/pmg-cloud-status"

# Thresholds
MAX_RESPONSE_TIME=5000  # milliseconds
MIN_DISK_SPACE=1000     # MB
MAX_MEMORY_PERCENT=90
MAX_CPU_PERCENT=90

log() {
    echo "[$(date +'%Y-%m-%d %H:%M:%S')] $1" | tee -a "$LOG_FILE"
}

send_alert() {
    local severity=$1
    local message=$2

    log "ALERT [$severity]: $message"

    # Email alert (nếu có mailx/sendmail)
    if command -v mail &> /dev/null && [ -n "$ALERT_EMAIL" ]; then
        echo "$message" | mail -s "PMG Cloud Alert [$severity]" "$ALERT_EMAIL"
    fi

    # Webhook alert (Slack, Discord, etc.)
    if [ -n "$ALERT_WEBHOOK" ]; then
        curl -X POST "$ALERT_WEBHOOK" \
            -H "Content-Type: application/json" \
            -d "{\"text\":\"🚨 PMG Cloud Alert [$severity]\\n$message\"}" \
            2>/dev/null || true
    fi
}

check_health_endpoint() {
    log "Checking health endpoint..."

    # Measure response time
    start_time=$(date +%s%3N)
    response=$(curl -s -w "\n%{http_code}" --max-time 10 "$HEALTH_ENDPOINT" 2>/dev/null)
    end_time=$(date +%s%3N)
    response_time=$((end_time - start_time))

    http_code=$(echo "$response" | tail -n1)
    body=$(echo "$response" | head -n-1)

    if [ "$http_code" != "200" ]; then
        send_alert "CRITICAL" "Health endpoint returned HTTP $http_code"
        return 1
    fi

    if [ $response_time -gt $MAX_RESPONSE_TIME ]; then
        send_alert "WARNING" "Health endpoint slow: ${response_time}ms (threshold: ${MAX_RESPONSE_TIME}ms)"
    fi

    # Parse response
    if command -v jq &> /dev/null; then
        is_ok=$(echo "$body" | jq -r '.ok')
        if [ "$is_ok" != "true" ]; then
            send_alert "CRITICAL" "Health check failed: $body"
            return 1
        fi

        # Check components
        components=$(echo "$body" | jq -r '.components')
        log "Components status: $components"
    fi

    log "✓ Health check passed (${response_time}ms)"
    return 0
}

check_docker_container() {
    log "Checking Docker container..."

    if ! docker ps | grep -q pmg-cloud; then
        send_alert "CRITICAL" "PMG Cloud container is not running"
        return 1
    fi

    # Check container status
    container_status=$(docker inspect -f '{{.State.Status}}' $(docker ps -qf "name=pmg-cloud") 2>/dev/null)
    if [ "$container_status" != "running" ]; then
        send_alert "CRITICAL" "Container status: $container_status"
        return 1
    fi

    # Check container restart count
    restart_count=$(docker inspect -f '{{.RestartCount}}' $(docker ps -qf "name=pmg-cloud") 2>/dev/null)
    if [ "$restart_count" -gt 5 ]; then
        send_alert "WARNING" "Container has restarted $restart_count times"
    fi

    log "✓ Container is running (restarts: $restart_count)"
    return 0
}

check_disk_space() {
    log "Checking disk space..."

    data_dir="./data"
    if [ ! -d "$data_dir" ]; then
        data_dir="/opt/pmg-cloud/data"
    fi

    available_mb=$(df -m "$data_dir" | tail -1 | awk '{print $4}')
    used_percent=$(df -h "$data_dir" | tail -1 | awk '{print $5}' | sed 's/%//')

    if [ "$available_mb" -lt "$MIN_DISK_SPACE" ]; then
        send_alert "CRITICAL" "Low disk space: ${available_mb}MB available (threshold: ${MIN_DISK_SPACE}MB)"
        return 1
    fi

    if [ "$used_percent" -gt 90 ]; then
        send_alert "WARNING" "Disk usage high: ${used_percent}%"
    fi

    log "✓ Disk space OK: ${available_mb}MB available (${used_percent}% used)"
    return 0
}

check_memory_usage() {
    log "Checking memory usage..."

    container_id=$(docker ps -qf "name=pmg-cloud" 2>/dev/null)
    if [ -z "$container_id" ]; then
        return 0  # Skip if not running in Docker
    fi

    # Get memory stats
    stats=$(docker stats --no-stream --format "{{.MemPerc}}" "$container_id" 2>/dev/null)
    mem_percent=$(echo "$stats" | sed 's/%//')

    if [ -n "$mem_percent" ] && [ "${mem_percent%.*}" -gt "$MAX_MEMORY_PERCENT" ]; then
        send_alert "WARNING" "High memory usage: ${mem_percent}%"
    fi

    log "✓ Memory usage: ${mem_percent}%"
    return 0
}

check_cpu_usage() {
    log "Checking CPU usage..."

    container_id=$(docker ps -qf "name=pmg-cloud" 2>/dev/null)
    if [ -z "$container_id" ]; then
        return 0  # Skip if not running in Docker
    fi

    # Get CPU stats
    stats=$(docker stats --no-stream --format "{{.CPUPerc}}" "$container_id" 2>/dev/null)
    cpu_percent=$(echo "$stats" | sed 's/%//')

    if [ -n "$cpu_percent" ] && [ "${cpu_percent%.*}" -gt "$MAX_CPU_PERCENT" ]; then
        send_alert "WARNING" "High CPU usage: ${cpu_percent}%"
    fi

    log "✓ CPU usage: ${cpu_percent}%"
    return 0
}

check_event_files() {
    log "Checking event files..."

    data_dir="./data"
    if [ ! -d "$data_dir" ]; then
        data_dir="/opt/pmg-cloud/data"
    fi

    # Count event files
    event_count=$(find "$data_dir" -name "events-*.jsonl" 2>/dev/null | wc -l)

    # Check today's event file
    today=$(date +%Y%m%d)
    today_file="$data_dir/events-${today}.jsonl"

    if [ ! -f "$today_file" ]; then
        send_alert "WARNING" "Today's event file not found: $today_file"
    else
        file_size=$(du -h "$today_file" | cut -f1)
        log "✓ Event files: $event_count total, today's file: $file_size"
    fi

    return 0
}

check_port_connectivity() {
    log "Checking port connectivity..."

    # Check gRPC port
    if timeout 5 bash -c "</dev/tcp/localhost/8443" 2>/dev/null; then
        log "✓ gRPC port 8443 accessible"
    else
        send_alert "CRITICAL" "gRPC port 8443 not accessible"
        return 1
    fi

    # Check HTTP port
    if timeout 5 bash -c "</dev/tcp/localhost/8080" 2>/dev/null; then
        log "✓ HTTP port 8080 accessible"
    else
        send_alert "CRITICAL" "HTTP port 8080 not accessible"
        return 1
    fi

    return 0
}

generate_report() {
    log "Generating monitoring report..."

    cat > /tmp/pmg-cloud-report.txt <<EOF
PMG Cloud Monitoring Report
Generated: $(date)

=== Service Status ===
$(docker ps | grep pmg-cloud || systemctl status pmg-cloud 2>/dev/null || echo "Not running")

=== Resource Usage ===
$(docker stats --no-stream $(docker ps -qf "name=pmg-cloud") 2>/dev/null || echo "N/A")

=== Disk Usage ===
$(df -h ./data 2>/dev/null || df -h /opt/pmg-cloud/data 2>/dev/null)

=== Event Files ===
$(find ./data -name "events-*.jsonl" -ls 2>/dev/null || find /opt/pmg-cloud/data -name "events-*.jsonl" -ls 2>/dev/null)

=== Recent Logs ===
$(docker logs --tail 50 $(docker ps -qf "name=pmg-cloud") 2>/dev/null || tail -50 /opt/pmg-cloud/logs/pmg-cloud.log 2>/dev/null)
EOF

    log "Report saved to /tmp/pmg-cloud-report.txt"
}

main() {
    log "=========================================="
    log "Starting PMG Cloud monitoring checks"
    log "=========================================="

    failed_checks=0

    # Run all checks
    check_health_endpoint || ((failed_checks++))
    check_docker_container || ((failed_checks++))
    check_disk_space || ((failed_checks++))
    check_memory_usage || ((failed_checks++))
    check_cpu_usage || ((failed_checks++))
    check_event_files || ((failed_checks++))
    check_port_connectivity || ((failed_checks++))

    # Save status
    echo "last_check=$(date +%s)" > "$STATUS_FILE"
    echo "failed_checks=$failed_checks" >> "$STATUS_FILE"

    if [ $failed_checks -eq 0 ]; then
        log "=========================================="
        log "✓ All checks passed"
        log "=========================================="
    else
        log "=========================================="
        log "✗ $failed_checks check(s) failed"
        log "=========================================="
        generate_report
    fi

    return $failed_checks
}

# Parse arguments
case "${1:-}" in
    --report)
        generate_report
        exit 0
        ;;
    --help|-h)
        echo "PMG Cloud Monitoring Script"
        echo ""
        echo "Usage: $0 [options]"
        echo ""
        echo "Options:"
        echo "  --report    Generate monitoring report"
        echo "  --help      Show this help"
        echo ""
        echo "Environment Variables:"
        echo "  ALERT_EMAIL       Email for alerts (default: admin@example.com)"
        echo "  ALERT_WEBHOOK     Webhook URL for alerts (Slack/Discord)"
        echo ""
        exit 0
        ;;
esac

main
exit $?
