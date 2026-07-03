package dashboard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestScan_SetsFlagAndPendingState(t *testing.T) {
	es := newTestEnrollmentStore(t)
	require.NoError(t, es.RegisterAgent(Agent{ID: "agent-1", APIKeyID: "key-1"}))

	require.NoError(t, es.RequestScan("agent-1"))

	agent, ok := es.GetAgentByID("agent-1")
	require.True(t, ok)
	assert.True(t, agent.ScanRequested)
	assert.Equal(t, "pending", agent.ScanState)
}

func TestRequestScan_UnknownAgentReturnsError(t *testing.T) {
	es := newTestEnrollmentStore(t)
	err := es.RequestScan("does-not-exist")
	assert.Error(t, err)
}

func TestConsumeScanRequest_ClearsFlagAndDispatches(t *testing.T) {
	es := newTestEnrollmentStore(t)
	require.NoError(t, es.RegisterAgent(Agent{ID: "agent-1", APIKeyID: "key-1"}))
	require.NoError(t, es.RequestScan("agent-1"))

	dispatched := es.ConsumeScanRequest("key-1")
	assert.True(t, dispatched)

	agent, _ := es.GetAgentByID("agent-1")
	assert.False(t, agent.ScanRequested)
	assert.Equal(t, "dispatched", agent.ScanState)
	assert.NotNil(t, agent.ScanDispatchedAt)
}

func TestConsumeScanRequest_FireOnce(t *testing.T) {
	es := newTestEnrollmentStore(t)
	require.NoError(t, es.RegisterAgent(Agent{ID: "agent-1", APIKeyID: "key-1"}))
	require.NoError(t, es.RequestScan("agent-1"))

	require.True(t, es.ConsumeScanRequest("key-1"))
	assert.False(t, es.ConsumeScanRequest("key-1"), "a second poll must not re-dispatch")
}

func TestConsumeScanRequest_NoPendingRequestReturnsFalse(t *testing.T) {
	es := newTestEnrollmentStore(t)
	require.NoError(t, es.RegisterAgent(Agent{ID: "agent-1", APIKeyID: "key-1"}))

	assert.False(t, es.ConsumeScanRequest("key-1"))
}

func TestRecordScanStarted_SetsRunningState(t *testing.T) {
	es := newTestEnrollmentStore(t)
	require.NoError(t, es.RegisterAgent(Agent{ID: "agent-1", APIKeyID: "key-1"}))

	require.NoError(t, es.RecordScanStarted("key-1"))

	agent, _ := es.GetAgentByID("agent-1")
	assert.Equal(t, "running", agent.ScanState)
}

func TestRecordScanCompleted_StoresFindingsAndSummary(t *testing.T) {
	es := newTestEnrollmentStore(t)
	require.NoError(t, es.RegisterAgent(Agent{ID: "agent-1", APIKeyID: "key-1", Hostname: "HT-PC", OS: "windows"}))

	findings := []EcosystemFinding{{
		Ecosystem: "npm", Name: "evil-pkg", Version: "6.6.6",
		Verdict: "known malware", Paths: []string{"/a/node_modules/evil-pkg"},
		RemoveHint: "npm uninstall evil-pkg",
	}}
	summary := EcosystemScanSummary{TotalPathsScanned: 10, UniquePackages: 5, FlaggedCount: 1}

	require.NoError(t, es.RecordScanCompleted("key-1", findings, summary))

	agent, _ := es.GetAgentByID("agent-1")
	assert.Equal(t, "completed", agent.ScanState)
	require.NotNil(t, agent.LastScanAt)
	require.NotNil(t, agent.LastScanSummary)
	assert.Equal(t, 1, agent.LastScanSummary.FlaggedCount)
	require.Len(t, agent.Findings, 1)
	assert.Equal(t, "evil-pkg", agent.Findings[0].Name)
}

func TestRecordScanCompleted_ReplacesPreviousFindings(t *testing.T) {
	es := newTestEnrollmentStore(t)
	require.NoError(t, es.RegisterAgent(Agent{ID: "agent-1", APIKeyID: "key-1"}))

	require.NoError(t, es.RecordScanCompleted("key-1",
		[]EcosystemFinding{{Name: "old-finding"}}, EcosystemScanSummary{}))
	require.NoError(t, es.RecordScanCompleted("key-1",
		[]EcosystemFinding{{Name: "new-finding"}}, EcosystemScanSummary{}))

	agent, _ := es.GetAgentByID("agent-1")
	require.Len(t, agent.Findings, 1)
	assert.Equal(t, "new-finding", agent.Findings[0].Name)
}

func TestListEcosystemFindings_EnrichesWithAgentIdentityAndSkipsRemoved(t *testing.T) {
	es := newTestEnrollmentStore(t)
	require.NoError(t, es.RegisterAgent(Agent{ID: "agent-1", APIKeyID: "key-1", Hostname: "HT-PC", OS: "windows"}))
	require.NoError(t, es.RegisterAgent(Agent{ID: "agent-2", APIKeyID: "key-2", Hostname: "removed-box", Removed: true}))

	require.NoError(t, es.RecordScanCompleted("key-1",
		[]EcosystemFinding{{Ecosystem: "npm", Name: "evil-pkg", Version: "6.6.6"}},
		EcosystemScanSummary{}))
	require.NoError(t, es.RecordScanCompleted("key-2",
		[]EcosystemFinding{{Ecosystem: "npm", Name: "should-not-appear", Version: "1.0.0"}},
		EcosystemScanSummary{}))

	views := es.ListEcosystemFindings()
	require.Len(t, views, 1)
	assert.Equal(t, "HT-PC", views[0].Hostname)
	assert.Equal(t, "windows", views[0].OS)
	assert.Equal(t, "evil-pkg", views[0].Name)
}

func TestEcosystemFleetSummaryStats_AggregatesAcrossAgents(t *testing.T) {
	es := newTestEnrollmentStore(t)
	require.NoError(t, es.RegisterAgent(Agent{ID: "agent-1", APIKeyID: "key-1"}))
	require.NoError(t, es.RegisterAgent(Agent{ID: "agent-2", APIKeyID: "key-2"}))

	require.NoError(t, es.RecordScanCompleted("key-1",
		[]EcosystemFinding{{Name: "a"}, {Name: "b"}}, EcosystemScanSummary{}))
	require.NoError(t, es.RecordScanCompleted("key-2",
		[]EcosystemFinding{{Name: "c"}}, EcosystemScanSummary{}))

	summary := es.EcosystemFleetSummaryStats()
	assert.Equal(t, 2, summary.AgentsScanned)
	assert.Equal(t, 3, summary.TotalFindings)
	require.NotNil(t, summary.LastScanAt)
}
