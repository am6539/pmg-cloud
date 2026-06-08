#!/bin/bash
# Setup Cron Jobs cho PMG Cloud

set -e

echo "=== PMG Cloud Cron Jobs Setup ==="

INSTALL_DIR="/opt/pmg-cloud"
SCRIPT_DIR="$(pwd)"

# Kiểm tra quyền root
if [ "$EUID" -ne 0 ]; then
    echo "WARNING: Chạy với sudo để cài đặt system-wide cron jobs"
    echo "Hoặc tiếp tục để cài cron jobs cho user hiện tại"
    read -p "Tiếp tục? (y/n): " confirm
    if [ "$confirm" != "y" ]; then
        exit 1
    fi
fi

# Make scripts executable
chmod +x monitor.sh manage.sh deploy-production.sh

# Create cron job file
CRON_FILE="/tmp/pmg-cloud-cron"

cat > "$CRON_FILE" <<'EOF'
# PMG Cloud Automated Tasks

# Monitoring - chạy mỗi 5 phút
*/5 * * * * cd /opt/pmg-cloud && ./monitor.sh >> /var/log/pmg-cloud-monitor.log 2>&1

# Daily backup - 2:00 AM
0 2 * * * cd /opt/pmg-cloud && ./manage.sh backup >> /var/log/pmg-cloud-backup.log 2>&1

# Weekly cleanup old events - Sunday 3:00 AM
0 3 * * 0 cd /opt/pmg-cloud && ./manage.sh clean 30 >> /var/log/pmg-cloud-cleanup.log 2>&1

# Monthly report - 1st day of month at 1:00 AM
0 1 1 * * cd /opt/pmg-cloud && ./monitor.sh --report >> /var/log/pmg-cloud-report.log 2>&1

# Health check every minute
* * * * * curl -s http://localhost:8080/healthz > /dev/null || echo "$(date): Health check failed" >> /var/log/pmg-cloud-health.log

# Check disk space hourly
0 * * * * df -h /opt/pmg-cloud/data | tail -1 | awk '{if(int($5)>90) print "$(date): Disk usage high: "$5}' >> /var/log/pmg-cloud-disk.log

EOF

# Install cron jobs
if [ "$EUID" -eq 0 ]; then
    # System-wide installation
    cp "$CRON_FILE" /etc/cron.d/pmg-cloud
    chmod 644 /etc/cron.d/pmg-cloud

    # Create log directory
    mkdir -p /var/log
    touch /var/log/pmg-cloud-monitor.log
    touch /var/log/pmg-cloud-backup.log
    touch /var/log/pmg-cloud-cleanup.log
    touch /var/log/pmg-cloud-report.log
    touch /var/log/pmg-cloud-health.log
    touch /var/log/pmg-cloud-disk.log

    # Set permissions
    chown pmg:pmg /var/log/pmg-cloud-*.log 2>/dev/null || true

    echo "✓ System-wide cron jobs installed: /etc/cron.d/pmg-cloud"
else
    # User-level installation
    (crontab -l 2>/dev/null; cat "$CRON_FILE") | crontab -
    echo "✓ User-level cron jobs installed"
fi

rm "$CRON_FILE"

echo ""
echo "=== Cron Jobs Installed ==="
echo ""
echo "Scheduled tasks:"
echo "  - Monitoring:     Every 5 minutes"
echo "  - Daily backup:   2:00 AM"
echo "  - Weekly cleanup: Sunday 3:00 AM"
echo "  - Monthly report: 1st of month 1:00 AM"
echo "  - Health check:   Every minute"
echo "  - Disk check:     Every hour"
echo ""
echo "View cron jobs:"
if [ "$EUID" -eq 0 ]; then
    echo "  cat /etc/cron.d/pmg-cloud"
else
    echo "  crontab -l"
fi
echo ""
echo "View logs:"
echo "  tail -f /var/log/pmg-cloud-monitor.log"
echo "  tail -f /var/log/pmg-cloud-backup.log"
echo ""
echo "Remove cron jobs:"
if [ "$EUID" -eq 0 ]; then
    echo "  sudo rm /etc/cron.d/pmg-cloud"
else
    echo "  crontab -e  # then delete the PMG Cloud lines"
fi
