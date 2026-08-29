package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jphastings/game-status/internal/webauth"
)

func TestStateCookieMatches(t *testing.T) {
	h := &OAuthHandlers{Cookies: webauth.SignedCookies{Secret: []byte("secret-a")}}
	other := webauth.SignedCookies{Secret: []byte("secret-b")}

	tests := []struct {
		name   string
		cookie string
		query  string
		want   bool
	}{
		{"same browser, same state", h.Cookies.EncodeWithTTL("state-1", oauthStateTTL), "state-1", true},
		{"no cookie", "", "state-1", false},
		{"attacker's callback delivered to another browser", h.Cookies.EncodeWithTTL("state-1", oauthStateTTL), "state-2", false},
		{"cookie signed with another secret", other.EncodeWithTTL("state-1", oauthStateTTL), "state-1", false},
		{"tampered cookie", h.Cookies.EncodeWithTTL("state-1", oauthStateTTL) + "0", "state-1", false},
		{"no state in callback", h.Cookies.EncodeWithTTL("state-1", oauthStateTTL), "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/oauth/callback?state="+tt.query, nil)
			if tt.cookie != "" {
				r.AddCookie(&http.Cookie{Name: oauthStateCookieName, Value: tt.cookie})
			}
			if got := h.stateCookieMatches(r); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
