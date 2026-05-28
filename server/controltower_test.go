package server

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	ctv1 "buf.build/gen/go/safedep/api/protocolbuffers/go/safedep/messages/controltower/v1"
	servicev1 "buf.build/gen/go/safedep/api/protocolbuffers/go/safedep/services/controltower/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// newServer creates a Server backed by a temp dir for test isolation.
func newServer(t *testing.T, apiKeys []string) *Server {
	t.Helper()
	s, err := New(t.TempDir(), apiKeys, nil, nil, nil)
	require.NoError(t, err)
	return s
}

// ctxWithAuth returns a context carrying gRPC incoming metadata with the given API key.
func ctxWithAuth(apiKey string) context.Context {
	md := metadata.Pairs("authorization", apiKey)
	return metadata.NewIncomingContext(context.Background(), md)
}

// ctxWithTenantAndAuth returns a context with tenant ID and API key metadata.
func ctxWithTenantAndAuth(tenantID, apiKey string) context.Context {
	md := metadata.Pairs("x-tenant-id", tenantID, "authorization", apiKey)
	return metadata.NewIncomingContext(context.Background(), md)
}

// simpleEvent builds a minimal ToolEvent with the given event ID.
func simpleEvent(id string) *servicev1.ToolEvent {
	return servicev1.ToolEvent_builder{EventId: id, ToolName: "pmg"}.Build()
}

// simpleRequest wraps events in a SyncEventsRequest with no endpoint.
func simpleRequest(events ...*servicev1.ToolEvent) *servicev1.SyncEventsRequest {
	return servicev1.SyncEventsRequest_builder{Events: events}.Build()
}

func TestSyncEvents_NoAuthAcceptsAnyRequest(t *testing.T) {
	s := newServer(t, nil)

	resp, err := s.SyncEvents(context.Background(), simpleRequest(simpleEvent("ev-1")))
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Contains(t, resp.GetConfirmedEventIds(), "ev-1")
}

func TestSyncEvents_ValidAPIKeyAccepted(t *testing.T) {
	s := newServer(t, []string{"secret-key"})

	resp, err := s.SyncEvents(ctxWithAuth("secret-key"), simpleRequest(simpleEvent("ev-2")))
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Contains(t, resp.GetConfirmedEventIds(), "ev-2")
}

func TestSyncEvents_InvalidAPIKeyRejected(t *testing.T) {
	s := newServer(t, []string{"secret-key"})

	_, err := s.SyncEvents(ctxWithAuth("wrong-key"), simpleRequest(simpleEvent("ev-3")))
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unauthenticated, st.Code())
}

func TestSyncEvents_MissingAPIKeyRejected(t *testing.T) {
	s := newServer(t, []string{"secret-key"})

	_, err := s.SyncEvents(context.Background(), simpleRequest(simpleEvent("ev-4")))
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unauthenticated, st.Code())
}

func TestSyncEvents_StoresEventsToJSONLFile(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, nil, nil, nil, nil)
	require.NoError(t, err)

	ctx := ctxWithTenantAndAuth("tenant-abc", "")
	endpoint := ctv1.EndpointIdentity_builder{
		Identifier: "test-endpoint",
		MachineId:  "machine-123",
	}.Build()
	req := servicev1.SyncEventsRequest_builder{
		Endpoint: endpoint,
		Events: []*servicev1.ToolEvent{
			simpleEvent("store-ev-1"),
			simpleEvent("store-ev-2"),
		},
	}.Build()

	resp, err := s.SyncEvents(ctx, req)
	require.NoError(t, err)
	assert.Len(t, resp.GetConfirmedEventIds(), 2)

	// Find the written JSONL file
	files, err := filepath.Glob(filepath.Join(dir, "events-*.jsonl"))
	require.NoError(t, err)
	require.Len(t, files, 1)

	// Parse the written lines
	f, err := os.Open(files[0])
	require.NoError(t, err)
	defer f.Close()

	var records []map[string]interface{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var rec map[string]interface{}
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &rec))
		records = append(records, rec)
	}
	require.Len(t, records, 2)

	eventIDs := []string{
		records[0]["event_id"].(string),
		records[1]["event_id"].(string),
	}
	assert.ElementsMatch(t, []string{"store-ev-1", "store-ev-2"}, eventIDs)

	// Tenant ID must be persisted on each record
	assert.Equal(t, "tenant-abc", records[0]["tenant_id"])
	assert.Equal(t, "tenant-abc", records[1]["tenant_id"])
}

func TestShortEnum_StripsPrefix(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		prefix string
		want   string
	}{
		{
			name:   "package decision event type",
			input:  "PMG_EVENT_TYPE_PACKAGE_DECISION",
			prefix: "PMG_EVENT_TYPE_",
			want:   "PACKAGE_DECISION",
		},
		{
			name:   "session summary event type",
			input:  "PMG_EVENT_TYPE_SESSION_SUMMARY",
			prefix: "PMG_EVENT_TYPE_",
			want:   "SESSION_SUMMARY",
		},
		{
			name:   "ecosystem npm",
			input:  "ECOSYSTEM_NPM",
			prefix: "ECOSYSTEM_",
			want:   "NPM",
		},
		{
			name:   "action blocked",
			input:  "PMG_PACKAGE_ACTION_BLOCKED",
			prefix: "PMG_PACKAGE_ACTION_",
			want:   "BLOCKED",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := shortEnum(tc.input, tc.prefix)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestShortEnum_ReturnsEmptyForUnspecified(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		prefix string
	}{
		{
			name:   "unspecified with prefix",
			input:  "PMG_EVENT_TYPE_UNSPECIFIED",
			prefix: "PMG_EVENT_TYPE_",
		},
		{
			name:   "plain unspecified no prefix match",
			input:  "UNSPECIFIED",
			prefix: "PMG_EVENT_TYPE_",
		},
		{
			name:   "empty string",
			input:  "",
			prefix: "PMG_EVENT_TYPE_",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := shortEnum(tc.input, tc.prefix)
			assert.Empty(t, got)
		})
	}
}

func TestShortEnum_NoPrefixMatchReturnsOriginal(t *testing.T) {
	// When the prefix is not found and the value does not contain UNSPECIFIED,
	// the original string is returned unchanged.
	got := shortEnum("SOME_OTHER_VALUE", "PMG_EVENT_TYPE_")
	assert.Equal(t, "SOME_OTHER_VALUE", got)
}
