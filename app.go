package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"maps"
	"os"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"burp-upstream-adapter/internal/adapter"
	"burp-upstream-adapter/internal/config"
	"burp-upstream-adapter/internal/keychain"
	"burp-upstream-adapter/internal/logging"
	"burp-upstream-adapter/internal/upstream"
)

type App struct {
	ctx    context.Context
	log    *logging.Logger
	server *adapter.Server
	cfg    config.Config
	mu     sync.Mutex
}

func NewApp() *App {
	return &App{
		log: logging.New(1000),
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	a.log.SetCallback(func(entry logging.Entry) {
		runtime.EventsEmit(ctx, "log", entry)
	})

	cfg, err := config.Load()
	if err != nil {
		a.log.Warn("Failed to load config, using defaults: %v", err)
		cfg = config.Default()
	}
	a.cfg = cfg
	a.applyWindowTitle()
	a.log.Info("Config loaded (active profile: %s)", cfg.ActiveProfile)
}

func (a *App) shutdown(_ context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.server != nil && a.server.IsRunning() {
		_ = a.server.Stop()
	}
}

// applyWindowTitle updates the native window title to reflect the active profile.
func (a *App) applyWindowTitle() {
	if a.ctx == nil {
		return
	}
	title := "Burp Upstream HTTPS Proxy Adapter"
	if a.cfg.ActiveProfile != "" {
		title = title + " — " + a.cfg.ActiveProfile
	}
	runtime.WindowSetTitle(a.ctx, title)
}

// --- DTOs ---

// ConfigDTO is the flat payload exchanged with the frontend for the active
// profile's settings plus the shared local listener settings.
type ConfigDTO struct {
	ActiveProfile  string `json:"active_profile"`
	UpstreamHost   string `json:"upstream_host"`
	UpstreamPort   int    `json:"upstream_port"`
	Username       string `json:"username"`
	Password       string `json:"password"`
	VerifyTLS      bool   `json:"verify_tls"`
	CustomCAPEM    string `json:"custom_ca_pem"`
	ConnectTimeout int    `json:"connect_timeout"`
	IdleTimeout    int    `json:"idle_timeout"`
	BindHost       string `json:"bind_host"`
	BindPort       int    `json:"bind_port"`
}

// ProfileSummary is a lightweight representation used for populating the
// selector dropdown without returning passwords. The frontend tracks the
// active profile via ConfigDTO.ActiveProfile, so no flag is duplicated here.
type ProfileSummary struct {
	Name string `json:"name"`
}

// --- Config methods ---

// requireProxyStoppedLocked returns an error if the proxy is running. Caller
// holds a.mu. Used as a precondition by profile-mutation methods so that
// switching underneath a live proxy can't produce inconsistent state.
func (a *App) requireProxyStoppedLocked(verb string) error {
	if a.server != nil && a.server.IsRunning() {
		return fmt.Errorf("stop the proxy before %s", verb)
	}
	return nil
}

func (a *App) GetConfig() ConfigDTO {
	return a.buildDTO()
}

// buildDTO assembles the DTO for the active profile. It captures profile
// state under a.mu, releases the lock, then resolves the password from the
// OS keychain. Keychain calls can block (macOS may display an auth prompt
// on first access), so we never hold the App mutex across them.
func (a *App) buildDTO() ConfigDTO {
	a.mu.Lock()
	active := a.cfg.ActiveProfile
	local := a.cfg.Local
	prof, err := a.cfg.Active()
	a.mu.Unlock()
	if err != nil {
		// Shouldn't happen after Load() normalises the config, but return
		// a minimal DTO so the UI can still render.
		return ConfigDTO{
			ActiveProfile: active,
			BindHost:      local.BindHost,
			BindPort:      local.BindPort,
		}
	}
	pw, _ := keychain.LoadPassword(active, prof.Username)
	return ConfigDTO{
		ActiveProfile:  active,
		UpstreamHost:   prof.Host,
		UpstreamPort:   prof.Port,
		Username:       prof.Username,
		Password:       pw,
		VerifyTLS:      prof.VerifyTLS,
		CustomCAPEM:    prof.CustomCAPEM,
		ConnectTimeout: prof.ConnectTimeout,
		IdleTimeout:    prof.IdleTimeout,
		BindHost:       local.BindHost,
		BindPort:       local.BindPort,
	}
}

// SaveConfig persists the form values into the active profile. The profile
// name is taken from the current ActiveProfile — renaming happens via a
// dedicated binding.
//
// When the username changes within the same profile, the keychain entry for
// the old username is migrated (or, if the user typed a new password, simply
// deleted) so we never leave orphaned credentials behind.
func (a *App) SaveConfig(dto ConfigDTO) error {
	a.mu.Lock()
	if a.cfg.ActiveProfile == "" {
		a.mu.Unlock()
		return fmt.Errorf("no active profile")
	}
	active := a.cfg.ActiveProfile
	oldUsername := a.cfg.Profiles[active].Username

	prof := config.ProfileConfig{
		Host:           dto.UpstreamHost,
		Port:           dto.UpstreamPort,
		Username:       dto.Username,
		VerifyTLS:      dto.VerifyTLS,
		CustomCAPEM:    dto.CustomCAPEM,
		ConnectTimeout: dto.ConnectTimeout,
		IdleTimeout:    dto.IdleTimeout,
	}

	newCfg := a.cfg
	// Copy the profiles map so we never mutate the one stored on a.cfg
	// until validation succeeds.
	newCfg.Profiles = maps.Clone(a.cfg.Profiles)
	newCfg.Profiles[active] = prof
	newCfg.Local = config.LocalConfig{BindHost: dto.BindHost, BindPort: dto.BindPort}

	if err := newCfg.Validate(); err != nil {
		a.mu.Unlock()
		return fmt.Errorf("validation: %w", err)
	}

	if err := config.Save(newCfg); err != nil {
		a.mu.Unlock()
		return fmt.Errorf("save config: %w", err)
	}

	a.cfg = newCfg
	a.log.Info("Config saved (profile: %s)", active)
	a.mu.Unlock()

	// All keychain writes happen outside the App mutex so a blocking OS
	// prompt can't freeze the UI.
	usernameChanged := oldUsername != dto.Username

	// 1. If the user supplied a new password in this save, persist it under
	//    the (possibly new) username. An empty password field means "no
	//    change" — we never erase a stored password implicitly.
	if dto.Password != "" {
		if err := keychain.SavePassword(active, dto.Username, dto.Password); err != nil {
			return fmt.Errorf("save password: %w", err)
		}
	} else if usernameChanged {
		// 2. Username changed but no new password typed: migrate the old
		//    entry so the user keeps their stored credential. Best-effort —
		//    a missing source entry is fine.
		if pw, err := keychain.LoadPassword(active, oldUsername); err == nil && pw != "" {
			_ = keychain.SavePassword(active, dto.Username, pw)
		}
	}

	// 3. Whenever the username changed, drop the old entry so it can't leak
	//    after the user later deletes the profile.
	if usernameChanged {
		_ = keychain.DeletePassword(active, oldUsername)
	}
	return nil
}

// --- Profile management ---

// ListProfiles returns the known profile names with a flag on the active one.
func (a *App) ListProfiles() []ProfileSummary {
	a.mu.Lock()
	defer a.mu.Unlock()
	names := a.cfg.ProfileNames()
	out := make([]ProfileSummary, 0, len(names))
	for _, n := range names {
		out = append(out, ProfileSummary{Name: n})
	}
	return out
}

// SwitchProfile sets the active profile. The proxy must be stopped first —
// the frontend is expected to confirm with the user before calling this.
func (a *App) SwitchProfile(name string) (ConfigDTO, error) {
	a.mu.Lock()
	if err := config.ValidateProfileName(name); err != nil {
		a.mu.Unlock()
		return ConfigDTO{}, err
	}
	if _, ok := a.cfg.Profiles[name]; !ok {
		a.mu.Unlock()
		return ConfigDTO{}, fmt.Errorf("profile %q does not exist", name)
	}
	if err := a.requireProxyStoppedLocked("switching profiles"); err != nil {
		a.mu.Unlock()
		return ConfigDTO{}, err
	}

	a.cfg.ActiveProfile = name
	if err := config.Save(a.cfg); err != nil {
		a.mu.Unlock()
		return ConfigDTO{}, fmt.Errorf("save config: %w", err)
	}
	a.applyWindowTitle()
	a.log.Info("Switched to profile %q", name)
	a.mu.Unlock()
	return a.buildDTO(), nil
}

// CreateProfile adds a new profile populated with safe defaults and returns
// the updated DTO for the new active profile.
func (a *App) CreateProfile(name string) (ConfigDTO, error) {
	a.mu.Lock()
	if err := config.ValidateProfileName(name); err != nil {
		a.mu.Unlock()
		return ConfigDTO{}, err
	}
	if _, exists := a.cfg.Profiles[name]; exists {
		a.mu.Unlock()
		return ConfigDTO{}, fmt.Errorf("profile %q already exists", name)
	}
	if err := a.requireProxyStoppedLocked("creating a new profile"); err != nil {
		a.mu.Unlock()
		return ConfigDTO{}, err
	}

	a.cfg.Profiles[name] = config.DefaultProfile()
	a.cfg.ActiveProfile = name

	if err := config.Save(a.cfg); err != nil {
		a.mu.Unlock()
		return ConfigDTO{}, fmt.Errorf("save config: %w", err)
	}
	a.applyWindowTitle()
	a.log.Info("Created profile %q", name)
	a.mu.Unlock()
	return a.buildDTO(), nil
}

// DuplicateProfile clones an existing profile (including its keychain
// password) under a new name and makes the copy active.
func (a *App) DuplicateProfile(src, dst string) (ConfigDTO, error) {
	a.mu.Lock()
	if err := config.ValidateProfileName(dst); err != nil {
		a.mu.Unlock()
		return ConfigDTO{}, err
	}
	srcProfile, ok := a.cfg.Profiles[src]
	if !ok {
		a.mu.Unlock()
		return ConfigDTO{}, fmt.Errorf("profile %q does not exist", src)
	}
	if _, exists := a.cfg.Profiles[dst]; exists {
		a.mu.Unlock()
		return ConfigDTO{}, fmt.Errorf("profile %q already exists", dst)
	}
	if err := a.requireProxyStoppedLocked("duplicating profiles"); err != nil {
		a.mu.Unlock()
		return ConfigDTO{}, err
	}

	a.cfg.Profiles[dst] = srcProfile
	a.cfg.ActiveProfile = dst

	if err := config.Save(a.cfg); err != nil {
		a.mu.Unlock()
		return ConfigDTO{}, fmt.Errorf("save config: %w", err)
	}
	a.applyWindowTitle()
	a.log.Info("Duplicated profile %q to %q", src, dst)
	a.mu.Unlock()

	// Keychain work happens outside the mutex so a blocking OS auth prompt
	// can't stall the whole App. Best-effort: a failure here just means the
	// user re-enters the password after switching.
	if pw, err := keychain.LoadPassword(src, srcProfile.Username); err == nil && pw != "" {
		_ = keychain.SavePassword(dst, srcProfile.Username, pw)
	}
	return a.buildDTO(), nil
}

// RenameProfile changes a profile's name in place. The keychain entry is
// migrated so the stored password follows the rename. If oldName was the
// active profile, ActiveProfile is updated to newName.
func (a *App) RenameProfile(oldName, newName string) (ConfigDTO, error) {
	a.mu.Lock()
	if oldName == newName {
		a.mu.Unlock()
		return a.buildDTO(), nil // no-op
	}
	if err := config.ValidateProfileName(newName); err != nil {
		a.mu.Unlock()
		return ConfigDTO{}, err
	}
	prof, ok := a.cfg.Profiles[oldName]
	if !ok {
		a.mu.Unlock()
		return ConfigDTO{}, fmt.Errorf("profile %q does not exist", oldName)
	}
	if _, exists := a.cfg.Profiles[newName]; exists {
		a.mu.Unlock()
		return ConfigDTO{}, fmt.Errorf("profile %q already exists", newName)
	}
	if err := a.requireProxyStoppedLocked("renaming a profile"); err != nil {
		a.mu.Unlock()
		return ConfigDTO{}, err
	}

	a.cfg.Profiles[newName] = prof
	delete(a.cfg.Profiles, oldName)
	if a.cfg.ActiveProfile == oldName {
		a.cfg.ActiveProfile = newName
	}

	if err := config.Save(a.cfg); err != nil {
		a.mu.Unlock()
		return ConfigDTO{}, fmt.Errorf("save config: %w", err)
	}
	a.applyWindowTitle()
	a.log.Info("Renamed profile %q to %q", oldName, newName)
	a.mu.Unlock()

	// Migrate the keychain entry so the password follows the rename. Both
	// steps are best-effort — losing the password just forces the user to
	// re-enter it, which is preferable to keeping an orphan under the old
	// profile name.
	if pw, err := keychain.LoadPassword(oldName, prof.Username); err == nil && pw != "" {
		_ = keychain.SavePassword(newName, prof.Username, pw)
	}
	_ = keychain.DeletePassword(oldName, prof.Username)
	return a.buildDTO(), nil
}

// DeleteProfile removes a profile. The last remaining profile cannot be
// deleted — the caller must always have at least one to fall back to.
func (a *App) DeleteProfile(name string) (ConfigDTO, error) {
	a.mu.Lock()
	prof, ok := a.cfg.Profiles[name]
	if !ok {
		a.mu.Unlock()
		return ConfigDTO{}, fmt.Errorf("profile %q does not exist", name)
	}
	if len(a.cfg.Profiles) <= 1 {
		a.mu.Unlock()
		return ConfigDTO{}, fmt.Errorf("cannot delete the last remaining profile")
	}
	if err := a.requireProxyStoppedLocked("deleting a profile"); err != nil {
		a.mu.Unlock()
		return ConfigDTO{}, err
	}

	delete(a.cfg.Profiles, name)
	// If we just deleted the active profile, pick the first remaining one.
	if a.cfg.ActiveProfile == name {
		a.cfg.ActiveProfile = a.cfg.ProfileNames()[0]
	}

	if err := config.Save(a.cfg); err != nil {
		a.mu.Unlock()
		return ConfigDTO{}, fmt.Errorf("save config: %w", err)
	}
	a.applyWindowTitle()
	a.log.Info("Deleted profile %q", name)
	a.mu.Unlock()

	// Drop the keychain entry outside the mutex. Any orphaned entries for
	// previously-used usernames on this profile will remain — username
	// changes are not tracked, so we only clean up the current one.
	_ = keychain.DeletePassword(name, prof.Username)
	return a.buildDTO(), nil
}

// --- Proxy control ---

func (a *App) StartProxy() error {
	a.mu.Lock()
	if a.server != nil && a.server.IsRunning() {
		a.mu.Unlock()
		return fmt.Errorf("proxy is already running")
	}
	if err := a.cfg.Validate(); err != nil {
		a.mu.Unlock()
		return fmt.Errorf("config invalid: %w", err)
	}
	prof, err := a.cfg.Active()
	if err != nil {
		a.mu.Unlock()
		return err
	}
	active := a.cfg.ActiveProfile
	local := a.cfg.Local
	a.mu.Unlock()

	// Load the password outside the App mutex so a blocking keychain prompt
	// can't stall other bindings.
	pw, _ := keychain.LoadPassword(active, prof.Username)

	srv, err := adapter.NewServer(prof, local, prof.Username, pw, a.log)
	if err != nil {
		return err
	}
	if err := srv.Start(); err != nil {
		return err
	}

	a.mu.Lock()
	a.server = srv
	a.mu.Unlock()
	runtime.EventsEmit(a.ctx, "status", "running")
	return nil
}

func (a *App) StopProxy() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.server == nil || !a.server.IsRunning() {
		return nil
	}

	if err := a.server.Stop(); err != nil {
		return err
	}

	runtime.EventsEmit(a.ctx, "status", "stopped")
	return nil
}

func (a *App) GetStatus() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.server != nil && a.server.IsRunning() {
		return "running"
	}
	return "stopped"
}

func (a *App) GetMetrics() adapter.MetricsSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.server != nil {
		return a.server.GetMetrics()
	}
	return adapter.MetricsSnapshot{}
}

func (a *App) GetLogs() []logging.Entry {
	return a.log.Entries()
}

func (a *App) ClearLogs() {
	a.log.Clear()
}

// --- Diagnostics ---

// diagContext is the snapshot every diagnostic needs: the active profile, a
// ready-to-use TLS config, and the keychain-resolved password. Building it
// in one place keeps all four Test* methods small and consistent.
type diagContext struct {
	profile config.ProfileConfig
	tlsCfg  *tls.Config
	pw      string
}

// captureDiagContext snapshots the active profile, releases the App mutex,
// then resolves the password and builds the TLS config. It never holds
// a.mu across keychain or TLS construction work.
func (a *App) captureDiagContext() (diagContext, error) {
	a.mu.Lock()
	prof, err := a.cfg.Active()
	active := a.cfg.ActiveProfile
	a.mu.Unlock()
	if err != nil {
		return diagContext{}, err
	}
	tlsCfg, err := upstream.BuildTLSConfig(upstream.TLSConfig{
		VerifyTLS:   prof.VerifyTLS,
		CustomCAPEM: []byte(prof.CustomCAPEM),
		ServerName:  prof.Host,
	})
	if err != nil {
		return diagContext{}, err
	}
	pw, _ := keychain.LoadPassword(active, prof.Username)
	return diagContext{profile: prof, tlsCfg: tlsCfg, pw: pw}, nil
}

func (a *App) logCheckResult(testName string, result upstream.CheckResult) {
	if result.OK {
		a.log.Info("%s passed: %s", testName, result.Message)
	} else {
		a.log.Error("%s failed: %s", testName, result.Message)
	}
}

func (a *App) TestUpstreamTLS() upstream.CheckResult {
	d, err := a.captureDiagContext()
	if err != nil {
		return upstream.CheckResult{OK: false, Message: err.Error()}
	}
	result := upstream.CheckTLS(a.ctx, d.profile.UpstreamAddr(), d.profile.ConnectTimeoutDuration(), d.tlsCfg)
	a.logCheckResult("TLS test", result)
	return result
}

func (a *App) TestProxyAuth() upstream.CheckResult {
	d, err := a.captureDiagContext()
	if err != nil {
		return upstream.CheckResult{OK: false, Message: err.Error()}
	}
	result := upstream.CheckProxyAuth(a.ctx, d.profile.UpstreamAddr(), d.profile.ConnectTimeoutDuration(), d.tlsCfg, d.profile.Username, d.pw)
	a.logCheckResult("Auth test", result)
	return result
}

func (a *App) TestCONNECT(target string) upstream.CheckResult {
	if target == "" {
		target = "example.com:443"
	}
	d, err := a.captureDiagContext()
	if err != nil {
		return upstream.CheckResult{OK: false, Message: err.Error()}
	}
	result := upstream.CheckCONNECT(a.ctx, d.profile.UpstreamAddr(), d.profile.ConnectTimeoutDuration(), d.tlsCfg, d.profile.Username, d.pw, target)
	a.logCheckResult("CONNECT test", result)
	return result
}

func (a *App) TestHTTPGet(targetURL string) upstream.CheckResult {
	if targetURL == "" {
		targetURL = "http://example.com/"
	}
	d, err := a.captureDiagContext()
	if err != nil {
		return upstream.CheckResult{OK: false, Message: err.Error()}
	}
	result := upstream.CheckHTTP(a.ctx, d.profile.UpstreamAddr(), d.profile.ConnectTimeoutDuration(), d.tlsCfg, d.profile.Username, d.pw, targetURL)
	a.logCheckResult("HTTP test", result)
	return result
}

// --- File picker ---

// LoadCAPEMFromFile prompts the user for a PEM file and returns its content
// so the frontend can store the bytes inline in the profile config. An empty
// return value with nil error indicates the user cancelled the dialog.
func (a *App) LoadCAPEMFromFile() (string, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select CA Certificate PEM File",
		Filters: []runtime.FileFilter{
			{DisplayName: "PEM Files", Pattern: "*.pem;*.crt;*.cer"},
			{DisplayName: "All Files", Pattern: "*"},
		},
	})
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", nil // user cancelled
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read CA file: %w", err)
	}
	return string(data), nil
}
