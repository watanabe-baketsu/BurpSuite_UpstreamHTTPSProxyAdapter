package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := Default()
	if cfg.Local.BindHost != "127.0.0.1" {
		t.Errorf("expected bind host 127.0.0.1, got %s", cfg.Local.BindHost)
	}
	if cfg.Local.BindPort != 18080 {
		t.Errorf("expected bind port 18080, got %d", cfg.Local.BindPort)
	}
	if cfg.Upstream.VerifyTLS != true {
		t.Error("expected verify_tls true by default")
	}
}

func TestValidateSuccess(t *testing.T) {
	cfg := Default()
	cfg.Upstream.Host = "proxy.example.com"
	cfg.Upstream.Port = 3128
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateMissingHost(t *testing.T) {
	cfg := Default()
	cfg.Upstream.Host = ""
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for missing host")
	}
}

func TestValidateInvalidPort(t *testing.T) {
	cfg := Default()
	cfg.Upstream.Host = "example.com"
	cfg.Upstream.Port = 0
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for port 0")
	}

	cfg.Upstream.Port = 70000
	err = cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for port 70000")
	}
}

func TestValidateInvalidBindHost(t *testing.T) {
	cfg := Default()
	cfg.Upstream.Host = "example.com"
	cfg.Local.BindHost = "not-an-ip"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for invalid bind host")
	}
}

func TestValidateInvalidTimeout(t *testing.T) {
	cfg := Default()
	cfg.Upstream.Host = "example.com"
	cfg.Upstream.ConnectTimeout = 0
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for zero timeout")
	}
}

func TestValidateCustomCANotFound(t *testing.T) {
	cfg := Default()
	cfg.Upstream.Host = "example.com"
	cfg.Upstream.CustomCAPath = "/nonexistent/ca.pem"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for missing CA file")
	}
}

func TestUpstreamAddr(t *testing.T) {
	cfg := Default()
	cfg.Upstream.Host = "proxy.example.com"
	cfg.Upstream.Port = 3128
	want := "proxy.example.com:3128"
	if got := cfg.UpstreamAddr(); got != want {
		t.Errorf("UpstreamAddr() = %s, want %s", got, want)
	}
}

func TestLocalAddr(t *testing.T) {
	cfg := Default()
	want := "127.0.0.1:18080"
	if got := cfg.LocalAddr(); got != want {
		t.Errorf("LocalAddr() = %s, want %s", got, want)
	}
}

func TestSaveLoad(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cfg := Default()
	cfg.Upstream.Host = "test-proxy.example.com"
	cfg.Upstream.Port = 9999

	if err := Save(cfg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify the file exists
	path := filepath.Join(tmp, ".burp-upstream-adapter", DefaultConfigFile)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file not found: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded.Upstream.Host != "test-proxy.example.com" {
		t.Errorf("expected host test-proxy.example.com, got %s", loaded.Upstream.Host)
	}
	if loaded.Upstream.Port != 9999 {
		t.Errorf("expected port 9999, got %d", loaded.Upstream.Port)
	}
}

func TestLoadReturnsDefaultWhenMissing(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Local.BindHost != "127.0.0.1" {
		t.Error("expected default bind host")
	}
}
