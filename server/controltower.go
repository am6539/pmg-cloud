package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	controltowerv1grpc "buf.build/gen/go/safedep/api/grpc/go/safedep/services/controltower/v1/controltowerv1grpc"
	ctv1 "buf.build/gen/go/safedep/api/protocolbuffers/go/safedep/messages/controltower/v1"
	servicev1 "buf.build/gen/go/safedep/api/protocolbuffers/go/safedep/services/controltower/v1"
	"github.com/yourorg/pmg-cloud/dashboard"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// Server implements the EndpointService gRPC interface used by PMG agents
// to deliver audit events to a custom cloud backend.
type Server struct {
	controltowerv1grpc.UnimplementedEndpointServiceServer

	dataDir string
	apiKeys map[string]struct{}        // static key set (backward compat); empty = no auth
	groups  *dashboard.GroupStore      // group-based auth; takes precedence when it has keys
	webhook *dashboard.WebhookDelivery // may be nil

	mu      sync.Mutex
	logFile *os.File
	logDate string // current log file date (YYYYMMDD)
}

// storedEvent is the on-disk JSONL record for one received event.
// Top-level fields are extracted for direct queryability with jq or other tools.
// The raw "event" blob preserves everything for completeness.
type storedEvent struct {
	// --- routing ---
	GroupID string `json:"group_id,omitempty"`

	// --- always present ---
	ReceivedAt   time.Time  `json:"received_at"`
	TenantID     string     `json:"tenant_id,omitempty"`
	EventID      string     `json:"event_id"`
	InvocationID string     `json:"invocation_id,omitempty"`
	ToolName     string     `json:"tool_name,omitempty"`
	ToolVersion  string     `json:"tool_version,omitempty"`
	EventTime    *time.Time `json:"event_time,omitempty"` // client-side timestamp

	// --- endpoint identity ---
	EndpointID string `json:"endpoint_id,omitempty"`
	MachineID  string `json:"machine_id,omitempty"`
	Hostname   string `json:"hostname,omitempty"`
	OS         string `json:"os,omitempty"`
	Arch       string `json:"arch,omitempty"`
	RemoteIP   string `json:"remote_ip,omitempty"`

	// --- pmg event type ---
	EventType string `json:"event_type,omitempty"` // PACKAGE_DECISION | SESSION_SUMMARY | INSECURE_BYPASS | SANDBOX_OVERRIDE | ERROR | HOST_OBSERVATION

	// --- package_decision fields (set when event_type == PACKAGE_DECISION) ---
	Ecosystem      string `json:"ecosystem,omitempty"`
	PackageName    string `json:"package_name,omitempty"`
	PackageVersion string `json:"package_version,omitempty"`
	Action         string `json:"action,omitempty"` // BLOCKED | CONFIRMED | TRUSTED | COOLDOWN_BLOCKED
	// *bool pointers: nil = field not applicable for this event type; false/true = explicit value.
	IsMalware  *bool  `json:"is_malware,omitempty"`
	IsVerified *bool  `json:"is_verified,omitempty"`
	AnalysisID string `json:"analysis_id,omitempty"`

	// --- session_summary fields (set when event_type == SESSION_SUMMARY) ---
	PackageManager       string `json:"package_manager,omitempty"`
	FlowType             string `json:"flow_type,omitempty"` // GUARD | PROXY
	Outcome              string `json:"outcome,omitempty"`   // SUCCESS | BLOCKED | USER_CANCELLED | ERROR | DRY_RUN | INSECURE_BYPASS
	TotalAnalyzed        uint32 `json:"total_analyzed,omitempty"`
	AllowedCount         uint32 `json:"allowed_count,omitempty"`
	BlockedCount         uint32 `json:"blocked_count,omitempty"`
	ConfirmedCount       uint32 `json:"confirmed_count,omitempty"`
	TrustedSkipped       uint32 `json:"trusted_skipped,omitempty"`
	CooldownBlockedCount uint32 `json:"cooldown_blocked_count,omitempty"`
	DurationMs           int64  `json:"duration_ms,omitempty"`
	// *bool pointers for session flags: nil = not a session_summary event.
	SandboxEnabled    *bool `json:"sandbox_enabled,omitempty"`
	ParanoidMode      *bool `json:"paranoid_mode,omitempty"`
	TransitiveEnabled *bool `json:"transitive_enabled,omitempty"`

	// --- invocation_context (when present) ---
	WorkingDirectory string `json:"working_directory,omitempty"`
	Command          string `json:"command,omitempty"`
	CIProvider       string `json:"ci_provider,omitempty"`
	CIRepository     string `json:"ci_repository,omitempty"`
	CIBranch         string `json:"ci_branch,omitempty"`
	CICommitSHA      string `json:"ci_commit_sha,omitempty"`
	AgentName        string `json:"agent_name,omitempty"`

	// --- raw event (always present for completeness) ---
	Event json.RawMessage `json:"event"`
}

func New(dataDir string, apiKeys []string, groups *dashboard.GroupStore, webhook *dashboard.WebhookDelivery) (*Server, error) {
	keys := make(map[string]struct{}, len(apiKeys))
	for _, k := range apiKeys {
		if k != "" {
			keys[k] = struct{}{}
		}
	}
	return &Server{dataDir: dataDir, apiKeys: keys, groups: groups, webhook: webhook}, nil
}

func (s *Server) SyncEvents(ctx context.Context, req *servicev1.SyncEventsRequest) (*servicev1.SyncEventsResponse, error) {
	tenantID, apiKey := credentialsFromContext(ctx)

	// Group-based auth takes precedence when the store has keys.
	var groupID string
	if s.groups != nil && s.groups.HasKeys() {
		gid, ok := s.groups.ResolveKey(apiKey)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "invalid API key")
		}
		groupID = gid
	} else if len(s.apiKeys) > 0 {
		// Fall back to static key list (backward compat).
		if _, ok := s.apiKeys[apiKey]; !ok {
			return nil, status.Error(codes.Unauthenticated, "invalid API key")
		}
	}

	var remoteIP string
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		if host, _, err := net.SplitHostPort(p.Addr.String()); err == nil {
			remoteIP = host
		} else {
			remoteIP = p.Addr.String()
		}
	}

	endpoint := req.GetEndpoint()

	var confirmedIDs []string
	for _, ev := range req.GetEvents() {
		id := ev.GetEventId()
		if err := s.storeEvent(ev, endpoint, tenantID, groupID, remoteIP); err != nil {
			slog.Error("failed to store event", "id", id, "err", err)
			continue
		}
		confirmedIDs = append(confirmedIDs, id)
	}

	endpointID := ""
	if endpoint != nil {
		endpointID = endpoint.GetIdentifier()
	}
	slog.Info("synced events", "count", len(confirmedIDs), "tenant", tenantID, "endpoint", endpointID)
	return &servicev1.SyncEventsResponse{ConfirmedEventIds: confirmedIDs}, nil
}

func (s *Server) storeEvent(ev *servicev1.ToolEvent, endpoint *ctv1.EndpointIdentity, tenantID, groupID, remoteIP string) error {
	raw, err := json.Marshal(ev)
	if err != nil {
		return err
	}

	record := storedEvent{
		GroupID:      groupID,
		ReceivedAt:   time.Now().UTC(),
		TenantID:     tenantID,
		EventID:      ev.GetEventId(),
		InvocationID: ev.GetInvocationId(),
		ToolName:     ev.GetToolName(),
		ToolVersion:  ev.GetToolVersion(),
		Event:        json.RawMessage(raw),
	}

	// client-side event timestamp
	if ts := ev.GetTimestamp(); ts != nil {
		t := ts.AsTime()
		record.EventTime = &t
	}

	// endpoint identity
	if endpoint != nil {
		record.EndpointID = endpoint.GetIdentifier()
		record.MachineID = endpoint.GetMachineId()
		if meta := endpoint.GetMetadata(); meta != nil {
			record.Hostname = meta.GetHostname()
			record.OS = shortEnum(meta.GetOs().String(), "ENDPOINT_OS_")
			record.Arch = shortEnum(meta.GetArch().String(), "ENDPOINT_ARCH_")
		}
	}
	record.RemoteIP = remoteIP

	// invocation context
	if ic := ev.GetInvocationContext(); ic != nil {
		record.WorkingDirectory = ic.GetWorkingDirectory()
		record.Command = ic.GetCommand()
		if ci := ic.GetCi(); ci != nil {
			record.CIProvider = shortEnum(ci.GetProvider().String(), "ENDPOINT_CI_PROVIDER_")
			record.CIRepository = ci.GetRepository()
			record.CIBranch = ci.GetBranch()
			record.CICommitSHA = ci.GetCommitSha()
		}
		if agent := ic.GetAgent(); agent != nil {
			record.AgentName = agent.GetAgentName()
		}
	}

	// pmg event payload
	if pmg := ev.GetPmgEvent(); pmg != nil {
		record.EventType = shortEnum(pmg.GetEventType().String(), "PMG_EVENT_TYPE_")

		switch pmg.GetEventType() {
		case ctv1.PmgEventType_PMG_EVENT_TYPE_PACKAGE_DECISION:
			extractPackageDecision(&record, pmg.GetPackageDecision())

		case ctv1.PmgEventType_PMG_EVENT_TYPE_SESSION_SUMMARY:
			extractSessionSummary(&record, pmg.GetSessionSummary())

		case ctv1.PmgEventType_PMG_EVENT_TYPE_INSECURE_BYPASS:
			if bp := pmg.GetInsecureBypass(); bp != nil {
				record.PackageManager = shortEnum(bp.GetPackageManager().String(), "PMG_PACKAGE_MANAGER_")
			}

		case ctv1.PmgEventType_PMG_EVENT_TYPE_HOST_OBSERVATION:
			if ho := pmg.GetHostObservation(); ho != nil {
				// override endpoint hostname with the one directly observed by the tool
				record.Hostname = ho.GetHostname()
			}

		default:
			// SANDBOX_OVERRIDE and ERROR carry no additional flat fields; raw blob is sufficient.
		}
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

	if _, err = s.logFile.Write(append(line, '\n')); err != nil {
		return err
	}

	s.dispatchWebhook(record)
	return nil
}

// dispatchWebhook fires webhook events for malware or blocked PACKAGE_DECISION records.
// Must be called with s.mu held (record is already persisted at this point).
func (s *Server) dispatchWebhook(r storedEvent) {
	if s.webhook == nil || r.EventType != "PACKAGE_DECISION" {
		return
	}
	var event string
	if r.IsMalware != nil && *r.IsMalware {
		event = "malware_detected"
	} else if r.Action == "BLOCKED" || r.Action == "COOLDOWN_BLOCKED" {
		event = "package_blocked"
	}
	if event == "" {
		return
	}
	s.webhook.Send(dashboard.WebhookPayload{
		Event:      event,
		Timestamp:  r.ReceivedAt,
		GroupID:    r.GroupID,
		Package:    r.PackageName,
		Ecosystem:  r.Ecosystem,
		Action:     r.Action,
		EndpointID: r.EndpointID,
	})
}

func extractPackageDecision(r *storedEvent, pd *ctv1.PmgPackageDecision) {
	if pd == nil {
		return
	}
	r.Action = shortEnum(pd.GetAction().String(), "PMG_PACKAGE_ACTION_")
	isMalware := pd.GetIsMalware()
	isVerified := pd.GetIsVerified()
	r.IsMalware = &isMalware
	r.IsVerified = &isVerified
	r.AnalysisID = pd.GetAnalysisId()

	if pv := pd.GetPackageVersion(); pv != nil {
		r.PackageVersion = pv.GetVersion()
		if pkg := pv.GetPackage(); pkg != nil {
			r.PackageName = pkg.GetName()
			r.Ecosystem = shortEnum(pkg.GetEcosystem().String(), "ECOSYSTEM_")
		}
	}
}

func extractSessionSummary(r *storedEvent, ss *ctv1.PmgSessionSummary) {
	if ss == nil {
		return
	}
	r.PackageManager = shortEnum(ss.GetPackageManager().String(), "PMG_PACKAGE_MANAGER_")
	r.FlowType = shortEnum(ss.GetFlowType().String(), "PMG_FLOW_TYPE_")
	r.Outcome = shortEnum(ss.GetOutcome().String(), "PMG_SESSION_OUTCOME_")
	r.TotalAnalyzed = ss.GetTotalAnalyzed()
	r.AllowedCount = ss.GetAllowedCount()
	r.BlockedCount = ss.GetBlockedCount()
	r.ConfirmedCount = ss.GetConfirmedCount()
	r.TrustedSkipped = ss.GetTrustedSkipped()
	r.CooldownBlockedCount = ss.GetCooldownBlockedCount()
	sandboxEnabled := ss.GetSandboxEnabled()
	paranoidMode := ss.GetParanoidMode()
	transitiveEnabled := ss.GetTransitiveEnabled()
	r.SandboxEnabled = &sandboxEnabled
	r.ParanoidMode = &paranoidMode
	r.TransitiveEnabled = &transitiveEnabled

	if d := ss.GetDuration(); d != nil {
		r.DurationMs = d.AsDuration().Milliseconds()
	}
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

// shortEnum strips the snake_case type prefix from a proto enum string.
// E.g. shortEnum("PMG_EVENT_TYPE_PACKAGE_DECISION", "PMG_EVENT_TYPE_") → "PACKAGE_DECISION"
// Returns empty string for zero/UNSPECIFIED values.
func shortEnum(s, prefix string) string {
	if s == "" {
		return ""
	}
	trimmed := strings.TrimPrefix(s, prefix)
	if trimmed == s {
		// prefix not found — proto default "UNSPECIFIED" names don't always carry the prefix
		if strings.Contains(s, "UNSPECIFIED") {
			return ""
		}
		return s
	}
	if trimmed == "" || strings.HasSuffix(trimmed, "UNSPECIFIED") {
		return ""
	}
	return trimmed
}
