# PMG Cloud - Production Readiness Checklist

Checklist này giúp đảm bảo pmg-cloud được triển khai an toàn và đầy đủ trên production.

**Date:** 2026-06-08  
**Version:** 1.0

---

## 🔐 Security Checklist

### Credentials và Secrets

- [ ] **API Keys đã được thay đổi** từ giá trị mặc định
  ```bash
  # Verify: Không chứa "changeme"
  grep -i changeme .env.production
  # Kết quả: không có output = OK
  ```

- [ ] **Dashboard password đã được đổi** khỏi "admin"
  ```bash
  # Generate strong password
  openssl rand -base64 16
  ```

- [ ] **Credentials không bị commit** vào git
  ```bash
  # Verify .env.production trong .gitignore
  cat .gitignore | grep .env
  ```

- [ ] **API keys được generate ngẫu nhiên**
  ```bash
  # Minimum 32 bytes hex
  openssl rand -hex 32
  ```

- [ ] **Credentials được backup** an toàn offline
  ```bash
  # Copy credentials.txt ra khỏi server
  # Store trong password manager hoặc encrypted storage
  ```

### TLS/SSL

- [ ] **TLS certificates hợp lệ** và chưa hết hạn
  ```bash
  # Check expiry
  openssl x509 -in certs/tls.crt -noout -enddate
  
  # Should be > 30 days from now
  ```

- [ ] **Private key được protect**
  ```bash
  # Verify permissions
  ls -la certs/tls.key
  # Should be 600 or 400
  chmod 600 certs/tls.key
  ```

- [ ] **Auto-renewal đã được setup** (nếu dùng Let's Encrypt)
  ```bash
  # Check cron
  crontab -l | grep certbot
  # hoặc
  cat /etc/cron.d/pmg-cloud | grep certbot
  ```

### Firewall

- [ ] **Firewall rules đã được cấu hình**
  ```bash
  # Check UFW
  sudo ufw status
  
  # Should allow:
  # - 8080/tcp (dashboard)
  # - 8443/tcp (gRPC)
  # - 22/tcp (SSH)
  ```

- [ ] **Chỉ expose ports cần thiết**
  ```bash
  # List listening ports
  sudo netstat -tlnp | grep -E ':(8080|8443|22)'
  ```

- [ ] **Rate limiting đã được enable** (nếu dùng nginx)
  ```bash
  # Check nginx config
  grep limit_req /etc/nginx/sites-available/pmg-cloud
  ```

### Access Control

- [ ] **SSH key-based authentication**
  ```bash
  # Disable password auth
  sudo grep -i PasswordAuthentication /etc/ssh/sshd_config
  # Should be: PasswordAuthentication no
  ```

- [ ] **Root login disabled**
  ```bash
  sudo grep -i PermitRootLogin /etc/ssh/sshd_config
  # Should be: PermitRootLogin no
  ```

- [ ] **Fail2ban hoặc similar tools** đã cài (optional but recommended)
  ```bash
  sudo systemctl status fail2ban
  ```

---

## 🏗️ Infrastructure Checklist

### Server Resources

- [ ] **RAM đủ**: Minimum 2GB
  ```bash
  free -h
  # Available >= 2GB
  ```

- [ ] **Disk space đủ**: Minimum 10GB free
  ```bash
  df -h
  # /opt/pmg-cloud hoặc ./ có >= 10GB free
  ```

- [ ] **CPU adequate**: 2+ cores recommended
  ```bash
  nproc
  # >= 2
  ```

### Docker (nếu dùng)

- [ ] **Docker installed và running**
  ```bash
  docker --version
  docker compose version
  sudo systemctl status docker
  ```

- [ ] **Docker auto-start enabled**
  ```bash
  sudo systemctl is-enabled docker
  # Should be: enabled
  ```

- [ ] **Docker log rotation configured**
  ```bash
  cat /etc/docker/daemon.json
  # Should have log-driver config
  ```

### Network

- [ ] **Domain DNS đã được cấu hình** (nếu có domain)
  ```bash
  # Test DNS resolution
  nslookup package.yourdomain.com
  dig package.yourdomain.com
  ```

- [ ] **Port forwarding configured** (nếu behind router)
  ```bash
  # Test external connectivity
  # Từ máy khác:
  telnet your-public-ip 8443
  ```

- [ ] **Cloudflare Tunnel running** (nếu dùng)
  ```bash
  sudo systemctl status cloudflared
  cloudflared tunnel list
  ```

---

## 📊 Deployment Checklist

### Pre-Deployment

- [ ] **.env.production file đã được tạo**
  ```bash
  test -f .env.production && echo "OK" || echo "MISSING"
  ```

- [ ] **Data directories exist**
  ```bash
  ls -ld data/ certs/ backups/
  ```

- [ ] **Scripts có execute permission**
  ```bash
  ls -l *.sh
  # All should have x permission
  ```

### Deployment

- [ ] **Service đã được start**
  ```bash
  ./manage.sh status
  # hoặc
  docker compose ps
  # hoặc
  sudo systemctl status pmg-cloud
  ```

- [ ] **Health endpoint responds**
  ```bash
  curl -s http://localhost:8080/healthz | jq
  # Should return: {"ok": true, ...}
  ```

- [ ] **Dashboard accessible**
  ```bash
  curl -I http://localhost:8080/
  # Should return: HTTP/1.1 200 OK
  ```

- [ ] **gRPC port listening**
  ```bash
  sudo netstat -tlnp | grep 8443
  ```

- [ ] **Logs không có errors**
  ```bash
  ./manage.sh logs | grep -i error
  # Should be empty or only expected errors
  ```

### Post-Deployment

- [ ] **First login successful**
  - Truy cập dashboard
  - Login với admin credentials
  - Verify dashboard loads

- [ ] **Password changed from default**
  - Dashboard → Settings → Change Password

- [ ] **API key groups created**
  - Dashboard → Groups → Create group
  - Generate API keys

- [ ] **Test agent enrollment**
  ```bash
  # On test machine
  pmg cloud enroll --endpoint http://your-server:8080 --token test-token
  ```

---

## 🔄 Monitoring & Maintenance Checklist

### Monitoring Setup

- [ ] **Monitoring script installed**
  ```bash
  test -x monitor.sh && echo "OK" || echo "MISSING"
  ```

- [ ] **Cron jobs configured**
  ```bash
  crontab -l | grep pmg-cloud
  # hoặc
  cat /etc/cron.d/pmg-cloud
  ```

- [ ] **Alert destinations configured**
  ```bash
  # Check environment
  echo $ALERT_EMAIL
  echo $ALERT_WEBHOOK
  ```

- [ ] **Test monitoring**
  ```bash
  ./monitor.sh
  # Should complete without errors
  ```

- [ ] **Test alert sending** (optional)
  ```bash
  # Temporarily break something to trigger alert
  docker compose stop
  ./monitor.sh
  # Check if alert received
  docker compose start
  ```

### Backup Setup

- [ ] **Backup script tested**
  ```bash
  ./manage.sh backup
  ls -lh backups/
  ```

- [ ] **Automated backups configured**
  ```bash
  # Check cron
  crontab -l | grep backup
  ```

- [ ] **Backup retention working**
  ```bash
  # Should keep only last 7 backups
  ls backups/ | wc -l
  ```

- [ ] **Restore procedure tested**
  ```bash
  # Test restore to verify backups are valid
  ./manage.sh restore backups/<latest-backup>.tar.gz
  ```

- [ ] **Off-site backup setup** (recommended)
  ```bash
  # Copy backups to remote location
  # Example: rsync, S3, etc.
  ```

### Log Management

- [ ] **Logrotate configured**
  ```bash
  # For systemd
  cat /etc/logrotate.d/pmg-cloud
  
  # For Docker
  cat /etc/docker/daemon.json | grep log
  ```

- [ ] **Logs accessible**
  ```bash
  ./manage.sh logs
  # Should show recent logs
  ```

- [ ] **Log retention policy set**
  ```bash
  # Check --retention-days flag or env var
  docker compose exec pmg-cloud /app/pmg-cloud --help | grep retention
  ```

---

## 🧪 Testing Checklist

### Functional Testing

- [ ] **Dashboard login works**
  - Login with admin credentials
  - Logout
  - Login again

- [ ] **Agent can connect**
  ```bash
  # From agent machine
  pmg cloud enroll --endpoint https://your-domain --token <token>
  pmg cloud sync
  ```

- [ ] **Events are received**
  - Trigger package install on agent
  - Check dashboard Events page
  - Verify event appears

- [ ] **Event files created**
  ```bash
  ls -lh data/events-*.jsonl
  # Should have today's file
  ```

- [ ] **Malware feed working**
  - Dashboard → Malware Feed
  - Check status and last update
  - Manual refresh if needed

### Performance Testing

- [ ] **Response time acceptable**
  ```bash
  # Measure response time
  time curl -s http://localhost:8080/healthz > /dev/null
  # Should be < 1 second
  ```

- [ ] **Resource usage normal**
  ```bash
  docker stats --no-stream
  # Memory should be < 1GB
  # CPU should be < 50% idle
  ```

- [ ] **Disk I/O acceptable**
  ```bash
  iostat -x 1 5
  # Watch %util column
  ```

### Stress Testing (optional)

- [ ] **Multiple concurrent agents**
  - Connect 10+ agents simultaneously
  - Monitor resource usage
  - Check for errors

- [ ] **Large event volume**
  - Generate many events quickly
  - Verify all events recorded
  - Check performance impact

---

## 📋 Documentation Checklist

### Internal Docs

- [ ] **Production credentials documented** (securely)
  - API keys location
  - Dashboard admin credentials
  - SSH keys
  - TLS certificate locations

- [ ] **Runbook created** với:
  - Deployment procedures
  - Troubleshooting steps
  - Emergency contacts
  - Rollback procedures

- [ ] **Architecture diagram** (optional)
  - Network topology
  - Service dependencies
  - Data flow

### Team Knowledge

- [ ] **Team trained** on:
  - Dashboard usage
  - Basic troubleshooting
  - Escalation procedures

- [ ] **On-call rotation setup** (nếu cần)
  - Contact information
  - Escalation procedures
  - SLA defined

---

## 🚨 Disaster Recovery Checklist

### Backup Verification

- [ ] **Backup restore tested**
  ```bash
  # Test restore on staging/test environment
  ./manage.sh restore backups/<backup-file>
  ```

- [ ] **Backup integrity checked**
  ```bash
  # Verify tar archive
  tar -tzf backups/<backup-file> > /dev/null
  echo $?  # Should be 0
  ```

- [ ] **Recovery time objective (RTO) documented**
  - Expected time to restore service
  - Maximum acceptable downtime

- [ ] **Recovery point objective (RPO) documented**
  - Maximum acceptable data loss
  - Backup frequency meets RPO

### Incident Response

- [ ] **Incident response plan documented**
  - Who to contact
  - Communication channels
  - Escalation procedures

- [ ] **Emergency procedures documented**
  - Service restart: `./manage.sh restart`
  - Rollback: restore from backup
  - Contact vendor/support

- [ ] **Contact information accessible**
  - Team contacts
  - Vendor support
  - Cloud provider support

---

## ✅ Sign-Off

### Pre-Production Sign-Off

- [ ] **Security review completed**
  - Reviewer: ________________
  - Date: ________________
  - Issues resolved: [ ]

- [ ] **Infrastructure review completed**
  - Reviewer: ________________
  - Date: ________________
  - Capacity verified: [ ]

- [ ] **Functional testing passed**
  - Tester: ________________
  - Date: ________________
  - All tests passed: [ ]

### Go-Live Approval

- [ ] **All checklist items completed**
- [ ] **Stakeholders notified**
- [ ] **Monitoring active**
- [ ] **On-call scheduled**

**Approved by:** ________________  
**Date:** ________________  
**Signature:** ________________

---

## 📝 Notes Section

Additional notes, exceptions, or special considerations:

```
[Empty - add your notes here]
```

---

## 🔄 Post-Launch Checklist (First 24h)

- [ ] **Hour 1:** Monitor logs continuously
- [ ] **Hour 2:** Check resource usage
- [ ] **Hour 4:** Verify first agents connecting
- [ ] **Hour 8:** Review event volume
- [ ] **Hour 24:** Full health check
- [ ] **Week 1:** Review monitoring data
- [ ] **Month 1:** Review backup retention

---

**Checklist Version:** 1.0  
**Last Updated:** 2026-06-08  
**Next Review:** [Set reminder for quarterly review]
