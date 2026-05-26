package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"

	controltowerv1grpc "buf.build/gen/go/safedep/api/grpc/go/safedep/services/controltower/v1/controltowerv1grpc"
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
	flag.Parse()

	// Also support API keys from env
	apiKeysRaw := *apiKeysFlag
	if apiKeysRaw == "" {
		apiKeysRaw = os.Getenv("PMG_CLOUD_API_KEYS")
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

	svc, err := server.New(*dataDir, apiKeys)
	if err != nil {
		slog.Error("failed to create server", "err", err)
		os.Exit(1)
	}

	s := grpc.NewServer(serverOpts...)
	controltowerv1grpc.RegisterEndpointServiceServer(s, svc)
	reflection.Register(s) // enables grpcurl introspection

	lis, err := net.Listen("tcp", *addr)
	if err != nil {
		slog.Error("failed to listen", "addr", *addr, "err", err)
		os.Exit(1)
	}

	authMode := "no auth"
	if len(apiKeys) > 0 {
		authMode = fmt.Sprintf("%d API key(s)", len(apiKeys))
	}
	slog.Info("pmg-cloud started", "addr", *addr, "insecure", *insecure, "auth", authMode, "data_dir", *dataDir)

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
			if err := http.Serve(ln, dashboard.Handler(*dataDir, mirror)); err != nil {
				slog.Error("dashboard error", "err", err)
			}
		}()
	}

	if err := s.Serve(lis); err != nil {
		slog.Error("server error", "err", err)
		os.Exit(1)
	}
}
