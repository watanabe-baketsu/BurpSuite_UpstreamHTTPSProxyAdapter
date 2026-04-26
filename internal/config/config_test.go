package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func validProfile() ProfileConfig {
	p := DefaultProfile()
	p.Host = "proxy.example.com"
	return p
}

func TestDefaultConfig(t *testing.T) {
	cfg := Default()
	if cfg.Local.BindHost != "127.0.0.1" {
		t.Errorf("expected bind host 127.0.0.1, got %s", cfg.Local.BindHost)
	}
	if cfg.Local.BindPort != 18080 {
		t.Errorf("expected bind port 18080, got %d", cfg.Local.BindPort)
	}
	if cfg.ActiveProfile != DefaultProfileName {
		t.Errorf("expected active profile %q, got %q", DefaultProfileName, cfg.ActiveProfile)
	}
	if _, ok := cfg.Profiles[DefaultProfileName]; !ok {
		t.Fatalf("expected a default profile to exist")
	}
	if !cfg.Profiles[DefaultProfileName].VerifyTLS {
		t.Error("expected verify_tls true by default")
	}
}

func TestValidateSuccess(t *testing.T) {
	cfg := Default()
	cfg.Profiles[DefaultProfileName] = validProfile()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateMissingHost(t *testing.T) {
	cfg := Default()
	// default profile already has empty host
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for missing host")
	}
}

func TestValidateInvalidPort(t *testing.T) {
	cfg := Default()
	p := validProfile()
	p.Port = 0
	cfg.Profiles[DefaultProfileName] = p
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for port 0")
	}

	p.Port = 70000
	cfg.Profiles[DefaultProfileName] = p
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for port 70000")
	}
}

func TestValidateInvalidBindHost(t *testing.T) {
	cfg := Default()
	cfg.Profiles[DefaultProfileName] = validProfile()
	cfg.Local.BindHost = "not-an-ip"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for invalid bind host")
	}
}

func TestValidateInvalidTimeout(t *testing.T) {
	cfg := Default()
	p := validProfile()
	p.ConnectTimeout = 0
	cfg.Profiles[DefaultProfileName] = p
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for zero timeout")
	}
}

func TestValidateInvalidCustomCAPEM(t *testing.T) {
	cfg := Default()
	p := validProfile()
	p.CustomCAPEM = "not a valid PEM"
	cfg.Profiles[DefaultProfileName] = p
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for invalid PEM")
	}
}

func TestValidateActiveProfileMissing(t *testing.T) {
	cfg := Default()
	cfg.Profiles[DefaultProfileName] = validProfile()
	cfg.ActiveProfile = "nonexistent"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for missing active profile")
	}
}

func TestValidateProfileName(t *testing.T) {
	cases := map[string]bool{
		"default":                                 true,
		"staging-east":                            true,
		"prod_1":                                  true,
		"":                                        false,
		"with space":                              false,
		"with/slash":                              false,
		"日本語":                                     false,
		"thisisareallylongnamethatexceedsthirtytwochars": false,
	}
	for name, wantOK := range cases {
		err := ValidateProfileName(name)
		if wantOK && err != nil {
			t.Errorf("expected %q to be valid, got error: %v", name, err)
		}
		if !wantOK && err == nil {
			t.Errorf("expected %q to be invalid, but Validate returned nil", name)
		}
	}
}

func TestUpstreamAddr(t *testing.T) {
	p := validProfile()
	p.Port = 3128
	want := "proxy.example.com:3128"
	if got := p.UpstreamAddr(); got != want {
		t.Errorf("UpstreamAddr() = %s, want %s", got, want)
	}
}

// setHomeDir overrides the home directory for the duration of the test.
// On Windows os.UserHomeDir reads USERPROFILE, on Unix it reads HOME.
func setHomeDir(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", dir)
	}
}

func TestSaveLoad(t *testing.T) {
	tmp := t.TempDir()
	setHomeDir(t, tmp)

	cfg := Default()
	p := validProfile()
	p.Host = "test-proxy.example.com"
	p.Port = 9999
	cfg.Profiles[DefaultProfileName] = p

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

	got, err := loaded.Active()
	if err != nil {
		t.Fatalf("Active() failed: %v", err)
	}
	if got.Host != "test-proxy.example.com" {
		t.Errorf("expected host test-proxy.example.com, got %s", got.Host)
	}
	if got.Port != 9999 {
		t.Errorf("expected port 9999, got %d", got.Port)
	}
}

func TestLoadReturnsDefaultWhenMissing(t *testing.T) {
	tmp := t.TempDir()
	setHomeDir(t, tmp)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Local.BindHost != "127.0.0.1" {
		t.Error("expected default bind host")
	}
	if cfg.ActiveProfile != DefaultProfileName {
		t.Errorf("expected active profile %q, got %q", DefaultProfileName, cfg.ActiveProfile)
	}
}

func TestLoadMigratesLegacyFormat(t *testing.T) {
	tmp := t.TempDir()
	setHomeDir(t, tmp)

	// Write a legacy single-profile config file.
	cfgDir := filepath.Join(tmp, ".burp-upstream-adapter")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	legacy := `{
  "upstream": {
    "host": "legacy.example.com",
    "port": 8443,
    "username": "legacy-user",
    "verify_tls": false,
    "connect_timeout_sec": 15,
    "idle_timeout_sec": 120
  },
  "local": {
    "bind_host": "127.0.0.1",
    "bind_port": 19000
  }
}`
	if err := os.WriteFile(filepath.Join(cfgDir, DefaultConfigFile), []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.ActiveProfile != DefaultProfileName {
		t.Errorf("expected active profile %q, got %q", DefaultProfileName, cfg.ActiveProfile)
	}
	p, err := cfg.Active()
	if err != nil {
		t.Fatal(err)
	}
	if p.Host != "legacy.example.com" {
		t.Errorf("expected migrated host, got %s", p.Host)
	}
	if p.Port != 8443 {
		t.Errorf("expected migrated port 8443, got %d", p.Port)
	}
	if p.Username != "legacy-user" {
		t.Errorf("expected migrated username, got %s", p.Username)
	}
	if cfg.Local.BindPort != 19000 {
		t.Errorf("expected migrated bind port, got %d", cfg.Local.BindPort)
	}
}
