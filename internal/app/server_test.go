package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewHandlerServesHealth(t *testing.T) {
	handler, err := NewHandlerForConfig(t.Context(), testConfig(t))
	if err != nil {
		t.Fatalf("NewHandlerForConfig error: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) != `{"ok":true}` {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestNewHandlerServesIndex(t *testing.T) {
	handler, err := NewHandlerForConfig(t.Context(), testConfig(t))
	if err != nil {
		t.Fatalf("NewHandlerForConfig error: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "revu") {
		t.Fatalf("index body does not contain revu: %q", rec.Body.String())
	}
}

func TestNewHandlerForConfigServesSeededThreads(t *testing.T) {
	handler, err := NewHandlerForConfig(t.Context(), testConfig(t))
	if err != nil {
		t.Fatalf("NewHandlerForConfig error: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/threads", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "id") {
		t.Fatalf("threads body does not look seeded: %q", rec.Body.String())
	}
}

func TestNewHandlerAddsSecurityHeaders(t *testing.T) {
	handler, err := NewHandlerForConfig(t.Context(), testConfig(t))
	if err != nil {
		t.Fatalf("NewHandlerForConfig error: %v", err)
	}
	for _, path := range []string{"/", "/app.js", "/api/health"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, rec.Code)
		}
		csp := rec.Header().Get("Content-Security-Policy")
		for _, expected := range []string{
			"default-src 'self'",
			"script-src 'self' 'unsafe-inline'",
			"style-src 'self' 'unsafe-inline'",
			"img-src 'self' data: blob:",
			"font-src 'self'",
			"connect-src 'self'",
			"object-src 'none'",
			"base-uri 'none'",
			"frame-ancestors 'none'",
		} {
			if !strings.Contains(csp, expected) {
				t.Fatalf("%s CSP %q missing %q", path, csp, expected)
			}
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s missing X-Content-Type-Options nosniff", path)
		}
		if rec.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatalf("%s missing Referrer-Policy no-referrer", path)
		}
		if rec.Header().Get("X-Frame-Options") != "DENY" {
			t.Fatalf("%s missing X-Frame-Options DENY", path)
		}
	}
}

func testConfig(t *testing.T) Config {
	t.Helper()
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.MaxUploadBytes = 1024
	return cfg
}
