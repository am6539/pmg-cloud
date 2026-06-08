#!/bin/bash
# PMG Cloud - Show Documentation Index

cat << 'EOF'
╔═══════════════════════════════════════════════════════════════╗
║                                                               ║
║            📦 PMG Cloud - Production Deployment              ║
║                  Documentation & Scripts v1.0                 ║
║                                                               ║
╚═══════════════════════════════════════════════════════════════╝

🚀 QUICK START (Choose Your Path):

┌─────────────────────────────────────────────────────────────┐
│ 1. First Time Deployment (10 minutes)                       │
│    → Read: QUICKSTART.md                                    │
│    → Run:  ./deploy-production.sh                           │
│    → Setup: ./setup-cron.sh                                 │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ 2. Production Grade Setup (30 minutes)                      │
│    → Read: DEPLOYMENT.md                                    │
│    → Run:  sudo ./setup-nginx.sh                            │
│    → Verify: PRODUCTION-CHECKLIST.md                        │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ 3. Daily Operations                                         │
│    → Commands: ./manage.sh help                             │
│    → Monitoring: ./monitor.sh                               │
│    → Reference: SCRIPTS.md                                  │
└─────────────────────────────────────────────────────────────┘

═══════════════════════════════════════════════════════════════

📚 DOCUMENTATION INDEX:

⭐ QUICKSTART.md
   Purpose: 10-minute deployment guide
   When: First deployment, need quick setup
   Contents: 4 deployment methods with step-by-step

📖 DEPLOYMENT.md
   Purpose: Comprehensive production guide
   When: Enterprise setup, detailed understanding needed
   Contents: Full architecture, security, troubleshooting

🛠️ SCRIPTS.md
   Purpose: Scripts reference & workflows
   When: Daily operations, automation setup
   Contents: All scripts documented with examples

✅ PRODUCTION-CHECKLIST.md
   Purpose: Production readiness verification
   When: Before go-live, security audit
   Contents: Security, infrastructure, deployment checks

📦 DEPLOYMENT-SUMMARY.md
   Purpose: Package overview & learning path
   When: Understanding the package, team onboarding
   Contents: File structure, use cases, features

📄 README.md
   Purpose: Original project documentation
   When: Development mode, contributing
   Contents: Architecture, quick start, API reference

═══════════════════════════════════════════════════════════════

🚀 SCRIPTS AVAILABLE:

Deployment:
  ./deploy-production.sh      Auto deployment with prompts
  ./install-systemd.sh        Install as systemd service

Management:
  ./manage.sh start           Start services
  ./manage.sh stop            Stop services
  ./manage.sh restart         Restart services
  ./manage.sh status          Check status
  ./manage.sh logs            View logs
  ./manage.sh backup          Backup data
  ./manage.sh restore FILE    Restore from backup
  ./manage.sh health          Health check
  ./manage.sh update          Update version
  ./manage.sh clean [DAYS]    Clean old events

Monitoring:
  ./monitor.sh                Run health checks
  ./monitor.sh --report       Generate report

Setup:
  ./setup-cron.sh             Setup automated tasks
  ./setup-nginx.sh            Setup nginx reverse proxy

═══════════════════════════════════════════════════════════════

🎯 DEPLOYMENT METHODS:

Method 1: Cloudflare Tunnel (Recommended for quick setup)
  ✓ No TLS setup needed
  ✓ Works behind NAT/firewall
  ✓ DDoS protection
  Time: 10 minutes

Method 2: Direct TLS (VPS with public IP)
  ✓ Full control
  ✓ No external dependencies
  Time: 20 minutes

Method 3: Nginx Reverse Proxy (Production grade)
  ✓ Multiple services support
  ✓ Rate limiting
  ✓ Security headers
  Time: 30 minutes

Method 4: Systemd Service (No Docker)
  ✓ Native service
  ✓ Lower overhead
  Time: 15 minutes

═══════════════════════════════════════════════════════════════

🆘 QUICK HELP:

Service not starting?
  → ./manage.sh status
  → docker compose logs

Agents can't connect?
  → telnet your-server 8443
  → Check firewall: sudo ufw status

Dashboard not accessible?
  → curl http://localhost:8080/healthz
  → Check nginx: sudo nginx -t

Disk full?
  → ./manage.sh clean 7
  → df -h ./data/

Full troubleshooting → DEPLOYMENT.md (Troubleshooting section)

═══════════════════════════════════════════════════════════════

💡 RECOMMENDED WORKFLOW:

┌─ Pre-Deployment ─────────────────────────────────────────────┐
│ 1. Read QUICKSTART.md (5 minutes)                           │
│ 2. Choose deployment method                                  │
│ 3. Prepare .env.production file                             │
│ 4. Generate TLS certificates (if needed)                     │
└──────────────────────────────────────────────────────────────┘
                            ↓
┌─ Deployment ─────────────────────────────────────────────────┐
│ 5. Run ./deploy-production.sh                               │
│ 6. Verify with ./manage.sh status                           │
│ 7. Check health with ./manage.sh health                     │
└──────────────────────────────────────────────────────────────┘
                            ↓
┌─ Post-Deployment ────────────────────────────────────────────┐
│ 8. Setup monitoring with ./setup-cron.sh                    │
│ 9. Test agent connection                                     │
│ 10. Review PRODUCTION-CHECKLIST.md                          │
└──────────────────────────────────────────────────────────────┘
                            ↓
                      ✅ Production Ready!

═══════════════════════════════════════════════════════════════

📞 SUPPORT:

Documentation: Read the docs above
GitHub Issues: [Repository URL]/issues
PMG Docs: https://github.com/safedep/pmg
Dashboard Help: Settings → Help (in-app)

═══════════════════════════════════════════════════════════════

Package Version: 1.0
Created: 2026-06-08
Total Files: 19 (6 docs, 6 scripts, 4 configs, 2 docker, 1 service)

To view this index anytime: ./show-docs.sh

EOF
