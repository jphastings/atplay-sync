package api

import (
	"context"
	"net/http"
)

type contextKey string

const didContextKey contextKey = "did"

func DIDFromContext(ctx context.Context) (string, bool) {
	did, ok := ctx.Value(didContextKey).(string)
	return did, ok
}

func (h *OAuthHandlers) RequireAuth(next func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			http.Error(w, "not signed in", http.StatusUnauthorized)
			return
		}
		did, err := h.Cookies.Decode(cookie.Value)
		if err != nil {
			http.Error(w, "not signed in", http.StatusUnauthorized)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), didContextKey, did)))
	}
}
