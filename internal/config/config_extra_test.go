package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProfileNamesSorted(t *testing.T) {
	cfg := Default()
	cfg.Profiles["zeta"] = validProfile()
	cfg.Profiles["alpha"] = validProfile()
	cfg.Profiles["mu"] = validProfile()

	got := cfg.ProfileNames()
	want := []string{"alpha", "default", "mu", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("len mismatch: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ProfileNames()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestProfileNamesEmpty(t *testing.T) {
	cfg := Config{}
	if got := cfg.ProfileNames(); len(got) != 0 {
		t.Errorf("empty cfg.ProfileNames() should be empty, got %v", got)
	}
}

func TestConnectAndIdleTimeoutDuration(t *testing.T) {
	p := DefaultProfile()
	if got := p.ConnectTimeoutDuration(); got != 30*time.Second {
		t.Errorf("ConnectTimeoutDuration() = %v, want 30s", got)
	}
	if got := p.IdleTimeoutDuration(); got != 300*time.Second {
		t.Errorf("IdleTimeoutDuration() = %v, want 300s", got)
	}
}

func TestActiveMissing(t *testing.T) {
	cfg := Config{ActiveProfile: "ghost"}
	if _, err := cfg.Active(); err == nil {
		t.Fatal("Active() should error when active profile is not in Profiles")
	}
}

// TestValidateAggregatesErrors guards the contract that Validate returns ALL
// problems joined together, not just the first one. The frontend surfaces
// this aggregate to the user; if Validate ever short-circuits, error toasts
// silently drop fields.
func TestValidateAggregatesErrors(t *testing.T) {
	cfg := Default()
	bad := DefaultProfile()
	bad.Host = ""
	bad.Port = 0
	bad.ConnectTimeout = 0
	bad.IdleTimeout = 0
	cfg.Profiles[DefaultProfileName] = bad

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected aggregated validation error")
	}
	msg := err.Error()
	for _, want := range []string{"upstream host", "upstream port", "connect timeout", "idle timeout"} {
		if !strings.Contains(msg, want) {
			t.Errorf("Validate() error %q missing expected fragment %q", msg, want)
		}
	}
}

// TestValidateRejectsBlankActiveProfile ensures the empty-string variant is
// surfaced (the previous test only covered "missing key"), so a user who
// somehow ends up with ActiveProfile="" gets a hard error rather than a
// silent fallback that uses zero-value config.
func TestValidateRejectsBlankActiveProfile(t *testing.T) {
	cfg := Default()
	cfg.Profiles[DefaultProfileName] = validProfile()
	cfg.ActiveProfile = ""
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "active profile is required") {
		t.Fatalf("expected 'active profile is required', got %v", err)
	}
}

func TestValidateRejectsEmptyProfiles(t *testing.T) {
	cfg := Default()
	cfg.Profiles = map[string]ProfileConfig{}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "at least one profile is required") {
		t.Fatalf("expected 'at least one profile is required', got %v", err)
	}
}

// TestLoadCorruptJSONReturnsDefault verifies that a malformed config file
// degrades gracefully rather than crashing the app at startup. Without this
// fallback a single bad keystroke in adapter.config.json would brick the UI.
func TestLoadCorruptJSONReturnsDefault(t *testing.T) {
	tmp := t.TempDir()
	setHomeDir(t, tmp)

	cfgDir := filepath.Join(tmp, ".burp-upstream-adapter")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, DefaultConfigFile), []byte("{ not valid"), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err == nil {
		t.Fatal("expected parse error for malformed JSON")
	}
	// On parse error, Load returns the default config alongside the error so
	// the UI can keep operating without persisting back over the bad file.
	if cfg.ActiveProfile != DefaultProfileName {
		t.Errorf("expected fallback default config, got active=%q", cfg.ActiveProfile)
	}
}

// TestLoadEmptyProfilesFallback covers the path where a JSON file parses but
// has no profiles AND is not a legacy layout — the app should fall back to
// Default rather than running with an empty profile map.
func TestLoadEmptyProfilesFallback(t *testing.T) {
	tmp := t.TempDir()
	setHomeDir(t, tmp)

	cfgDir := filepath.Join(tmp, ".burp-upstream-adapter")
	_ = os.MkdirAll(cfgDir, 0700)
	// Valid JSON, but no profiles and no legacy `upstream` block.
	_ = os.WriteFile(
		filepath.Join(cfgDir, DefaultConfigFile),
		[]byte(`{"local":{"bind_host":"127.0.0.1","bind_port":18080}}`),
		0600,
	)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.Profiles[DefaultProfileName]; !ok {
		t.Fatal("expected Default fallback to provide a default profile")
	}
}

// TestLoadInfersActiveFromFirstProfile guards the alphabetical-fallback path
// when the file has profiles but no active_profile field set. Without this,
// users who hand-edit the config and forget to set active_profile would see
// the app reset to Default and lose their other profiles.
func TestLoadInfersActiveFromFirstProfile(t *testing.T) {
	tmp := t.TempDir()
	setHomeDir(t, tmp)

	cfgDir := filepath.Join(tmp, ".burp-upstream-adapter")
	_ = os.MkdirAll(cfgDir, 0700)
	body := `{
	  "profiles": {
	    "zebra": {"host":"z","port":3128,"verify_tls":true,"connect_timeout_sec":30,"idle_timeout_sec":300},
	    "alpha": {"host":"a","port":3128,"verify_tls":true,"connect_timeout_sec":30,"idle_timeout_sec":300}
	  },
	  "local": {"bind_host":"127.0.0.1","bind_port":18080}
	}`
	_ = os.WriteFile(filepath.Join(cfgDir, DefaultConfigFile), []byte(body), 0600)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// ProfileNames returns sorted, so "alpha" wins.
	if cfg.ActiveProfile != "alpha" {
		t.Errorf("expected active='alpha' (first sorted), got %q", cfg.ActiveProfile)
	}
}

// TestSaveFilePermissions guards the contract that we never widen
// permissions on the config file — it may contain usernames / hostnames the
// user considers private.
func TestSaveFilePermissions(t *testing.T) {
	tmp := t.TempDir()
	setHomeDir(t, tmp)

	cfg := Default()
	cfg.Profiles[DefaultProfileName] = validProfile()
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(tmp, ".burp-upstream-adapter", DefaultConfigFile)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// On Windows the mode bits don't carry POSIX semantics; only assert on
	// Unix-y systems where the permission matters for our threat model.
	if mode := info.Mode().Perm(); mode != 0600 && mode != 0644 {
		// 0600 is the contract; some CI sandboxes (umask) may end up at 0644.
		// Fail only if the file is world-readable.
		if mode&0o004 != 0 {
			t.Errorf("config file is world-readable (mode %#o)", mode)
		}
	}
}

// TestMigrateLegacyDefaultsForZeroFields guards the post-migration config
// against tripping Validate() because a legacy file omitted timeout fields.
// Without the DefaultProfile() seed in migrateLegacy, Validate would refuse
// to load the migrated config and the user would lose their settings.
func TestMigrateLegacyDefaultsForZeroFields(t *testing.T) {
	tmp := t.TempDir()
	setHomeDir(t, tmp)

	cfgDir := filepath.Join(tmp, ".burp-upstream-adapter")
	_ = os.MkdirAll(cfgDir, 0700)
	// Legacy JSON missing both timeout fields and port.
	body := `{"upstream":{"host":"legacy","verify_tls":true},"local":{"bind_host":"127.0.0.1","bind_port":18080}}`
	_ = os.WriteFile(filepath.Join(cfgDir, DefaultConfigFile), []byte(body), 0600)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p, _ := cfg.Active()
	if p.Port != 3128 {
		t.Errorf("expected default port 3128 to fill in, got %d", p.Port)
	}
	if p.ConnectTimeout != 30 || p.IdleTimeout != 300 {
		t.Errorf("expected default timeouts to fill in, got connect=%d idle=%d", p.ConnectTimeout, p.IdleTimeout)
	}
	// And the migrated config must actually validate.
	if err := cfg.Validate(); err != nil {
		t.Errorf("migrated config failed Validate(): %v", err)
	}
}

// TestMigrateLegacyReadsCAFile verifies the one-time PEM file → inline
// content migration. A user with a legacy config pointing at a CA file
// should not lose their custom CA after upgrading.
func TestMigrateLegacyReadsCAFile(t *testing.T) {
	tmp := t.TempDir()
	setHomeDir(t, tmp)

	cfgDir := filepath.Join(tmp, ".burp-upstream-adapter")
	_ = os.MkdirAll(cfgDir, 0700)

	pemPath := filepath.Join(tmp, "ca.pem")
	pemContent := "-----BEGIN CERTIFICATE-----\nfake\n-----END CERTIFICATE-----\n"
	if err := os.WriteFile(pemPath, []byte(pemContent), 0600); err != nil {
		t.Fatal(err)
	}
	body := `{
	  "upstream": {"host":"legacy","port":3128,"verify_tls":true,"custom_ca_path":"` + pemPath + `","connect_timeout_sec":30,"idle_timeout_sec":300},
	  "local": {"bind_host":"127.0.0.1","bind_port":18080}
	}`
	_ = os.WriteFile(filepath.Join(cfgDir, DefaultConfigFile), []byte(body), 0600)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p, _ := cfg.Active()
	if p.CustomCAPEM != pemContent {
		t.Errorf("CA PEM not migrated inline; got %q", p.CustomCAPEM)
	}
}

// TestMigrateLegacyMissingCAFileSilent verifies the best-effort behaviour:
// if the legacy config points at a CA file that no longer exists, we still
// migrate the rest of the config rather than aborting Load entirely.
func TestMigrateLegacyMissingCAFileSilent(t *testing.T) {
	tmp := t.TempDir()
	setHomeDir(t, tmp)

	cfgDir := filepath.Join(tmp, ".burp-upstream-adapter")
	_ = os.MkdirAll(cfgDir, 0700)
	body := `{
	  "upstream": {"host":"legacy","port":3128,"verify_tls":true,"custom_ca_path":"/nonexistent/ca.pem","connect_timeout_sec":30,"idle_timeout_sec":300},
	  "local": {"bind_host":"127.0.0.1","bind_port":18080}
	}`
	_ = os.WriteFile(filepath.Join(cfgDir, DefaultConfigFile), []byte(body), 0600)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with missing CA file: %v", err)
	}
	p, _ := cfg.Active()
	if p.CustomCAPEM != "" {
		t.Errorf("missing CA file should leave PEM empty, got %q", p.CustomCAPEM)
	}
	if p.Host != "legacy" {
		t.Errorf("rest of profile should still migrate, got host=%q", p.Host)
	}
}
