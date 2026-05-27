package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	controltowerv1grpc "buf.build/gen/go/safedep/api/grpc/go/safedep/services/controltower/v1/controltowerv1grpc"
	malysisv1grpc "buf.build/gen/go/safedep/api/grpc/go/safedep/services/malysis/v1/malysisv1grpc"
	"github.com/yourorg/pmg-cloud/dashboard"
	"github.com/yourorg/pmg-cloud/server"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection"
)

func main() {
	addr := flag.String("addr", ":8443", "gRPC listen address (host:port)")
	tlsCert := flag.String("tls-cert", "", "TLS certificate file (PEM)")
	tlsKey := flag.String("tls-key", "", "TLS key file (PEM)")
	insecure := flag.Bool("insecure", false, "Disable TLS (plaintext gRPC, for local dev)")
	dataDir := flag.String("data-dir", "data", "Directory for event storage")
	apiKeysFlag := flag.String("api-keys", "", "Comma-separated list of accepted API keys (empty = no auth)")
	httpAddr := flag.String("http-addr", ":8080", "HTTP dashboard listen address (empty to disable)")
	retentionDays := flag.Int("retention-days", 30, "Delete event files older than this many days (0 = disabled)")
	dashUser := flag.String("dash-user", "", "Dashboard HTTP basic auth username (empty = no auth)")
	dashPass := flag.String("dash-pass", "", "Dashboard HTTP basic auth password")
	malwareRefreshInterval := flag.Duration("malware-refresh-interval", 6*time.Hour, "Auto-refresh interval for the Aikido malware feed (0 = disabled)")
	flag.Parse()

	// Also support API keys from env
	apiKeysRaw := *apiKeysFlag
	if apiKeysRaw == "" {
		apiKeysRaw = os.Getenv("PMG_CLOUD_API_KEYS")
	}
	if *dashUser == "" {
		*dashUser = os.Getenv("PMG_CLOUD_DASH_USER")
	}
	if *dashPass == "" {
		*dashPass = os.Getenv("PMG_CLOUD_DASH_PASS")
	}
	var apiKeys []string
	for _, k := range strings.Split(apiKeysRaw, ",") {
		if k = strings.TrimSpace(k); k != "" {
			apiKeys = append(apiKeys, k)
		}
	}

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		slog.Error("failed to create data dir", "err", err)
		os.Exit(1)
	}

	var serverOpts []grpc.ServerOption

	if *insecure {
		slog.Warn("TLS disabled — plaintext gRPC only, do not use in production")
	} else {
		if *tlsCert == "" || *tlsKey == "" {
			slog.Error("--tls-cert and --tls-key are required unless --insecure is set")
			os.Exit(1)
		}
		cert, err := tls.LoadX509KeyPair(*tlsCert, *tlsKey)
		if err != nil {
			slog.Error("failed to load TLS credentials", "err", err)
			os.Exit(1)
		}
		serverOpts = append(serverOpts, grpc.Creds(credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{cert},
		})))
	}

	groups, err := dashboard.NewGroupStore(*dataDir)
	if err != nil {
		slog.Error("failed to open group store", "err", err)
		os.Exit(1)
	}

	enrollmentStore, err := dashboard.NewEnrollmentStore(*dataDir)
	if err != nil {
		slog.Error("failed to open enrollment store", "err", err)
		os.Exit(1)
	}

	cfgStore, err := dashboard.NewConfigStore(*dataDir)
	if err != nil {
		slog.Error("failed to open config store", "err", err)
		os.Exit(1)
	}

	auditLog := dashboard.NewAuditLog(*dataDir)
	webhookDelivery := dashboard.NewWebhookDelivery(cfgStore)

	svc, err := server.New(*dataDir, apiKeys, groups, webhookDelivery)
	if err != nil {
		slog.Error("failed to create server", "err", err)
		os.Exit(1)
	}

	s := grpc.NewServer(serverOpts...)
	controltowerv1grpc.RegisterEndpointServiceServer(s, svc)

	relay, err := server.NewMalysisRelay()
	if err != nil {
		slog.Warn("malysis relay unavailable — agents cannot use pmg-cloud as SafeDep proxy", "err", err)
	} else {
		malysisv1grpc.RegisterMalwareAnalysisServiceServer(s, relay)
		slog.Info("malysis relay registered")
	}

	reflection.Register(s) // enables grpcurl introspection

	// Use tcp4 to ensure IPv4 binding on WSL2 where "tcp" defaults to IPv6-only.
	lis, err := net.Listen("tcp4", *addr)
	if err != nil {
		slog.Error("failed to listen", "addr", *addr, "err", err)
		os.Exit(1)
	}

	authMode := "no auth"
	if groups.HasKeys() {
		keyCounts := groups.KeyCount()
		total := 0
		for _, n := range keyCounts {
			total += n
		}
		authMode = fmt.Sprintf("group-auth (%d groups, %d keys)", len(groups.ListGroups()), total)
	} else if len(apiKeys) > 0 {
		authMode = fmt.Sprintf("%d static API key(s)", len(apiKeys))
	}
	slog.Info("pmg-cloud started", "addr", *addr, "insecure", *insecure, "auth", authMode, "data_dir", *dataDir)

	effectiveRetention := *retentionDays
	if cfgStore != nil {
		if r := cfgStore.Get().RetentionDays; r > 0 {
			effectiveRetention = r
		}
	}
	if effectiveRetention > 0 {
		go runRetentionLoop(*dataDir, effectiveRetention)
	}

	if *httpAddr != "" {
		go func() {
			// Use tcp4 explicitly — Go defaults to IPv6-only on Linux/WSL2,
			// which prevents Windows browsers from connecting via localhost.
			ln, err := net.Listen("tcp4", *httpAddr)
			if err != nil {
				slog.Error("dashboard listen error", "addr", *httpAddr, "err", err)
				return
			}
			slog.Info("dashboard started", "addr", *httpAddr)
			mirror := dashboard.NewMalwareMirror(*dataDir + "/aikido-mirror")
			if *malwareRefreshInterval > 0 {
				mirror.StartAutoRefresh(context.Background(), *malwareRefreshInterval)
			}

			userStore, err := dashboard.NewUserStore(*dataDir, *dashUser, *dashPass)
			if err != nil {
				slog.Error("failed to open user store", "err", err)
				return
			}
			sessionStore := dashboard.NewSessionStore()

			deps := dashboard.HandlerDeps{
				Mirror:     mirror,
				Groups:     groups,
				Config:     cfgStore,
				Audit:      auditLog,
				Webhook:    webhookDelivery,
				Users:      userStore,
				Sessions:   sessionStore,
				Enrollment:   enrollmentStore,
				GRPCAddr:     *addr,
				GRPCInsecure: *insecure,
			}

			// /healthz is always unauthenticated (load balancers, Docker HEALTHCHECK).
			// Session-based auth is handled inside dashboard.Handler for all /api/* routes.
			mux := http.NewServeMux()
			mux.Handle("/healthz", dashboard.HealthzHandler(*dataDir, mirror))
			mux.Handle("/", dashboard.Handler(*dataDir, deps))
			if err := http.Serve(ln, mux); err != nil {
				slog.Error("dashboard error", "err", err)
			}
		}()
	}

	if err := s.Serve(lis); err != nil {
		slog.Error("server error", "err", err)
		os.Exit(1)
	}
}

func runRetentionLoop(dataDir string, days int) {
	runOnce := func() {
		n, err := dashboard.DeleteOldFiles(dataDir, days)
		if err != nil {
			slog.Warn("retention cleanup error", "err", err)
		} else if n > 0 {
			slog.Info("retention: deleted old event files", "count", n, "retention_days", days)
		}
	}
	runOnce()
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		runOnce()
	}
}
