package main

// tray_logic_test.go drives the parts of tray.go that don't touch the
// native menu update path (which requires a running Wails event loop).
// Specifically:
//
//   - populateProfileSubmenu mutates Go-side state on a *application.Menu
//     and registers click handlers. Both Clear/AddRadio/OnClick are pure Go.
//
//   - handleStartStop and switchProfile delegate to the App service —
//     also pure Go.
//
// We can't unit-test refreshStatus / refreshMetrics here because they call
// menu.Update(), which dispatches to the (absent) Wails main thread.

import (
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"burp-upstream-adapter/internal/config"
)

func TestPopulateProfileSubmenuMatchesProfiles(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	a.cfg.Profiles["staging"] = config.DefaultProfile()
	a.cfg.Profiles["prod"] = config.DefaultProfile()
	a.mu.Unlock()

	ui := &trayUI{}
	submenu := application.NewMenu()
	ui.populateProfileSubmenu(a, submenu, config.DefaultProfileName)

	if got := len(ui.profileItems); got != 3 {
		t.Fatalf("expected 3 profile items, got %d", got)
	}
}

// TestPopulateProfileSubmenuRebuildsCleanly verifies the contract that a
// second populate call discards the previous radio items rather than
// duplicating them. Without it, every profile rename/delete would leak
// orphan menu items.
func TestPopulateProfileSubmenuRebuildsCleanly(t *testing.T) {
	a := newTestApp(t)
	ui := &trayUI{}
	submenu := application.NewMenu()

	ui.populateProfileSubmenu(a, submenu, config.DefaultProfileName)
	first := len(ui.profileItems)

	a.mu.Lock()
	a.cfg.Profiles["second"] = config.DefaultProfile()
	a.mu.Unlock()
	ui.populateProfileSubmenu(a, submenu, config.DefaultProfileName)
	second := len(ui.profileItems)

	if first != 1 {
		t.Errorf("first populate should have 1 item, got %d", first)
	}
	if second != 2 {
		t.Errorf("second populate should have 2 items (replacing, not appending), got %d", second)
	}
}

// TestHandleStartStopWhileRunning drives the tray's start/stop button in
// the running → stopped direction. We deliberately don't test the reverse
// because that would require re-binding the same port, which is racy on
// slow systems — the relevant behaviour (the tray button picks the right
// branch based on GetStatus) is fully exercised by this case.
func TestHandleStartStopWhileRunning(t *testing.T) {
	a := newTestApp(t)
	startEphemeralProxy(t, a)

	ui := &trayUI{}
	ui.handleStartStop(a)

	if a.GetStatus() != "stopped" {
		t.Errorf("after click, expected 'stopped', got %q", a.GetStatus())
	}
}

// TestHandleStartStopWhileStopped exercises the other branch of the
// dispatch: when the proxy is stopped, the click invokes StartProxy.
// We use a fresh tempdir / fresh app so there's no prior listener to
// reuse the port from.
func TestHandleStartStopWhileStopped(t *testing.T) {
	a := newTestApp(t)
	startEphemeralProxy(t, a)
	if err := a.StopProxy(); err != nil {
		t.Fatal(err)
	}
	// Give the kernel a moment to actually free the listener.
	time.Sleep(50 * time.Millisecond)

	ui := &trayUI{}
	ui.handleStartStop(a)

	if a.GetStatus() != "running" {
		t.Errorf("after click, expected 'running', got %q", a.GetStatus())
	}
	_ = a.StopProxy()
}

// TestSwitchProfileFromTrayStopsRunningProxy guards the contract embodied
// in tray.switchProfile: choosing a profile from the menu while the proxy
// is running first stops it so the SwitchProfile binding's "must be
// stopped" precondition is satisfied. Without this courtesy, tray clicks
// silently fail and confuse the user.
func TestSwitchProfileFromTrayStopsRunningProxy(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	a.cfg.Profiles["staging"] = config.DefaultProfile()
	prof := a.cfg.Profiles["staging"]
	prof.Host = "proxy.example.com"
	a.cfg.Profiles["staging"] = prof
	a.mu.Unlock()

	startEphemeralProxy(t, a)

	ui := &trayUI{}
	ui.switchProfile(a, "staging")

	if a.GetStatus() != "stopped" {
		t.Errorf("expected proxy stopped after tray switch, got %q", a.GetStatus())
	}
	if a.activeProfileName() != "staging" {
		t.Errorf("expected active=staging, got %q", a.activeProfileName())
	}
}
