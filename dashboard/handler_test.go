package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newHandlerMirror builds a Handler with a mirror pointing at the given test server URL.
func newHandlerMirror(t *testing.T, srvURL string) http.Handler {
	t.Helper()
	m := newTestMirror(t, srvURL, srvURL)
	return Handler(t.TempDir(), m)
}

func TestHandler_HealthzHandler_ReturnsOK(t *testing.T) {
	h := HealthzHandler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]bool
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.True(t, body["ok"])
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
