package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newHandlerMirror builds a Handler with a mirror pointing at the given test server URL.
func newHandlerMirror(t *testing.T, srvURL string) http.Handler {
	t.Helper()
	m := newTestMirror(t, srvURL, srvURL)
	return Handler(t.TempDir(), HandlerDeps{Mirror: m})
}

func TestHandler_HealthzHandler_ReturnsOK(t *testing.T) {
	h := HealthzHandler(t.TempDir(), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		OK bool `json:"ok"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.True(t, body.OK)
}

func TestHandler_MalwareRefresh_Post_ReturnsStatus(t *testing.T) {
	srv := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(validFeedJSON))
	}))
	defer srv.Close()

	h := newHandlerMirror(t, srv.URL)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/malware/refresh", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	var status MalwareMirrorStatus
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &status))
	assert.True(t, status.NPM.OK)
	assert.True(t, status.PyPI.OK)
}

func TestHandler_MalwareRefresh_GetMethodNotAllowed(t *testing.T) {
	h := newHandlerMirror(t, "http://127.0.0.1:0")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/malware/refresh", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestHandler_MalwareRefresh_UpstreamError_ReturnsBadGateway(t *testing.T) {
	srv := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	h := newHandlerMirror(t, srv.URL)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/malware/refresh", nil))
	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

func newEnrollHandler(t *testing.T) (http.Handler, *GroupStore, *EnrollmentStore) {
	t.Helper()
	dataDir := t.TempDir()
	groups, err := NewGroupStore(dataDir)
	require.NoError(t, err)
	enrollment, err := NewEnrollmentStore(dataDir)
	require.NoError(t, err)
	h := Handler(dataDir, HandlerDeps{
		Groups:     groups,
		Enrollment: enrollment,
		Audit:      NewAuditLog(dataDir),
	})
	return h, groups, enrollment
}

func doEnroll(t *testing.T, h http.Handler, token, hostname, localIP string) map[string]any {
	t.Helper()
	body := `{"token":"` + token + `","hostname":"` + hostname + `","os":"windows","arch":"amd64","pmg_version":"0.18.10","local_ip":"` + localIP + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/enroll", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	return resp
}

func TestHandler_Enroll_ReenrollSameHostnameAndIP_UpdatesExistingAgent(t *testing.T) {
	h, groups, enrollment := newEnrollHandler(t)
	group, err := groups.CreateGroup("vega")
	require.NoError(t, err)
	plaintextToken, _, err := enrollment.CreateToken("agent", group.ID, "test", 0, 0)
	require.NoError(t, err)

	first := doEnroll(t, h, plaintextToken, "HT-PC", "169.254.27.36")
	second := doEnroll(t, h, plaintextToken, "HT-PC", "169.254.27.36")

	assert.Equal(t, first["agent_id"], second["agent_id"], "re-enroll must reuse the same agent ID")
	assert.NotEqual(t, first["api_key"], second["api_key"], "re-enroll must issue a fresh API key")

	_, _, oldKeyOK := groups.ResolveKeyWithID(first["api_key"].(string))
	assert.False(t, oldKeyOK, "old API key must be revoked after re-enroll")
	_, _, newKeyOK := groups.ResolveKeyWithID(second["api_key"].(string))
	assert.True(t, newKeyOK, "new API key must resolve")

	active := enrollment.ListAgents()
	count := 0
	for _, a := range active {
		if a.Hostname == "HT-PC" {
			count++
		}
	}
	assert.Equal(t, 1, count, "re-enroll must not create a duplicate agent row")
}

func TestHandler_Enroll_DifferentLocalIP_CreatesSeparateAgents(t *testing.T) {
	h, groups, enrollment := newEnrollHandler(t)
	group, err := groups.CreateGroup("vega")
	require.NoError(t, err)
	plaintextToken, _, err := enrollment.CreateToken("agent", group.ID, "test", 0, 0)
	require.NoError(t, err)

	first := doEnroll(t, h, plaintextToken, "HT-PC", "169.254.27.36")
	second := doEnroll(t, h, plaintextToken, "HT-PC", "192.168.99.99")

	assert.NotEqual(t, first["agent_id"], second["agent_id"], "different local IP must not be treated as the same machine")

	active := enrollment.ListAgents()
	count := 0
	for _, a := range active {
		if a.Hostname == "HT-PC" {
			count++
		}
	}
	assert.Equal(t, 2, count)
}

func TestHandler_Enroll_ReenrollPreservesAdminAssignedLabel(t *testing.T) {
	h, groups, enrollment := newEnrollHandler(t)
	group, err := groups.CreateGroup("vega")
	require.NoError(t, err)
	plaintextToken, _, err := enrollment.CreateToken("agent", group.ID, "test", 0, 0)
	require.NoError(t, err)

	first := doEnroll(t, h, plaintextToken, "HT-PC", "169.254.27.36")
	require.NoError(t, enrollment.SetAgentLabel(first["agent_id"].(string), "HC-Hieu"))

	doEnroll(t, h, plaintextToken, "HT-PC", "169.254.27.36")

	updated, ok := enrollment.GetAgentByID(first["agent_id"].(string))
	require.True(t, ok)
	assert.Equal(t, "HC-Hieu", updated.Label)
}
