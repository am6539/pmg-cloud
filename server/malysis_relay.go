package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"sync"
	"time"

	malysisv1grpc "buf.build/gen/go/safedep/api/grpc/go/safedep/services/malysis/v1/malysisv1grpc"
	malysisv1 "buf.build/gen/go/safedep/api/protocolbuffers/go/safedep/services/malysis/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

const (
	safedepUpstream = "community-api.safedep.io:443"
	cacheEntryTTL   = time.Hour
)

type cacheEntry struct {
	resp      *malysisv1.QueryPackageAnalysisResponse
	expiresAt time.Time
}

// MalysisRelay forwards QueryPackageAnalysis calls to SafeDep and caches
// responses in memory for one hour. All other service methods return
// codes.Unimplemented via the embedded struct.
type MalysisRelay struct {
	malysisv1grpc.UnimplementedMalwareAnalysisServiceServer

	client malysisv1grpc.MalwareAnalysisServiceClient

	mu    sync.Mutex
	cache map[string]*cacheEntry
}

// NewMalysisRelay creates a gRPC connection to SafeDep and returns a
// ready-to-register MalysisRelay. Returns an error if the connection cannot
// be established.
func NewMalysisRelay() (*MalysisRelay, error) {
	conn, err := grpc.NewClient(safedepUpstream,
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{})),
	)
	if err != nil {
		return nil, fmt.Errorf("dial safedep upstream: %w", err)
	}
	return &MalysisRelay{
		client: malysisv1grpc.NewMalwareAnalysisServiceClient(conn),
		cache:  make(map[string]*cacheEntry),
	}, nil
}

// QueryPackageAnalysis forwards the request to SafeDep, caching results for
// one hour keyed by ecosystem:name:version.
func (r *MalysisRelay) QueryPackageAnalysis(ctx context.Context, req *malysisv1.QueryPackageAnalysisRequest) (*malysisv1.QueryPackageAnalysisResponse, error) {
	key := cacheKey(req)
	slog.Debug("malysis relay: query", "key", key)

	if cached := r.fromCache(key); cached != nil {
		return cached, nil
	}

	resp, err := r.client.QueryPackageAnalysis(ctx, req)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "safedep upstream error: %v", err)
	}

	r.toCache(key, resp)
	return resp, nil
}

func (r *MalysisRelay) fromCache(key string) *malysisv1.QueryPackageAnalysisResponse {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.cache[key]
	if !ok || time.Now().After(entry.expiresAt) {
		delete(r.cache, key)
		return nil
	}
	return entry.resp
}

func (r *MalysisRelay) toCache(key string, resp *malysisv1.QueryPackageAnalysisResponse) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache[key] = &cacheEntry{resp: resp, expiresAt: time.Now().Add(cacheEntryTTL)}
}

// cacheKey returns "ecosystem:name:version" when the target fields are present,
// falling back to a fmt.Sprintf representation for unusual requests.
func cacheKey(req *malysisv1.QueryPackageAnalysisRequest) string {
	if pv := req.GetTarget().GetPackageVersion(); pv != nil {
		ecosystem := ""
		name := ""
		if pkg := pv.GetPackage(); pkg != nil {
			ecosystem = pkg.GetEcosystem().String()
			name = pkg.GetName()
		}
		version := pv.GetVersion()
		if ecosystem != "" || name != "" || version != "" {
			return ecosystem + ":" + name + ":" + version
		}
	}
	return fmt.Sprintf("%v", req)
}
