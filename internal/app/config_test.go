package app

import "testing"

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Addr != "0.0.0.0:8080" {
		t.Fatalf("Addr = %q, want 0.0.0.0:8080", cfg.Addr)
	}
	if cfg.DataDir != "data" {
		t.Fatalf("DataDir = %q, want data", cfg.DataDir)
	}
	if cfg.MaxUploadBytes != 10*1024*1024 {
		t.Fatalf("MaxUploadBytes = %d, want 10 MiB", cfg.MaxUploadBytes)
	}
}

func TestConfigWithOverrides(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Addr = "127.0.0.1:9090"
	cfg.DataDir = t.TempDir()
	cfg.MaxUploadBytes = 1024
	if cfg.Addr != "127.0.0.1:9090" || cfg.DataDir == "" || cfg.MaxUploadBytes != 1024 {
		t.Fatalf("override config not preserved: %+v", cfg)
	}
}
