package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	controltowerv1grpc "buf.build/gen/go/safedep/api/grpc/go/safedep/services/controltower/v1/controltowerv1grpc"
	servicev1 "buf.build/gen/go/safedep/api/protocolbuffers/go/safedep/services/controltower/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Server implements the EndpointService gRPC interface used by PMG agents
// to deliver audit events to a custom cloud backend.
type Server struct {
	controltowerv1grpc.UnimplementedEndpointServiceServer

	dataDir string
	apiKeys map[string]struct{} // set of accepted API keys; empty = no auth

	mu      sync.Mutex
	logFile *os.File
	logDate string // current log file date (YYYYMMDD)
}

// storedEvent is the on-disk representation of one received event.
type storedEvent struct {
	ReceivedAt   time.Time       `json:"received_at"`
	TenantID     string          `json:"tenant_id,omitempty"`
	EndpointID   string          `json:"endpoint_id,omitempty"`
	InvocationID string          `json:"invocation_id,omitempty"`
	ToolName     string          `json:"tool_name,omitempty"`
	ToolVersion  string          `json:"tool_version,omitempty"`
	EventID      string          `json:"event_id"`
	Event        json.RawMessage `json:"event"`
}

func New(dataDir string, apiKeys []string) (*Server, error) {
	keys := make(map[string]struct{}, len(apiKeys))
	for _, k := range apiKeys {
		if k != "" {
			keys[k] = struct{}{}
		}
	}
	return &Server{dataDir: dataDir, apiKeys: keys}, nil
}

func (s *Server) SyncEvents(ctx context.Context, req *servicev1.SyncEventsRequest) (*servicev1.SyncEventsResponse, error) {
	tenantID, apiKey := credentialsFromContext(ctx)

	if len(s.apiKeys) > 0 {
		if _, ok := s.apiKeys[apiKey]; !ok {
			return nil, status.Error(codes.Unauthenticated, "invalid API key")
		}
	}

	endpointID := ""
	if ep := req.GetEndpoint(); ep != nil {
		endpointID = ep.GetIdentifier()
	}

	var confirmedIDs []string
	for _, ev := range req.GetEvents() {
		id := ev.GetEventId()
		if err := s.storeEvent(ev, tenantID, endpointID); err != nil {
			slog.Error("failed to store event", "id", id, "err", err)
			continue
		}
		confirmedIDs = append(confirmedIDs, id)
	}

	slog.Info("synced events", "count", len(confirmedIDs), "tenant", tenantID, "endpoint", endpointID)
	return &servicev1.SyncEventsResponse{ConfirmedEventIds: confirmedIDs}, nil
}

func (s *Server) storeEvent(ev *servicev1.ToolEvent, tenantID, endpointID string) error {
	raw, err := json.Marshal(ev)
	if err != nil {
		return err
	}

	record := storedEvent{
		ReceivedAt:   time.Now().UTC(),
		TenantID:     tenantID,
		EndpointID:   endpointID,
		InvocationID: ev.GetInvocationId(),
		ToolName:     ev.GetToolName(),
		ToolVersion:  ev.GetToolVersion(),
		EventID:      ev.GetEventId(),
		Event:        json.RawMessage(raw),
	}

	line, err := json.Marshal(record)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.rotateIfNeeded(); err != nil {
		return err
	}

	_, err = s.logFile.Write(append(line, '\n'))
	return err
}

// rotateIfNeeded opens a new daily log file when the date changes.
// Must be called with s.mu held.
func (s *Server) rotateIfNeeded() error {
	today := time.Now().UTC().Format("20060102")
	if s.logFile != nil && s.logDate == today {
		return nil
	}
	if s.logFile != nil {
		if err := s.logFile.Close(); err != nil {
			slog.Error("failed to close log file", "err", err)
		}
	}

	path := filepath.Join(s.dataDir, "events-"+today+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	s.logFile = f
	s.logDate = today
	return nil
}

// credentialsFromContext extracts tenant ID and API key from gRPC metadata.
// The dry/cloud client (adapters/grpc) sends:
//   - x-tenant-id: tenant domain string
//   - authorization: raw API key (no "Bearer " prefix)
func credentialsFromContext(ctx context.Context) (tenantID, apiKey string) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", ""
	}
	if vals := md.Get("x-tenant-id"); len(vals) > 0 {
		tenantID = vals[0]
	}
	if vals := md.Get("authorization"); len(vals) > 0 {
		apiKey = vals[0]
	}
	return tenantID, apiKey
}
