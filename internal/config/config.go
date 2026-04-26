package config

import (
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"time"
)

const (
	DefaultConfigFile  = "adapter.config.json"
	DefaultProfileName = "default"

	// MaxProfileNameLen is the maximum number of characters in a profile name.
	MaxProfileNameLen = 32
)

// profileNameRegex matches allowed profile names: alphanumeric, hyphen, underscore.
var profileNameRegex = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Config is the root configuration persisted to disk.
//
// Profiles hold per-environment upstream proxy settings. Local listener
// settings are shared across profiles because the adapter listens on a
// single socket at a time.
type Config struct {
	ActiveProfile string                   `json:"active_profile"`
	Profiles      map[string]ProfileConfig `json:"profiles"`
	Local         LocalConfig              `json:"local"`
}

// ProfileConfig holds one upstream proxy configuration. The password is never
// stored here; it lives in the OS keychain keyed by profile name.
type ProfileConfig struct {
	Host           string `json:"host"`
	Port           int    `json:"port"`
	Username       string `json:"username"`
	VerifyTLS      bool   `json:"verify_tls"`
	CustomCAPEM    string `json:"custom_ca_pem,omitempty"` // Inline PEM content (replaces the former path-based field).
	ConnectTimeout int    `json:"connect_timeout_sec"`
	IdleTimeout    int    `json:"idle_timeout_sec"`
}

type LocalConfig struct {
	BindHost string `json:"bind_host"`
	BindPort int    `json:"bind_port"`

	// MinimizeToTrayOnClose hides the main window to the system tray when the
	// user clicks the close button instead of quitting the application.
	MinimizeToTrayOnClose bool `json:"minimize_to_tray_on_close"`

	// HideDockIcon (macOS only) makes the app run as an accessory app — no
	// Dock icon, no Cmd+Tab presence, only the menu-bar icon. Takes effect on
	// next launch because activation policy is set during application startup.
	HideDockIcon bool `json:"hide_dock_icon"`
}

// DefaultProfile returns the upstream defaults used for a new profile.
func DefaultProfile() ProfileConfig {
	return ProfileConfig{
		Port:           3128,
		VerifyTLS:      true,
		ConnectTimeout: 30,
		IdleTimeout:    300,
	}
}

// Default returns a configuration with a single "default" profile and
// loopback listener settings.
func Default() Config {
	return Config{
		ActiveProfile: DefaultProfileName,
		Profiles: map[string]ProfileConfig{
			DefaultProfileName: DefaultProfile(),
		},
		Local: LocalConfig{
			BindHost:              "127.0.0.1",
			BindPort:              18080,
			MinimizeToTrayOnClose: false,
			HideDockIcon:          false,
		},
	}
}

// ValidateProfileName returns nil if name satisfies the profile naming rules.
func ValidateProfileName(name string) error {
	if name == "" {
		return errors.New("profile name is required")
	}
	if len(name) > MaxProfileNameLen {
		return fmt.Errorf("profile name must be %d characters or fewer", MaxProfileNameLen)
	}
	if !profileNameRegex.MatchString(name) {
		return errors.New("profile name may only contain letters, digits, hyphen, underscore")
	}
	return nil
}

// Active returns the profile selected as active, or an error if no such profile exists.
func (c *Config) Active() (ProfileConfig, error) {
	p, ok := c.Profiles[c.ActiveProfile]
	if !ok {
		return ProfileConfig{}, fmt.Errorf("active profile %q not found", c.ActiveProfile)
	}
	return p, nil
}

// ProfileNames returns the profile names in deterministic (sorted) order.
func (c *Config) ProfileNames() []string {
	names := make([]string, 0, len(c.Profiles))
	for name := range c.Profiles {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// Validate checks the top-level config and the active profile. Inactive
// profiles are validated with a lighter set of rules so a partially
// configured profile can still be saved.
func (c *Config) Validate() error {
	var errs []error

	if c.Local.BindHost == "" {
		errs = append(errs, errors.New("local bind host is required"))
	} else if net.ParseIP(c.Local.BindHost) == nil {
		errs = append(errs, fmt.Errorf("invalid bind host IP: %s", c.Local.BindHost))
	}
	if c.Local.BindPort < 1 || c.Local.BindPort > 65535 {
		errs = append(errs, fmt.Errorf("local bind port must be 1-65535, got %d", c.Local.BindPort))
	}

	if len(c.Profiles) == 0 {
		errs = append(errs, errors.New("at least one profile is required"))
	}
	if c.ActiveProfile == "" {
		errs = append(errs, errors.New("active profile is required"))
	}

	for name, p := range c.Profiles {
		if err := ValidateProfileName(name); err != nil {
			errs = append(errs, fmt.Errorf("profile %q: %w", name, err))
		}
		if err := p.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("profile %q: %w", name, err))
		}
	}

	if c.ActiveProfile != "" {
		if _, ok := c.Profiles[c.ActiveProfile]; !ok {
			errs = append(errs, fmt.Errorf("active profile %q not found in profiles", c.ActiveProfile))
		}
	}

	return errors.Join(errs...)
}

// Validate checks profile-level fields.
func (p *ProfileConfig) Validate() error {
	var errs []error

	if p.Host == "" {
		errs = append(errs, errors.New("upstream host is required"))
	}
	if p.Port < 1 || p.Port > 65535 {
		errs = append(errs, fmt.Errorf("upstream port must be 1-65535, got %d", p.Port))
	}
	if p.ConnectTimeout < 1 {
		errs = append(errs, errors.New("connect timeout must be >= 1 second"))
	}
	if p.IdleTimeout < 1 {
		errs = append(errs, errors.New("idle timeout must be >= 1 second"))
	}
	if p.CustomCAPEM != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(p.CustomCAPEM)) {
			errs = append(errs, errors.New("custom CA PEM is not valid"))
		}
	}
	return errors.Join(errs...)
}

// UpstreamAddr returns host:port for the given profile.
func (p *ProfileConfig) UpstreamAddr() string {
	return net.JoinHostPort(p.Host, strconv.Itoa(p.Port))
}

// ConnectTimeoutDuration returns ConnectTimeout as a time.Duration.
func (p *ProfileConfig) ConnectTimeoutDuration() time.Duration {
	return time.Duration(p.ConnectTimeout) * time.Second
}

// IdleTimeoutDuration returns IdleTimeout as a time.Duration.
func (p *ProfileConfig) IdleTimeoutDuration() time.Duration {
	return time.Duration(p.IdleTimeout) * time.Second
}

func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".burp-upstream-adapter")
	return dir, os.MkdirAll(dir, 0700)
}

func configPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, DefaultConfigFile), nil
}

// Load reads the config file, migrating a pre-profile layout into a single
// "default" profile. Missing files yield the default config with no error.
func Load() (Config, error) {
	path, err := configPath()
	if err != nil {
		return Default(), err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), nil
		}
		return Default(), err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Default(), fmt.Errorf("parse config: %w", err)
	}

	// If the JSON contained no profiles, attempt a legacy migration.
	if len(cfg.Profiles) == 0 {
		migrated, ok := migrateLegacy(data)
		if ok {
			cfg = migrated
		} else {
			cfg = Default()
		}
	}

	if cfg.ActiveProfile == "" {
		// Fall back to the first profile name if active is unset but profiles exist.
		if len(cfg.Profiles) > 0 {
			cfg.ActiveProfile = cfg.ProfileNames()[0]
		} else {
			cfg = Default()
		}
	}
	return cfg, nil
}

// legacyConfig matches the pre-profile JSON layout: a single "upstream"
// object directly under root. Used only for migration on first load.
type legacyConfig struct {
	Upstream *struct {
		Host           string `json:"host"`
		Port           int    `json:"port"`
		Username       string `json:"username"`
		VerifyTLS      bool   `json:"verify_tls"`
		CustomCAPath   string `json:"custom_ca_path,omitempty"`
		ConnectTimeout int    `json:"connect_timeout_sec"`
		IdleTimeout    int    `json:"idle_timeout_sec"`
	} `json:"upstream,omitempty"`
	Local LocalConfig `json:"local"`
}

// migrateLegacy folds the old single-upstream layout into a profile named "default".
// The old CustomCAPath is read from disk so the migrated config stores the PEM inline.
// Best-effort: a missing or unreadable file becomes an empty PEM with no error.
//
// Zero-valued numeric fields fall back to DefaultProfile() values so that
// configs written before a field existed still satisfy Validate().
func migrateLegacy(data []byte) (Config, bool) {
	var lc legacyConfig
	if err := json.Unmarshal(data, &lc); err != nil || lc.Upstream == nil {
		return Config{}, false
	}

	var pem string
	if lc.Upstream.CustomCAPath != "" {
		if b, err := os.ReadFile(lc.Upstream.CustomCAPath); err == nil {
			pem = string(b)
		}
	}

	prof := DefaultProfile() // seed with defaults so missing fields stay valid
	prof.Host = lc.Upstream.Host
	prof.Username = lc.Upstream.Username
	prof.VerifyTLS = lc.Upstream.VerifyTLS
	prof.CustomCAPEM = pem
	if lc.Upstream.Port > 0 {
		prof.Port = lc.Upstream.Port
	}
	if lc.Upstream.ConnectTimeout > 0 {
		prof.ConnectTimeout = lc.Upstream.ConnectTimeout
	}
	if lc.Upstream.IdleTimeout > 0 {
		prof.IdleTimeout = lc.Upstream.IdleTimeout
	}

	cfg := Default()
	cfg.Profiles[DefaultProfileName] = prof
	if lc.Local.BindHost != "" {
		cfg.Local.BindHost = lc.Local.BindHost
	}
	if lc.Local.BindPort != 0 {
		cfg.Local.BindPort = lc.Local.BindPort
	}
	return cfg, true
}

// Save writes the config to disk with mode 0600.
func Save(cfg Config) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
