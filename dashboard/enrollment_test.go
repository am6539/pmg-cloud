package dashboard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestEnrollmentStore(t *testing.T) *EnrollmentStore {
	t.Helper()
	es, err := NewEnrollmentStore(t.TempDir())
	require.NoError(t, err)
	return es
}

func TestEnrollmentStore_FindActiveAgentByHostnameAndIP_MatchesCaseInsensitiveHostname(t *testing.T) {
	es := newTestEnrollmentStore(t)
	require.NoError(t, es.RegisterAgent(Agent{
		ID:       "agent-1",
		Hostname: "HT-PC",
		LocalIP:  "169.254.27.36",
	}))

	found, ok := es.FindActiveAgentByHostnameAndIP("ht-pc", "169.254.27.36")
	require.True(t, ok)
	assert.Equal(t, "agent-1", found.ID)
}

func TestEnrollmentStore_FindActiveAgentByHostnameAndIP_NoMatchWhenIPDiffers(t *testing.T) {
	es := newTestEnrollmentStore(t)
	require.NoError(t, es.RegisterAgent(Agent{
		ID:       "agent-1",
		Hostname: "HT-PC",
		LocalIP:  "169.254.27.36",
	}))

	_, ok := es.FindActiveAgentByHostnameAndIP("HT-PC", "192.168.99.99")
	assert.False(t, ok)
}

func TestEnrollmentStore_FindActiveAgentByHostnameAndIP_NoMatchWhenRemoved(t *testing.T) {
	es := newTestEnrollmentStore(t)
	require.NoError(t, es.RegisterAgent(Agent{
		ID:       "agent-1",
		Hostname: "HT-PC",
		LocalIP:  "169.254.27.36",
		Removed:  true,
	}))

	_, ok := es.FindActiveAgentByHostnameAndIP("HT-PC", "169.254.27.36")
	assert.False(t, ok)
}

func TestEnrollmentStore_FindActiveAgentByHostnameAndIP_NoMatchWhenLocalIPEmpty(t *testing.T) {
	es := newTestEnrollmentStore(t)
	require.NoError(t, es.RegisterAgent(Agent{
		ID:       "agent-1",
		Hostname: "HT-PC",
		LocalIP:  "169.254.27.36",
	}))

	_, ok := es.FindActiveAgentByHostnameAndIP("HT-PC", "")
	assert.False(t, ok)
}

func TestEnrollmentStore_ReenrollAgent_UpdatesFieldsKeepsIDAndLabel(t *testing.T) {
	es := newTestEnrollmentStore(t)
	require.NoError(t, es.RegisterAgent(Agent{
		ID:         "agent-1",
		Hostname:   "HT-PC",
		LocalIP:    "169.254.27.36",
		OS:         "windows",
		Arch:       "amd64",
		PMGVersion: "0.18.9",
		GroupID:    "group-old",
		APIKeyID:   "key-old",
	}))
	require.NoError(t, es.SetAgentLabel("agent-1", "HC-Hieu"))

	err := es.ReenrollAgent("agent-1", "windows", "amd64", "0.18.10", "113.190.252.218", "169.254.27.36", "group-new", "key-new")
	require.NoError(t, err)

	updated, ok := es.GetAgentByID("agent-1")
	require.True(t, ok)
	assert.Equal(t, "agent-1", updated.ID)
	assert.Equal(t, "HC-Hieu", updated.Label, "label must survive re-enrollment")
	assert.Equal(t, "0.18.10", updated.PMGVersion)
	assert.Equal(t, "113.190.252.218", updated.RemoteIP)
	assert.Equal(t, "169.254.27.36", updated.LocalIP)
	assert.Equal(t, "group-new", updated.GroupID)
	assert.Equal(t, "key-new", updated.APIKeyID)
}

func TestEnrollmentStore_ReenrollAgent_ErrorsWhenNotFound(t *testing.T) {
	es := newTestEnrollmentStore(t)
	err := es.ReenrollAgent("does-not-exist", "windows", "amd64", "0.18.10", "1.2.3.4", "10.0.0.1", "group-new", "key-new")
	assert.Error(t, err)
}
