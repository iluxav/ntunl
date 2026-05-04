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

// Scheme returns a short label for UI/logs ("bearer", "session",
// "X-API-Key", or "").
func (a *RouteAuth) Scheme() string {
	if a == nil {
		return ""
	}
	if a.Bearer != "" {
		return "bearer"
	}
	if len(a.Users) > 0 {
		return "session"
	}
	return a.Header
}

// HasSession reports whether this route uses cookie-session auth.
// The caller is responsible for cookie validation; Check ignores Users.
func (a *RouteAuth) HasSession() bool {
	return a != nil && len(a.Users) > 0
}

// LookupUser returns the password for a given user, or "" if not found.
func (a *RouteAuth) LookupUser(user string) string {
	if a == nil {
		return ""
	}
	for _, u := range a.Users {
		if u.User == user {
			return u.Password
		}
	}
	return ""
}

// WriteUnauthorized sends a 401 with an appropriate WWW-Authenticate header.
func (a *RouteAuth) WriteUnauthorized(w http.ResponseWriter) {
	if a != nil && a.Bearer != "" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="etunl"`)
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}
