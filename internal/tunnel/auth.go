package tunnel

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// Check returns true if the request carries credentials matching this
// auth config. nil receiver means "no auth required" and always passes.
func (a *RouteAuth) Check(r *http.Request) bool {
	if a == nil {
		return true
	}
	if a.Bearer != "" {
		const prefix = "Bearer "
		got := r.Header.Get("Authorization")
		if !strings.HasPrefix(got, prefix) {
			return false
		}
		return subtle.ConstantTimeCompare([]byte(got[len(prefix):]), []byte(a.Bearer)) == 1
	}
	if a.Header != "" {
		got := r.Header.Get(a.Header)
		return subtle.ConstantTimeCompare([]byte(got), []byte(a.Value)) == 1
	}
	return true
}

// Scheme returns a short label for UI/logs ("bearer", "X-API-Key", or "").
func (a *RouteAuth) Scheme() string {
	if a == nil {
		return ""
	}
	if a.Bearer != "" {
		return "bearer"
	}
	return a.Header
}

// WriteUnauthorized sends a 401 with an appropriate WWW-Authenticate header.
func (a *RouteAuth) WriteUnauthorized(w http.ResponseWriter) {
	if a != nil && a.Bearer != "" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="etunl"`)
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}
