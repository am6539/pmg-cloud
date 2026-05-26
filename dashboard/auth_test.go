package dashboard

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestBasicAuth_NoCredentials_PassThrough(t *testing.T) {
	h := BasicAuthMiddleware(okHandler(), "", "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestBasicAuth_ValidCredentials_Allowed(t *testing.T) {
	h := BasicAuthMiddleware(okHandler(), "admin", "secret")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("admin", "secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestBasicAuth_WrongPassword_Rejected(t *testing.T) {
	h := BasicAuthMiddleware(okHandler(), "admin", "secret")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("admin", "wrong")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, `Basic realm="pmg-cloud"`, rec.Header().Get("WWW-Authenticate"))
}

func TestBasicAuth_WrongUser_Rejected(t *testing.T) {
	h := BasicAuthMiddleware(okHandler(), "admin", "secret")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("hacker", "secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestBasicAuth_MissingAuthHeader_Rejected(t *testing.T) {
	h := BasicAuthMiddleware(okHandler(), "admin", "secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
