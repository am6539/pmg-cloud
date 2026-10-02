package dashboard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUnifiedEndpointsStaticContract verifies the embedded dashboard HTML has
// been migrated from the separate Agents tab to a single unified Endpoints
// page: the Agents nav/render path is gone, the legacy #agents hash is
// normalized to #endpoints, and the unified renderer with Managed/Observed
// classification and the telemetry-deletion endpoint are present.
func TestUnifiedEndpointsStaticContract(t *testing.T) {
	data, err := staticFiles.ReadFile("static/index.html")
	require.NoError(t, err)
	html := string(data)
	assert.NotContains(t, html, `nav('agents')`)
	assert.Contains(t, html, `if(hashPage==='agents')hashPage='endpoints'`)
	assert.Contains(t, html, `function renderEndpoints()`)
	assert.Contains(t, html, `Managed`)
	assert.Contains(t, html, `Observed`)
	assert.Contains(t, html, `/api/endpoints/`)
	assert.Contains(t, html, `function openEnrollmentTokens()`)
	assert.Contains(t, html, `function deleteEndpointEvents(endpointId)`)
	assert.Contains(t, html, `'/api/agents/'+encodeURIComponent(ep.agent_id)`)
	assert.Contains(t, html, `'/api/endpoints/'+encodeURIComponent(endpointId)+'/events'`)
	assert.Contains(t, html, `overflow-x:auto`)
}
