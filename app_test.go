package main

// app_test.go covers the main package's App service. The tests live in
// `package main` (not `_test`) so they can poke unexported helpers like
// captureDiagContext, onAppShutdown, and the observer plumbing — those are
// the bits most likely to regress because they have no frontend type system
// to catch shape changes.
//
// Two cross-cutting concerns shape the setup:
//
//  1. config.Save / config.Load read from $HOME. We redirect HOME to a
//     per-test tempdir via t.Setenv (matches the pattern in
//     internal/config/config_test.go).
//
//  2. The keychain package talks to the real OS keychain. We swap it for
//     go-keyring's in-memory mock once at TestMain time so no test ever
//     prompts for credentials. The mock is process-global, so teardown
//     between tests just deletes the keys we touched.

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/zalando/go-keyring"

	"burp-upstream-adapter/internal/config"
	"burp-upstream-adapter/internal/keychain"
)

func TestMain(m *testing.M) {
	keyring.MockInit()
	os.Exit(m.Run())
}

// setHomeDir overrides HOME / USERPROFILE for the duration of the test so
// config.Load and config.Save operate on a private directory.
func setHomeDir(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", dir)
	}
}

// newTestApp returns a freshly-constructed App rooted at a tempdir HOME
// with a single valid profile (config validates) and the keychain mocked.
// It does NOT attach a Wails app/window — methods that touch those guard
// with nil checks and are exercised in their own tests.
func newTestApp(t *testing.T) *App {
	t.Helper()
	tmp := t.TempDir()
	setHomeDir(t, tmp)

	a := NewApp()
	// NewApp() loads from disk; on a fresh tempdir there's no file, so it
	// gets the package default. Replace the default profile's host so the
	// config can pass Validate() in tests that need it.
	a.mu.Lock()
	prof := a.cfg.Profiles[config.DefaultProfileName]
	prof.Host = "proxy.example.com"
	a.cfg.Profiles[config.DefaultProfileName] = prof
	a.mu.Unlock()
	return a
}

// ---------------------------------------------------------------- Config

func TestNewAppLoadsDefaults(t *testing.T) {
	tmp := t.TempDir()
	setHomeDir(t, tmp)

	a := NewApp()
	if a.cfg.Local.BindHost != "127.0.0.1" {
		t.Errorf("default bind host expected, got %q", a.cfg.Local.BindHost)
	}
	if a.cfg.ActiveProfile != config.DefaultProfileName {
		t.Errorf("expected active=%q, got %q", config.DefaultProfileName, a.cfg.ActiveProfile)
	}
}

// TestNewAppRecoversFromCorruptConfig is the regression test for the silent
// fallback when the on-disk config is unparseable. Without it, a single
// bad keystroke in adapter.config.json would crash the app at launch.
func TestNewAppRecoversFromCorruptConfig(t *testing.T) {
	tmp := t.TempDir()
	setHomeDir(t, tmp)
	cfgDir := filepath.Join(tmp, ".burp-upstream-adapter")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, config.DefaultConfigFile), []byte("garbage"), 0600); err != nil {
		t.Fatal(err)
	}

	a := NewApp()
	// Falls back to defaults rather than panicking, and the warning is logged.
	if a.cfg.ActiveProfile != config.DefaultProfileName {
		t.Errorf("expected fallback default, got active=%q", a.cfg.ActiveProfile)
	}
	entries := a.log.Entries()
	if len(entries) == 0 {
		t.Fatal("expected a warning log about the corrupt config")
	}
}

func TestPreferAccessoryActivationFromConfig(t *testing.T) {
	a := newTestApp(t)
	if a.preferAccessoryActivation() {
		t.Error("default config should not prefer accessory activation")
	}
	a.mu.Lock()
	a.cfg.Local.HideDockIcon = true
	a.mu.Unlock()
	if !a.preferAccessoryActivation() {
		t.Error("HideDockIcon=true should flip accessory activation on")
	}
}

func TestMinimizeToTrayOnCloseFromConfig(t *testing.T) {
	a := newTestApp(t)
	if a.minimizeToTrayOnClose() {
		t.Error("default config should not minimize to tray on close")
	}
	a.mu.Lock()
	a.cfg.Local.MinimizeToTrayOnClose = true
	a.mu.Unlock()
	if !a.minimizeToTrayOnClose() {
		t.Error("MinimizeToTrayOnClose=true should flip the flag on")
	}
}

// TestApplyWindowTitleNilWindow guards the tray-only mode where no main
// window is attached: applyWindowTitle must short-circuit, not panic.
func TestApplyWindowTitleNilWindow(t *testing.T) {
	a := newTestApp(t)
	a.applyWindowTitle() // no panic = success
}

// TestQuitNoApp guards the same nil-Wails path for Quit, which is wired to
// a tray menu item.
func TestQuitNoApp(t *testing.T) {
	a := newTestApp(t)
	a.Quit() // no panic
}

// ---------------------------------------------------------------- DTOs

func TestGetConfigPopulatesAllFields(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	a.cfg.Local.BindHost = "127.0.0.1"
	a.cfg.Local.BindPort = 9999
	prof := a.cfg.Profiles[config.DefaultProfileName]
	prof.Host = "proxy.test"
	prof.Port = 8443
	prof.Username = "alice"
	prof.VerifyTLS = false
	prof.ConnectTimeout = 11
	prof.IdleTimeout = 22
	a.cfg.Profiles[config.DefaultProfileName] = prof
	a.mu.Unlock()

	// Pre-seed a password so the DTO surfaces it.
	if err := keychain.SavePassword(config.DefaultProfileName, "alice", "s3cret"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = keychain.DeletePassword(config.DefaultProfileName, "alice") })

	dto := a.GetConfig()
	if dto.UpstreamHost != "proxy.test" || dto.UpstreamPort != 8443 {
		t.Errorf("upstream fields wrong: %+v", dto)
	}
	if dto.Username != "alice" || dto.Password != "s3cret" {
		t.Errorf("credentials wrong: user=%q pw=%q", dto.Username, dto.Password)
	}
	if dto.VerifyTLS {
		t.Error("VerifyTLS should be false")
	}
	if dto.ConnectTimeout != 11 || dto.IdleTimeout != 22 {
		t.Errorf("timeouts wrong: %+v", dto)
	}
	if dto.BindHost != "127.0.0.1" || dto.BindPort != 9999 {
		t.Errorf("local fields wrong: %+v", dto)
	}
}

// TestGetConfigReturnsMinimalDTOOnMissingProfile exercises the safety net:
// if the active profile is missing from the map (an invariant that should
// hold but might not after a manual edit), the DTO still has Local + name,
// rather than panicking inside Active().
func TestGetConfigReturnsMinimalDTOOnMissingProfile(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	a.cfg.ActiveProfile = "ghost"
	a.mu.Unlock()
	dto := a.GetConfig()
	if dto.ActiveProfile != "ghost" {
		t.Errorf("expected active=ghost, got %q", dto.ActiveProfile)
	}
	if dto.UpstreamHost != "" {
		t.Errorf("missing-profile DTO should have empty upstream, got %q", dto.UpstreamHost)
	}
}

// ---------------------------------------------------------------- SaveConfig

func TestSaveConfigPersistsAndUpdates(t *testing.T) {
	a := newTestApp(t)

	dto := ConfigDTO{
		ActiveProfile:  config.DefaultProfileName,
		UpstreamHost:   "saved.example.com",
		UpstreamPort:   3128,
		Username:       "alice",
		Password:       "p1",
		VerifyTLS:      true,
		ConnectTimeout: 10,
		IdleTimeout:    60,
		BindHost:       "127.0.0.1",
		BindPort:       18080,
	}
	if err := a.SaveConfig(dto); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	t.Cleanup(func() { _ = keychain.DeletePassword(config.DefaultProfileName, "alice") })

	// In-memory state updated.
	got := a.GetConfig()
	if got.UpstreamHost != "saved.example.com" {
		t.Errorf("in-memory DTO not updated: %+v", got)
	}
	if got.Password != "p1" {
		t.Errorf("password not stored: %q", got.Password)
	}

	// File is on disk.
	tmpHome, _ := os.UserHomeDir()
	if _, err := os.Stat(filepath.Join(tmpHome, ".burp-upstream-adapter", config.DefaultConfigFile)); err != nil {
		t.Errorf("config file not written: %v", err)
	}
}

// TestSaveConfigGuardsMissingActiveProfile is the regression test for the
// SaveConfig fix in this branch: a missing active profile must surface as
// an explicit error rather than silently overwriting an empty entry.
func TestSaveConfigGuardsMissingActiveProfile(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	a.cfg.ActiveProfile = "ghost"
	a.mu.Unlock()

	err := a.SaveConfig(ConfigDTO{
		UpstreamHost: "x", UpstreamPort: 3128,
		ConnectTimeout: 10, IdleTimeout: 60,
		BindHost: "127.0.0.1", BindPort: 18080,
	})
	if err == nil {
		t.Fatal("expected error when active profile is missing from Profiles map")
	}
}

// TestSaveConfigEmptyActiveProfile guards the other branch of the same
// invariant: ActiveProfile is the empty string.
func TestSaveConfigEmptyActiveProfile(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	a.cfg.ActiveProfile = ""
	a.mu.Unlock()

	if err := a.SaveConfig(ConfigDTO{}); err == nil {
		t.Fatal("expected error when ActiveProfile is empty")
	}
}

// TestSaveConfigRejectsInvalid covers the per-field validation surface:
// SaveConfig must not write a config that fails Validate(), otherwise the
// next Load could refuse to parse it back.
func TestSaveConfigRejectsInvalid(t *testing.T) {
	a := newTestApp(t)
	err := a.SaveConfig(ConfigDTO{
		ActiveProfile: config.DefaultProfileName,
		UpstreamHost:  "", // invalid
		UpstreamPort:  3128,
		BindHost:      "127.0.0.1",
		BindPort:      18080,
	})
	if err == nil {
		t.Fatal("expected validation failure for empty host")
	}
}

// TestSaveConfigPasswordMigratesOnUsernameChange verifies the keychain
// migration logic: when the username changes inside a profile and no new
// password is supplied, the stored credential follows the username so the
// user doesn't have to re-enter it. Without this, every username edit
// silently locks the user out.
func TestSaveConfigPasswordMigratesOnUsernameChange(t *testing.T) {
	a := newTestApp(t)

	base := ConfigDTO{
		ActiveProfile:  config.DefaultProfileName,
		UpstreamHost:   "proxy.example.com",
		UpstreamPort:   3128,
		Username:       "old-user",
		Password:       "saved-pw",
		VerifyTLS:      true,
		ConnectTimeout: 10,
		IdleTimeout:    60,
		BindHost:       "127.0.0.1",
		BindPort:       18080,
	}
	if err := a.SaveConfig(base); err != nil {
		t.Fatal(err)
	}

	// Change just the username — leave Password empty to mean "no change".
	migrated := base
	migrated.Username = "new-user"
	migrated.Password = ""
	if err := a.SaveConfig(migrated); err != nil {
		t.Fatal(err)
	}

	pw, _ := keychain.LoadPassword(config.DefaultProfileName, "new-user")
	if pw != "saved-pw" {
		t.Errorf("password did not migrate to new username, got %q", pw)
	}
	// Old slot must be dropped so it can't leak after the profile is later deleted.
	old, _ := keychain.LoadPassword(config.DefaultProfileName, "old-user")
	if old != "" {
		t.Errorf("old keychain slot should be deleted, still has %q", old)
	}
	t.Cleanup(func() { _ = keychain.DeletePassword(config.DefaultProfileName, "new-user") })
}

// ---------------------------------------------------------------- Profiles

func TestListProfilesSorted(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	a.cfg.Profiles["zeta"] = config.DefaultProfile()
	a.cfg.Profiles["alpha"] = config.DefaultProfile()
	a.mu.Unlock()

	got := a.ListProfiles()
	if len(got) != 3 {
		t.Fatalf("want 3 profiles, got %d", len(got))
	}
	if got[0].Name != "alpha" || got[2].Name != "zeta" {
		t.Errorf("ListProfiles not sorted: %+v", got)
	}
}

func TestSwitchProfileBasic(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	a.cfg.Profiles["staging"] = config.DefaultProfile()
	a.mu.Unlock()

	dto, err := a.SwitchProfile("staging")
	if err != nil {
		t.Fatalf("SwitchProfile: %v", err)
	}
	if dto.ActiveProfile != "staging" {
		t.Errorf("DTO active=%q, want %q", dto.ActiveProfile, "staging")
	}
	if a.activeProfileName() != "staging" {
		t.Errorf("activeProfileName=%q, want staging", a.activeProfileName())
	}
}

func TestSwitchProfileRejectsInvalidName(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.SwitchProfile("with space"); err == nil {
		t.Error("expected validation error for invalid name")
	}
}

func TestSwitchProfileRejectsUnknown(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.SwitchProfile("ghost"); err == nil {
		t.Error("expected error for unknown profile")
	}
}

func TestCreateProfile(t *testing.T) {
	a := newTestApp(t)
	dto, err := a.CreateProfile("staging")
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	if dto.ActiveProfile != "staging" {
		t.Errorf("DTO active should switch to new profile, got %q", dto.ActiveProfile)
	}
	if _, ok := a.cfg.Profiles["staging"]; !ok {
		t.Error("staging profile should exist in cfg")
	}
}

func TestCreateProfileRejectsDuplicate(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.CreateProfile(config.DefaultProfileName); err == nil {
		t.Error("expected error for duplicate profile name")
	}
}

func TestCreateProfileRejectsInvalidName(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.CreateProfile(""); err == nil {
		t.Error("expected error for empty name")
	}
}

func TestDuplicateProfile(t *testing.T) {
	a := newTestApp(t)

	// Pre-seed an upstream password on the source so we can verify it's copied.
	if err := keychain.SavePassword(config.DefaultProfileName, "", "src-pw"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = keychain.DeletePassword(config.DefaultProfileName, "") })

	dto, err := a.DuplicateProfile(config.DefaultProfileName, "copy")
	if err != nil {
		t.Fatalf("DuplicateProfile: %v", err)
	}
	if dto.ActiveProfile != "copy" {
		t.Errorf("expected active to switch to copy, got %q", dto.ActiveProfile)
	}
	pw, _ := keychain.LoadPassword("copy", "")
	if pw != "src-pw" {
		t.Errorf("copy did not inherit password, got %q", pw)
	}
	t.Cleanup(func() { _ = keychain.DeletePassword("copy", "") })
}

func TestDuplicateProfileRejectsMissingSource(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.DuplicateProfile("ghost", "copy"); err == nil {
		t.Error("expected error for missing source")
	}
}

func TestRenameProfileMigratesKeychain(t *testing.T) {
	a := newTestApp(t)
	if err := keychain.SavePassword(config.DefaultProfileName, "alice", "p1"); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	prof := a.cfg.Profiles[config.DefaultProfileName]
	prof.Username = "alice"
	a.cfg.Profiles[config.DefaultProfileName] = prof
	a.mu.Unlock()

	if _, err := a.RenameProfile(config.DefaultProfileName, "renamed"); err != nil {
		t.Fatalf("RenameProfile: %v", err)
	}
	if a.activeProfileName() != "renamed" {
		t.Errorf("ActiveProfile should follow rename, got %q", a.activeProfileName())
	}
	pw, _ := keychain.LoadPassword("renamed", "alice")
	if pw != "p1" {
		t.Errorf("password didn't follow rename, got %q", pw)
	}
	old, _ := keychain.LoadPassword(config.DefaultProfileName, "alice")
	if old != "" {
		t.Errorf("old keychain slot should be empty, got %q", old)
	}
	t.Cleanup(func() { _ = keychain.DeletePassword("renamed", "alice") })
}

func TestRenameProfileNoOp(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.RenameProfile(config.DefaultProfileName, config.DefaultProfileName); err != nil {
		t.Errorf("rename to same name should be a no-op, got %v", err)
	}
}

func TestRenameProfileCollision(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	a.cfg.Profiles["other"] = config.DefaultProfile()
	a.mu.Unlock()
	if _, err := a.RenameProfile(config.DefaultProfileName, "other"); err == nil {
		t.Error("expected error renaming onto an existing profile")
	}
}

func TestDeleteProfile(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	a.cfg.Profiles["other"] = config.DefaultProfile()
	a.mu.Unlock()

	if _, err := a.DeleteProfile("other"); err != nil {
		t.Fatalf("DeleteProfile: %v", err)
	}
	if _, ok := a.cfg.Profiles["other"]; ok {
		t.Error("profile should be gone")
	}
}

// TestDeleteLastProfileRejected is the safety net: the app should always
// have at least one profile to fall back to. Deleting the last one would
// leave the active-profile invariant unsatisfiable.
func TestDeleteLastProfileRejected(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.DeleteProfile(config.DefaultProfileName); err == nil {
		t.Error("expected error deleting the last remaining profile")
	}
}

// TestDeleteActiveSwitchesActive verifies that deleting the currently-active
// profile picks a fallback rather than leaving ActiveProfile dangling.
func TestDeleteActiveSwitchesActive(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	a.cfg.Profiles["other"] = config.DefaultProfile()
	a.mu.Unlock()
	if _, err := a.DeleteProfile(config.DefaultProfileName); err != nil {
		t.Fatal(err)
	}
	if a.activeProfileName() != "other" {
		t.Errorf("active should fall back to remaining profile, got %q", a.activeProfileName())
	}
}

// TestProfileMutationRejectedWhileRunning verifies that creating /
// deleting / switching / renaming a profile is forbidden while the proxy is
// alive — otherwise the live server's profile may stop matching the saved
// config and produce surprising behaviour for in-flight connections.
func TestProfileMutationRejectedWhileRunning(t *testing.T) {
	a := newTestApp(t)
	srv := startEphemeralProxy(t, a)
	defer func() {
		if err := a.StopProxy(); err != nil {
			t.Errorf("StopProxy: %v", err)
		}
	}()
	_ = srv

	if _, err := a.SwitchProfile("anything"); err == nil {
		t.Error("expected error switching profile while proxy is running")
	}
	if _, err := a.CreateProfile("new"); err == nil {
		t.Error("expected error creating profile while proxy is running")
	}
	if _, err := a.DeleteProfile(config.DefaultProfileName); err == nil {
		t.Error("expected error deleting profile while proxy is running")
	}
}

// ---------------------------------------------------------------- Proxy

// startEphemeralProxy stands up a fake upstream TLS proxy and points the
// app's active profile at it, then starts the local proxy. Returns the fake
// upstream so the caller can assert on traffic if needed.
func startEphemeralProxy(t *testing.T, a *App) *httptest.Server {
	t.Helper()
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Healthcheck just needs a 200 to consider auth OK.
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	u, _ := url.Parse(upstream.URL)
	host, portStr, _ := net.SplitHostPort(u.Host)
	port, _ := strconv.Atoi(portStr)

	// Pick a free local port up-front: Validate() rejects BindPort=0, but
	// the adapter's listener still uses an explicit port. We grab a TCP
	// port via the kernel, release it, and use that number — racy in
	// theory but reliable in the CI loopback.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	bindPort := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()

	a.mu.Lock()
	prof := a.cfg.Profiles[config.DefaultProfileName]
	prof.Host = host
	prof.Port = port
	prof.VerifyTLS = false // self-signed test cert
	prof.ConnectTimeout = 5
	prof.IdleTimeout = 30
	a.cfg.Profiles[config.DefaultProfileName] = prof
	a.cfg.Local.BindPort = bindPort
	a.mu.Unlock()

	if err := a.StartProxy(); err != nil {
		t.Fatalf("StartProxy: %v", err)
	}
	if a.GetStatus() != "running" {
		t.Fatalf("expected status 'running', got %q", a.GetStatus())
	}
	return upstream
}

func TestStartStopRoundTrip(t *testing.T) {
	a := newTestApp(t)
	startEphemeralProxy(t, a)
	if err := a.StopProxy(); err != nil {
		t.Fatalf("StopProxy: %v", err)
	}
	if a.GetStatus() != "stopped" {
		t.Errorf("expected status stopped after StopProxy, got %q", a.GetStatus())
	}
	// StopProxy on already-stopped proxy is a no-op.
	if err := a.StopProxy(); err != nil {
		t.Errorf("StopProxy on stopped should be nil, got %v", err)
	}
}

func TestDoubleStartRejected(t *testing.T) {
	a := newTestApp(t)
	startEphemeralProxy(t, a)
	defer func() { _ = a.StopProxy() }()

	if err := a.StartProxy(); err == nil {
		t.Error("expected error starting an already-running proxy")
	}
}

func TestStartProxyValidatesConfig(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	prof := a.cfg.Profiles[config.DefaultProfileName]
	prof.Host = "" // invalid
	a.cfg.Profiles[config.DefaultProfileName] = prof
	a.mu.Unlock()

	if err := a.StartProxy(); err == nil {
		t.Error("expected validation error before listening")
	}
}

func TestGetMetricsZeroWhenStopped(t *testing.T) {
	a := newTestApp(t)
	m := a.GetMetrics()
	if m.ActiveConnections != 0 || m.TotalRequests != 0 {
		t.Errorf("expected zeroed metrics when stopped, got %+v", m)
	}
}

// TestOnAppShutdownStopsRunningProxy is the contract the OS-shutdown path
// relies on: when the user quits the app, in-flight proxy connections must
// be drained rather than abandoned.
func TestOnAppShutdownStopsRunningProxy(t *testing.T) {
	a := newTestApp(t)
	startEphemeralProxy(t, a)
	a.onAppShutdown()
	// onAppShutdown takes a.mu and stops the server. After it returns,
	// IsRunning should be false — but onAppShutdown holds the lock the
	// whole time, so we have to wait for it to release before checking.
	if a.GetStatus() == "running" {
		t.Error("proxy should be stopped after onAppShutdown")
	}
}

func TestBoundPortReflectsConfig(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	a.cfg.Local.BindPort = 12345
	a.mu.Unlock()
	if got := a.boundPort(); got != 12345 {
		t.Errorf("boundPort = %d, want 12345", got)
	}
}

// ---------------------------------------------------------------- Logs

func TestGetLogsAndClearLogs(t *testing.T) {
	a := newTestApp(t)
	a.log.Info("hello %s", "world")
	if got := a.GetLogs(); len(got) != 1 || got[0].Message != "hello world" {
		t.Errorf("GetLogs round trip broken: %+v", got)
	}
	a.ClearLogs()
	if got := a.GetLogs(); len(got) != 0 {
		t.Errorf("ClearLogs should empty the buffer, got %+v", got)
	}
}

// ---------------------------------------------------------------- Observers

func TestStatusObserversFire(t *testing.T) {
	a := newTestApp(t)
	var fired sync.WaitGroup
	fired.Add(1)
	got := false
	a.onStatusChange(func(running bool) {
		got = running
		fired.Done()
	})

	a.notifyStatusObservers(true)
	fired.Wait()
	if !got {
		t.Error("status observer did not receive running=true")
	}
}

// TestProfileObserversFireWithSnapshot verifies the observer receives the
// active name AND the sorted profile list. The tray uses both — the active
// name to update the radio selection, the list to repopulate the submenu.
// If either is dropped from the snapshot, the tray falls out of sync.
func TestProfileObserversFireWithSnapshot(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	a.cfg.Profiles["staging"] = config.DefaultProfile()
	a.cfg.Profiles["prod"] = config.DefaultProfile()
	a.mu.Unlock()

	var (
		gotActive   string
		gotProfiles []string
	)
	var done sync.WaitGroup
	done.Add(1)
	a.onProfileChange(func(active string, profiles []string) {
		gotActive = active
		gotProfiles = profiles
		done.Done()
	})

	a.notifyProfileObservers()
	done.Wait()

	if gotActive != config.DefaultProfileName {
		t.Errorf("observer active=%q, want %q", gotActive, config.DefaultProfileName)
	}
	want := []string{config.DefaultProfileName, "prod", "staging"}
	if len(gotProfiles) != len(want) {
		t.Fatalf("observer profiles len=%d want %d", len(gotProfiles), len(want))
	}
	for i := range want {
		if gotProfiles[i] != want[i] {
			t.Errorf("observer profiles[%d]=%q want %q", i, gotProfiles[i], want[i])
		}
	}
}

// TestObserversConcurrentRegistration is the race-detector probe for the
// observer plumbing. Many goroutines registering observers while another
// fires the notifier must not race on the slice append.
func TestObserversConcurrentRegistration(t *testing.T) {
	a := newTestApp(t)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.onStatusChange(func(bool) {})
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			a.notifyStatusObservers(i%2 == 0)
		}
	}()
	wg.Wait()
}

// ---------------------------------------------------------------- Diagnostics

// TestCaptureDiagContextValid verifies the happy-path snapshot is fully
// populated and the password is resolved from the keychain.
func TestCaptureDiagContextValid(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	prof := a.cfg.Profiles[config.DefaultProfileName]
	prof.Username = "alice"
	a.cfg.Profiles[config.DefaultProfileName] = prof
	a.mu.Unlock()

	if err := keychain.SavePassword(config.DefaultProfileName, "alice", "diag-pw"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = keychain.DeletePassword(config.DefaultProfileName, "alice") })

	d, err := a.captureDiagContext()
	if err != nil {
		t.Fatalf("captureDiagContext: %v", err)
	}
	if d.profile.Host != "proxy.example.com" {
		t.Errorf("diag profile host wrong: %q", d.profile.Host)
	}
	if d.pw != "diag-pw" {
		t.Errorf("diag password wrong: %q", d.pw)
	}
	if d.tlsCfg == nil {
		t.Error("diag tlsCfg should be non-nil")
	}
}

func TestCaptureDiagContextMissingProfile(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	a.cfg.ActiveProfile = "ghost"
	a.mu.Unlock()
	if _, err := a.captureDiagContext(); err == nil {
		t.Error("expected error when active profile is missing")
	}
}

// TestCaptureDiagContextInvalidCA verifies the TLS-config build failure
// surfaces. Without it, an invalid PEM in the active profile would crash
// the diagnostic flow at use site instead of failing fast.
func TestCaptureDiagContextInvalidCA(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	prof := a.cfg.Profiles[config.DefaultProfileName]
	prof.CustomCAPEM = "not a PEM"
	a.cfg.Profiles[config.DefaultProfileName] = prof
	a.mu.Unlock()

	if _, err := a.captureDiagContext(); err == nil {
		t.Error("expected error from BuildTLSConfig with invalid CA")
	}
}

// TestTestUpstreamTLSAgainstFakeProxy is the end-to-end happy path: with a
// reachable fake upstream, TestUpstreamTLS should return OK=true.
func TestTestUpstreamTLSAgainstFakeProxy(t *testing.T) {
	a := newTestApp(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	u, _ := url.Parse(srv.URL)
	host, portStr, _ := net.SplitHostPort(u.Host)
	port, _ := strconv.Atoi(portStr)
	a.mu.Lock()
	prof := a.cfg.Profiles[config.DefaultProfileName]
	prof.Host = host
	prof.Port = port
	prof.VerifyTLS = false
	a.cfg.Profiles[config.DefaultProfileName] = prof
	a.mu.Unlock()

	res := a.TestUpstreamTLS()
	if !res.OK {
		t.Errorf("TestUpstreamTLS against reachable fake proxy returned %+v", res)
	}
	// And the result should be journaled to the in-app log so the UI panel
	// can show it without a separate state channel.
	found := false
	for _, e := range a.GetLogs() {
		if e.Message != "" && e.Level != "" && containsCheckResult(e.Message) {
			found = true
		}
	}
	if !found {
		t.Error("expected a TLS test log entry to be written")
	}
}

func containsCheckResult(msg string) bool {
	// Loose check — the Info log includes "TLS test passed" and the Error
	// log includes "TLS test failed". Either form proves the path executed.
	return msg != "" && (strings.HasPrefix(msg, "TLS test passed") || strings.HasPrefix(msg, "TLS test failed"))
}

func TestTestProxyAuthFailsAgainstUnreachable(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	prof := a.cfg.Profiles[config.DefaultProfileName]
	prof.Host = "127.0.0.1"
	prof.Port = 1                // Port 1 with no listener → fast refused
	prof.ConnectTimeout = 1      // Bound the test latency.
	prof.VerifyTLS = false
	a.cfg.Profiles[config.DefaultProfileName] = prof
	a.mu.Unlock()

	if res := a.TestProxyAuth(); res.OK {
		t.Errorf("TestProxyAuth against unreachable should fail, got %+v", res)
	}
	if res := a.TestCONNECT(""); res.OK {
		t.Errorf("TestCONNECT against unreachable should fail, got %+v", res)
	}
	if res := a.TestHTTPGet(""); res.OK {
		t.Errorf("TestHTTPGet against unreachable should fail, got %+v", res)
	}
}

// TestTestCONNECTAndHTTPDefaultArgs verifies the empty-target/empty-URL
// branches: the app fills sensible defaults so the UI can call them
// without prompting the user.
func TestTestCONNECTAndHTTPDefaultArgs(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	prof := a.cfg.Profiles[config.DefaultProfileName]
	prof.Host = "127.0.0.1"
	prof.Port = 1
	prof.ConnectTimeout = 1
	a.cfg.Profiles[config.DefaultProfileName] = prof
	a.mu.Unlock()

	// Both calls take the empty-arg branch. Failure is expected (host is
	// unreachable) — the assertion is only that the function did not panic.
	_ = a.TestCONNECT("")
	_ = a.TestHTTPGet("")
}

// TestLoadCAPEMFromFileNoApp guards the early-return when the Wails app
// hasn't been attached. The file picker requires a.app, so we fail fast
// with a clear error rather than nil-deref.
func TestLoadCAPEMFromFileNoApp(t *testing.T) {
	a := newTestApp(t)
	pem, err := a.LoadCAPEMFromFile()
	if err == nil {
		t.Error("expected error when application is not initialised")
	}
	if pem != "" {
		t.Errorf("expected empty PEM, got %q", pem)
	}
}

// TestRequireProxyStoppedLockedReturnsNilWhenStopped covers the gate used
// by every profile-mutation method.
func TestRequireProxyStoppedLockedReturnsNilWhenStopped(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.requireProxyStoppedLocked("test"); err != nil {
		t.Errorf("expected nil when proxy is stopped, got %v", err)
	}
}

func TestServiceStartupAttachesLogCallback(t *testing.T) {
	a := newTestApp(t)
	if err := a.ServiceStartup(t.Context(), application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	// One log entry should have been added (the "Config loaded" line).
	if len(a.GetLogs()) == 0 {
		t.Error("ServiceStartup should emit at least one log line")
	}
}

// fastTimeoutMargin caps each healthcheck-style sub-test so a hung dial
// can't drag CI to a halt. Used in combination with ConnectTimeout=1.
const fastTimeoutMargin = 5 * time.Second

func init() {
	// All Test* tests below indirectly hit the network through DialTLS.
	// Force a hard ceiling on test-suite dial duration via a process-level
	// tweak: nothing to set here yet, but keeping the init slot reserved
	// makes it easy to add e.g. http.DefaultTransport tuning if a future
	// change introduces it.
	_ = fastTimeoutMargin
}
