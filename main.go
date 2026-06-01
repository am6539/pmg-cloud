package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	controltowerv1grpc "buf.build/gen/go/safedep/api/grpc/go/safedep/services/controltower/v1/controltowerv1grpc"
	malysisv1grpc "buf.build/gen/go/safedep/api/grpc/go/safedep/services/malysis/v1/malysisv1grpc"
	servicev1 "buf.build/gen/go/safedep/api/protocolbuffers/go/safedep/services/controltower/v1"
	malysisv1 "buf.build/gen/go/safedep/api/protocolbuffers/go/safedep/services/malysis/v1"
	"github.com/yourorg/pmg-cloud/dashboard"
	"github.com/yourorg/pmg-cloud/server"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection"
	"google.golang.org/protobuf/proto"
)

func main() {
	addr := flag.String("addr", ":8443", "gRPC listen address (host:port); set empty to disable dedicated gRPC port")
	tlsCert := flag.String("tls-cert", "", "TLS certificate file (PEM)")
	tlsKey := flag.String("tls-key", "", "TLS key file (PEM)")
	insecure := flag.Bool("insecure", false, "Disable TLS on dedicated gRPC port (plaintext gRPC)")
	dataDir := flag.String("data-dir", "data", "Directory for event storage")
	apiKeysFlag := flag.String("api-keys", "", "Comma-separated list of accepted API keys (empty = no auth)")
	httpAddr := flag.String("http-addr", ":8080", "HTTP dashboard listen address (empty to disable)")
	grpcPublicAddr := flag.String("grpc-public-addr", "", "Public gRPC address reported to enrolling agents (e.g. host:443 for Cloudflare Tunnel)")
	retentionDays := flag.Int("retention-days", 30, "Delete event files older than this many days (0 = disabled)")
	dashUser := flag.String("dash-user", "", "Dashboard HTTP basic auth username (empty = no auth)")
	dashPass := flag.String("dash-pass", "", "Dashboard HTTP basic auth password")
	malwareRefreshInterval := flag.Duration("malware-refresh-interval", 6*time.Hour, "Auto-refresh interval for the Aikido malware feed (0 = disabled)")
	flag.Parse()

	// Env overrides for all string flags
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
	if *grpcPublicAddr == "" {
		*grpcPublicAddr = os.Getenv("PMG_CLOUD_GRPC_PUBLIC_ADDR")
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

	updateStore, err := dashboard.NewUpdateStore(*dataDir)
	if err != nil {
		slog.Error("failed to open update store", "err", err)
		os.Exit(1)
	}

	policyStore, err := dashboard.NewPolicyStore(*dataDir)
	if err != nil {
		slog.Error("failed to open policy store", "err", err)
		os.Exit(1)
	}

	cfgStore, err := dashboard.NewConfigStore(*dataDir)
	if err != nil {
		slog.Error("failed to open config store", "err", err)
		os.Exit(1)
	}

	missingMonitor := dashboard.NewMissingAgentMonitor(enrollmentStore, cfgStore)
	go missingMonitor.Run(make(chan struct{})) // lives for process lifetime

	auditLog := dashboard.NewAuditLog(*dataDir)
	webhookDelivery := dashboard.NewWebhookDelivery(cfgStore)

	svc, err := server.New(*dataDir, apiKeys, groups, enrollmentStore, webhookDelivery)
	if err != nil {
		slog.Error("failed to create server", "err", err)
		os.Exit(1)
	}

	// Build gRPC server (used both for dedicated port and h2c mux)
	var serverOpts []grpc.ServerOption
	if *insecure {
		slog.Warn("TLS disabled — plaintext gRPC only, do not use in production")
	} else if *addr != "" {
		if *tlsCert == "" || *tlsKey == "" {
			slog.Error("--tls-cert and --tls-key are required unless --insecure is set (or set --addr= to skip dedicated gRPC port)")
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

	grpcServer := grpc.NewServer(serverOpts...)
	controltowerv1grpc.RegisterEndpointServiceServer(grpcServer, svc)

	relay, err := server.NewMalysisRelay()
	if err != nil {
		slog.Warn("malysis relay unavailable — agents cannot use pmg-cloud as SafeDep proxy", "err", err)
	} else {
		malysisv1grpc.RegisterMalwareAnalysisServiceServer(grpcServer, relay)
		slog.Info("malysis relay registered")
	}

	reflection.Register(grpcServer)

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

	effectiveRetention := *retentionDays
	if cfgStore != nil {
		if r := cfgStore.Get().RetentionDays; r > 0 {
			effectiveRetention = r
		}
	}
	if effectiveRetention > 0 {
		go runRetentionLoop(*dataDir, effectiveRetention)
	}

	// Dedicated gRPC port (optional — skip when addr is empty or grpc-public-addr is set)
	if *addr != "" && *grpcPublicAddr == "" {
		lis, err := net.Listen("tcp4", *addr)
		if err != nil {
			slog.Error("failed to listen on gRPC addr", "addr", *addr, "err", err)
			os.Exit(1)
		}
		slog.Info("pmg-cloud started", "grpc_addr", *addr, "insecure", *insecure, "auth", authMode, "data_dir", *dataDir)
		go func() {
			if err := grpcServer.Serve(lis); err != nil {
				slog.Error("gRPC server error", "err", err)
			}
		}()
	}

	// HTTP dashboard + h2c gRPC mux on httpAddr
	if *httpAddr != "" {
		ln, err := net.Listen("tcp4", *httpAddr)
		if err != nil {
			slog.Error("dashboard listen error", "addr", *httpAddr, "err", err)
			os.Exit(1)
		}

		mirror := dashboard.NewMalwareMirror(*dataDir + "/aikido-mirror")
		if *malwareRefreshInterval > 0 {
			mirror.StartAutoRefresh(context.Background(), *malwareRefreshInterval)
		}

		userStore, err := dashboard.NewUserStore(*dataDir, *dashUser, *dashPass)
		if err != nil {
			slog.Error("failed to open user store", "err", err)
			os.Exit(1)
		}
		sessionStore := dashboard.NewSessionStore()

		// GRPCAddr reported to enrolling agents:
		//   - grpc-public-addr if set (Cloudflare Tunnel / reverse proxy)
		//   - otherwise the dedicated gRPC listen addr
		grpcAddrForClients := *grpcPublicAddr
		if grpcAddrForClients == "" {
			grpcAddrForClients = *addr
		}
		// Insecure only applies when there is no public proxy handling TLS
		grpcInsecureForClients := *insecure && *grpcPublicAddr == ""

		deps := dashboard.HandlerDeps{
			Mirror:       mirror,
			Groups:       groups,
			Config:       cfgStore,
			Audit:        auditLog,
			Webhook:      webhookDelivery,
			Users:        userStore,
			Sessions:     sessionStore,
			Enrollment:   enrollmentStore,
			Updates:      updateStore,
			Policy:       policyStore,
			GRPCAddr:     grpcAddrForClients,
			GRPCInsecure: grpcInsecureForClients,
		}

		dashMux := http.NewServeMux()
		dashMux.Handle("/healthz", dashboard.HealthzHandler(*dataDir, mirror))
		// HTTP sync endpoint: POST /api/sync — protobuf over HTTP/1.1 for agents
		// that cannot reach gRPC (e.g. behind Cloudflare Tunnel on public hostnames).
		dashMux.Handle("/api/sync", httpSyncHandler(svc))
		// HTTP malysis endpoint: POST /api/malysis — protobuf over HTTP/1.1 fallback
		// for agents that cannot reach gRPC malysis relay (e.g. behind Cloudflare Tunnel).
		if relay != nil {
			dashMux.Handle("/api/malysis", httpMalysisHandler(relay, svc))
		}
		dashMux.Handle("/", dashboard.Handler(*dataDir, deps))

		// Route gRPC (HTTP/2, Content-Type: application/grpc) to grpcServer;
		// everything else goes to the dashboard. Wrapped in h2c so Cloudflare
		// Tunnel (and other reverse proxies) can forward HTTP/2 cleartext.
		combined := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
				grpcServer.ServeHTTP(w, r)
				return
			}
			dashMux.ServeHTTP(w, r)
		})

		h2cServer := &http2.Server{}
		slog.Info("dashboard started", "addr", *httpAddr, "grpc_mux", true)
		if err := http.Serve(ln, h2c.NewHandler(combined, h2cServer)); err != nil {
			slog.Error("dashboard error", "err", err)
		}
	} else if *addr != "" && *grpcPublicAddr == "" {
		// No HTTP, dedicated gRPC only — block here
		select {}
	}
}

// httpSyncHandler returns an http.Handler for POST /api/sync.
// It accepts a protobuf SyncEventsRequest body and responds with SyncEventsResponse.
// This provides an HTTP/1.1 alternative to gRPC for agents behind Cloudflare Tunnel.
func httpSyncHandler(svc *server.Server) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		apiKey := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")

		body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil {
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}

		var req servicev1.SyncEventsRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			http.Error(w, "invalid protobuf body", http.StatusBadRequest)
			return
		}

		// Extract remote IP, honouring Cloudflare's CF-Connecting-IP header.
		remoteIP := r.RemoteAddr
		if h, _, e := net.SplitHostPort(remoteIP); e == nil {
			remoteIP = h
		}
		if cfIP := r.Header.Get("CF-Connecting-IP"); cfIP != "" {
			remoteIP = cfIP
		} else if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if parts := strings.SplitN(xff, ",", 2); len(parts) > 0 {
				remoteIP = strings.TrimSpace(parts[0])
			}
		}

		resp, err := svc.SyncEventsHTTP(r.Context(), apiKey, remoteIP, &req)
		if err != nil {
			if strings.Contains(err.Error(), "invalid API key") {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			} else {
				slog.Error("http-sync error", "err", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
			return
		}

		respBody, err := proto.Marshal(resp)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		if _, err := w.Write(respBody); err != nil {
			slog.Warn("http-sync: failed to write response", "err", err)
		}
	})
}

// httpMalysisHandler returns an http.Handler for POST /api/malysis.
// It accepts a protobuf QueryPackageAnalysisRequest body and responds with
// QueryPackageAnalysisResponse. This provides an HTTP/1.1 alternative to the
// gRPC malysis relay for agents behind Cloudflare Tunnel.
func httpMalysisHandler(relay *server.MalysisRelay, svc *server.Server) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		apiKey := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if err := svc.ValidateAPIKey(apiKey); err != nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil {
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}

		var req malysisv1.QueryPackageAnalysisRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			http.Error(w, "invalid protobuf body", http.StatusBadRequest)
			return
		}

		resp, err := relay.QueryPackageAnalysis(r.Context(), &req)
		if err != nil {
			slog.Error("http-malysis error", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		respBody, err := proto.Marshal(resp)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		if _, err := w.Write(respBody); err != nil {
			slog.Warn("http-malysis: failed to write response", "err", err)
		}
	})
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
