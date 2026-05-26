package dashboard

import (
	"crypto/subtle"
	"net/http"
)

// BasicAuthMiddleware wraps h with HTTP Basic Auth.
// When user and pass are both empty the original handler is returned unchanged.
func BasicAuthMiddleware(h http.Handler, user, pass string) http.Handler {
	if user == "" && pass == "" {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(u), []byte(user)) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), []byte(pass)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="pmg-cloud"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}
