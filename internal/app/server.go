package app

import (
	"context"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/po-oq/revu/internal/api"
	"github.com/po-oq/revu/internal/store"
	"github.com/po-oq/revu/internal/uploads"
	"github.com/po-oq/revu/internal/web"
)

// script-src allows inline scripts because iframe srcdoc inherits the parent CSP,
// and revu intentionally supports scripts inside sandboxed HTML previews. External
// scripts remain blocked by the absence of external sources, and the iframe still
// has no allow-same-origin.
const contentSecurityPolicy = "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'"

func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func NewHandler() (http.Handler, error) {
	return NewHandlerForConfig(context.Background(), DefaultConfig())
}

func NewHandlerForConfig(ctx context.Context, cfg Config) (http.Handler, error) {
	staticFS, err := fs.Sub(web.Files, "static")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, err
	}
	s, err := store.Open(ctx, filepath.Join(cfg.DataDir, "revu.sqlite"))
	if err != nil {
		return nil, err
	}
	if err := s.SeedIfEmpty(ctx); err != nil {
		_ = s.Close()
		return nil, err
	}
	storage := uploads.NewStorage(filepath.Join(cfg.DataDir, "uploads"), cfg.MaxUploadBytes)

	mux := http.NewServeMux()
	mux.Handle("/api/", api.NewHandler(s, storage))
	mux.Handle("/", http.FileServer(http.FS(staticFS)))
	return withSecurityHeaders(mux), nil
}
