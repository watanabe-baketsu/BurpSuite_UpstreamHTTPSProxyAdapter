package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"maps"
	"os"
	"sync"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"

	"burp-upstream-adapter/internal/adapter"
	"burp-upstream-adapter/internal/config"
	"burp-upstream-adapter/internal/keychain"
	"burp-upstream-adapter/internal/logging"
	"burp-upstream-adapter/internal/upstream"
)

// App is the singleton service bound to the frontend. Wails v3 exposes every
// exported method as a JS/TS binding, so the public surface here is the same
// contract the React frontend depends on. The service also acts as the bridge
// between proxy state and the system tray, so app.go owns the tray-visible
// observers (statusObservers / metricsObservers) too.
type App struct {
	app    *application.App
	window *application.WebviewWindow

	log    *logging.Logger
	server *adapter.Server
	cfg    config.Config
	mu     sync.Mutex

	// Tray subscribers. Tray rebuilds in place on each event rather than
	// re-creating the menu, so list rebuilds (profile rename/delete) do not
	// trigger the systray rebuild crash reported in wailsapp/wails#5227.
	obsMu            sync.Mutex
	statusObservers  []func(running bool)
	profileObservers []func(active string, profiles []string)

	// shutdownHooks run from onAppShutdown BEFORE the proxy graceful-stop.
	// The tray ticker registers its cancel function here so it stops poking
	// the macOS main thread (InvokeSync round-trips) the moment the user
	// asks to quit, instead of after app.Run() returns.
	shutdownHooks []func()

	// quitting is flipped to true the moment we *intend* to terminate the
	// process (tray Quit, close-button when minimize-to-tray is off, etc.)
	// so the WindowClosing hook can stop intercepting close events. Without
	// this flag, [NSApp terminate:nil] would re-enter the hook, the hook
	// would Cancel the close to "minimize to tray", and the termination
	// would be vetoed — i.e. Quit would silently do nothing.
	quitting atomic.Bool
}

// preferAccessoryActivation reports whether the app should launch as a macOS
// accessory app (no Dock icon). It is read before application.New, so it
// cannot use the ServiceStartup-injected logger. Lowercase to keep it out of
// the frontend bindings — the toggle is exposed via ConfigDTO instead.
func (a *App) preferAccessoryActivation() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.Local.HideDockIcon
}

// minimizeToTrayOnClose is consulted by the WindowClosing hook to decide
// whether to swallow the close event and hide the window instead.
func (a *App) minimizeToTrayOnClose() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.Local.MinimizeToTrayOnClose
}

func NewApp() *App {
	a := &App{
		log: logging.New(1000),
	}
	// Load the config eagerly so PreferAccessoryActivation() returns the
	// user's choice before application.New is called. ServiceStartup runs
	// after the application is already constructed, which is too late for
	// the macOS activation policy.
	cfg, err := config.Load()
	if err != nil {
		a.log.Warn("Failed to load config, using defaults: %v", err)
		cfg = config.Default()
	}
	a.cfg = cfg
	return a
}

// attachApp wires the running application into the service so it can emit
// events. Called from main.go after application.New returns. Lowercase so
// the Wails v3 binding generator does not expose it to the frontend.
func (a *App) attachApp(app *application.App) {
	a.app = app
}

// attachWindow wires the primary window into the service so window-control
// methods (ShowWindow / HideWindow) can target it. Called from main.go.
func (a *App) attachWindow(w *application.WebviewWindow) {
	a.window = w
}

// ServiceStartup is the Wails v3 lifecycle hook that runs after bindings are
// wired. We use it to forward each log entry to the frontend via the typed
// event channel.
func (a *App) ServiceStartup(_ context.Context, _ application.ServiceOptions) error {
	a.log.SetCallback(func(entry logging.Entry) {
		if a.app != nil {
			a.app.Event.Emit("log", entry)
		}
	})
	a.log.Info("Config loaded (active profile: %s)", a.cfg.ActiveProfile)
	a.applyWindowTitle()
	return nil
}

// addShutdownHook registers fn to run during onAppShutdown, before the
// proxy graceful-stop. Used by the tray to cancel its background ticker
// the instant the user requests quit, so it stops dispatching to the macOS
// main thread while the rest of the shutdown sequence runs.
func (a *App) addShutdownHook(fn func()) {
	a.mu.Lock()
	a.shutdownHooks = append(a.shutdownHooks, fn)
	a.mu.Unlock()
}

// onAppShutdown is the application-level shutdown hook (registered on
// Options.OnShutdown). It runs once when the app is exiting.
//
// CRITICAL: we must not hold a.mu across server.Stop. server.Stop blocks
// for up to 5 seconds waiting for in-flight connections to drain, and
// during that window every other path that touches a.mu (tray ticker
// GetMetrics/GetStatus, frontend RPC bindings, the Quit menu's status
// query) would stall — exactly the "menu bar icon frozen" symptom users
// see when they try to quit. Capture the server pointer + hooks under the
// lock, release it, then do the slow work outside.
func (a *App) onAppShutdown() {
	a.mu.Lock()
	srv := a.server
	hooks := a.shutdownHooks
	a.shutdownHooks = nil
	a.mu.Unlock()

	for _, fn := range hooks {
		fn()
	}

	if srv != nil && srv.IsRunning() {
		_ = srv.Stop()
	}
}

// applyWindowTitle updates the native window title to reflect the active profile.
func (a *App) applyWindowTitle() {
	if a.window == nil {
		return
	}
	title := "Burp Upstream HTTPS Proxy Adapter"
	if a.cfg.ActiveProfile != "" {
		title = title + " — " + a.cfg.ActiveProfile
	}
	a.window.SetTitle(title)
}

// emitStatus publishes the running/stopped event to the frontend AND
// notifies tray observers. Tray observers run synchronously here so the
// menu-bar reflects state changes without relying on the next poll tick.
func (a *App) emitStatus(running bool) {
	state := "stopped"
	if running {
		state = "running"
	}
	if a.app != nil {
		a.app.Event.Emit("status", state)
	}
	a.notifyStatusObservers(running)
}

// --- Tray observer plumbing (called by tray.go) ---
// Methods are lowercase so the Wails v3 binding generator does not expose
// these function-typed callbacks to the frontend (it cannot serialise them).

func (a *App) onStatusChange(fn func(running bool)) {
	a.obsMu.Lock()
	a.statusObservers = append(a.statusObservers, fn)
	a.obsMu.Unlock()
}

func (a *App) onProfileChange(fn func(active string, profiles []string)) {
	a.obsMu.Lock()
	a.profileObservers = append(a.profileObservers, fn)
	a.obsMu.Unlock()
}

func (a *App) notifyStatusObservers(running bool) {
	a.obsMu.Lock()
	obs := append([]func(bool){}, a.statusObservers...)
	a.obsMu.Unlock()
	for _, fn := range obs {
		fn(running)
	}
}

func (a *App) notifyProfileObservers() {
	a.mu.Lock()
	active := a.cfg.ActiveProfile
	profiles := a.cfg.ProfileNames()
	a.mu.Unlock()
	a.obsMu.Lock()
	obs := append([]func(string, []string){}, a.profileObservers...)
	a.obsMu.Unlock()
	for _, fn := range obs {
		fn(active, profiles)
	}
}

// --- DTOs ---

// ConfigDTO is the flat payload exchanged with the frontend for the active
// profile's settings plus the shared local listener settings.
type ConfigDTO struct {
	ActiveProfile         string `json:"active_profile"`
	UpstreamHost          string `json:"upstream_host"`
	UpstreamPort          int    `json:"upstream_port"`
	Username              string `json:"username"`
	Password              string `json:"password"`
	VerifyTLS             bool   `json:"verify_tls"`
	CustomCAPEM           string `json:"custom_ca_pem"`
	ConnectTimeout        int    `json:"connect_timeout"`
	IdleTimeout           int    `json:"idle_timeout"`
	BindHost              string `json:"bind_host"`
	BindPort              int    `json:"bind_port"`
	MinimizeToTrayOnClose bool   `json:"minimize_to_tray_on_close"`
	HideDockIcon          bool   `json:"hide_dock_icon"`
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
			ActiveProfile:         active,
			BindHost:              local.BindHost,
			BindPort:              local.BindPort,
			MinimizeToTrayOnClose: local.MinimizeToTrayOnClose,
			HideDockIcon:          local.HideDockIcon,
		}
	}
	pw, _ := keychain.LoadPassword(active, prof.Username)
	return ConfigDTO{
		ActiveProfile:         active,
		UpstreamHost:          prof.Host,
		UpstreamPort:          prof.Port,
		Username:              prof.Username,
		Password:              pw,
		VerifyTLS:             prof.VerifyTLS,
		CustomCAPEM:           prof.CustomCAPEM,
		ConnectTimeout:        prof.ConnectTimeout,
		IdleTimeout:           prof.IdleTimeout,
		BindHost:              local.BindHost,
		BindPort:              local.BindPort,
		MinimizeToTrayOnClose: local.MinimizeToTrayOnClose,
		HideDockIcon:          local.HideDockIcon,
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
	activeProf, ok := a.cfg.Profiles[active]
	if !ok {
		a.mu.Unlock()
		return fmt.Errorf("active profile %q is missing from config", active)
	}
	oldUsername := activeProf.Username

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
	newCfg.Local = config.LocalConfig{
		BindHost:              dto.BindHost,
		BindPort:              dto.BindPort,
		MinimizeToTrayOnClose: dto.MinimizeToTrayOnClose,
		HideDockIcon:          dto.HideDockIcon,
	}

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
	a.log.Info("Switched to profile %q", name)
	a.mu.Unlock()
	a.applyWindowTitle()
	a.notifyProfileObservers()
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
	a.log.Info("Created profile %q", name)
	a.mu.Unlock()
	a.applyWindowTitle()
	a.notifyProfileObservers()
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
	a.log.Info("Duplicated profile %q to %q", src, dst)
	a.mu.Unlock()

	a.applyWindowTitle()
	// Keychain work happens outside the mutex so a blocking OS auth prompt
	// can't stall the whole App. Best-effort: a failure here just means the
	// user re-enters the password after switching.
	if pw, err := keychain.LoadPassword(src, srcProfile.Username); err == nil && pw != "" {
		_ = keychain.SavePassword(dst, srcProfile.Username, pw)
	}
	a.notifyProfileObservers()
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
	a.log.Info("Renamed profile %q to %q", oldName, newName)
	a.mu.Unlock()

	a.applyWindowTitle()
	// Migrate the keychain entry so the password follows the rename. Both
	// steps are best-effort — losing the password just forces the user to
	// re-enter it, which is preferable to keeping an orphan under the old
	// profile name.
	if pw, err := keychain.LoadPassword(oldName, prof.Username); err == nil && pw != "" {
		_ = keychain.SavePassword(newName, prof.Username, pw)
	}
	_ = keychain.DeletePassword(oldName, prof.Username)
	a.notifyProfileObservers()
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
	a.log.Info("Deleted profile %q", name)
	a.mu.Unlock()

	a.applyWindowTitle()
	// Drop the keychain entry outside the mutex. Any orphaned entries for
	// previously-used usernames on this profile will remain — username
	// changes are not tracked, so we only clean up the current one.
	_ = keychain.DeletePassword(name, prof.Username)
	a.notifyProfileObservers()
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

	// Commit the new server pointer *before* publishing the status change.
	// Otherwise GetMetrics readers (notably the tray ticker) can race against
	// the start sequence and surface metrics from the previous, stopped
	// server during the gap between Start succeeding and the new pointer
	// being installed.
	a.mu.Lock()
	a.server = srv
	a.mu.Unlock()

	a.emitStatus(true)
	return nil
}

func (a *App) StopProxy() error {
	a.mu.Lock()
	if a.server == nil || !a.server.IsRunning() {
		a.mu.Unlock()
		return nil
	}
	srv := a.server
	a.mu.Unlock()

	// server.Stop blocks for up to 5 seconds waiting for in-flight connections
	// to drain. We must not hold a.mu across that window, otherwise every
	// frontend RPC and the tray ticker stalls until shutdown completes.
	if err := srv.Stop(); err != nil {
		return err
	}
	a.emitStatus(false)
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

// boundPort returns the live listener port so the tray label can display it
// without going through the JSON DTO. Falls back to the configured BindPort
// when the server isn't running.
func (a *App) boundPort() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.Local.BindPort
}

// activeProfileName returns the active profile name without the JSON-DTO
// trip, for the tray's status label.
func (a *App) activeProfileName() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.ActiveProfile
}

func (a *App) GetLogs() []logging.Entry {
	return a.log.Entries()
}

func (a *App) ClearLogs() {
	a.log.Clear()
}

// --- Window / lifecycle helpers used by the tray ---
// Window control is not exposed to the frontend RPC surface (the tray owns
// the WebviewWindow pointer directly). Quit stays exported because the
// frontend may want to ask for a graceful exit from a UI element.

// IsQuitting reports whether a Quit has been requested. The WindowClosing
// hook reads this to bypass its hide-to-tray branch during termination.
func (a *App) IsQuitting() bool {
	return a.quitting.Load()
}

// Quit triggers an application-level shutdown. Setting the quitting flag
// before delegating to a.app.Quit() ensures the WindowClosing hook lets
// the resulting [NSApp terminate:nil] proceed without re-intercepting it
// as a "minimize to tray".
func (a *App) Quit() {
	a.quitting.Store(true)
	if a.app != nil {
		a.app.Quit()
	}
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
	result := upstream.CheckTLS(context.Background(), d.profile.UpstreamAddr(), d.profile.ConnectTimeoutDuration(), d.tlsCfg)
	a.logCheckResult("TLS test", result)
	return result
}

func (a *App) TestProxyAuth() upstream.CheckResult {
	d, err := a.captureDiagContext()
	if err != nil {
		return upstream.CheckResult{OK: false, Message: err.Error()}
	}
	result := upstream.CheckProxyAuth(context.Background(), d.profile.UpstreamAddr(), d.profile.ConnectTimeoutDuration(), d.tlsCfg, d.profile.Username, d.pw)
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
	result := upstream.CheckCONNECT(context.Background(), d.profile.UpstreamAddr(), d.profile.ConnectTimeoutDuration(), d.tlsCfg, d.profile.Username, d.pw, target)
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
	result := upstream.CheckHTTP(context.Background(), d.profile.UpstreamAddr(), d.profile.ConnectTimeoutDuration(), d.tlsCfg, d.profile.Username, d.pw, targetURL)
	a.logCheckResult("HTTP test", result)
	return result
}

// --- File picker ---

// LoadCAPEMFromFile prompts the user for a PEM file and returns its content
// so the frontend can store the bytes inline in the profile config. An empty
// return value with nil error indicates the user cancelled the dialog.
func (a *App) LoadCAPEMFromFile() (string, error) {
	if a.app == nil {
		return "", fmt.Errorf("application not initialised")
	}
	dialog := a.app.Dialog.OpenFile()
	dialog.SetTitle("Select CA Certificate PEM File")
	dialog.AddFilter("PEM Files", "*.pem;*.crt;*.cer")
	dialog.AddFilter("All Files", "*")
	path, err := dialog.PromptForSingleSelection()
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
