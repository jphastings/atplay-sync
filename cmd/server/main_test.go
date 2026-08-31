package main

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFrontendServedFromDistRoot(t *testing.T) {
	distFS, err := fs.Sub(frontendFS, "web/dist")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}

	rec := httptest.NewRecorder()
	http.FileServerFS(distFS).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	body := rec.Body.String()
	if !strings.Contains(body, "<title>At Play Sync</title>") {
		t.Fatalf("expected built index.html content, got directory listing or empty response:\n%s", body)
	}
}
