package main

import (
	"context"
	_ "embed"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed build/tray/tray-template.png
var trayTemplateIcon []byte

//go:embed build/tray/tray-regular.png
var trayRegularIcon []byte

// errorFreshness is how long after a recorded error the tray keeps showing
// the error state. After this elapses, the icon falls back to running/stopped.
const errorFreshness = 30 * time.Second

// trayUI owns the live menu items so the tray can mutate them in place
// (SetLabel / SetEnabled / SetChecked + menu.Update()) instead of rebuilding
// the menu on every tick. In-place updates avoid the SetMenu race that
// crashes Windows under load (wailsapp/wails#5227) and the macOS click-handler
// loss reported in #4719.
type trayUI struct {
	tray   *application.SystemTray
	menu   *application.Menu
	window *application.WebviewWindow

	statusItem    *application.MenuItem
	profileItem   *application.MenuItem
	startStopItem *application.MenuItem
	connItem      *application.MenuItem
	reqItem       *application.MenuItem

	// profileSubmenu is the live submenu of radio items. Its contents need to
	// be reset when profiles are added/renamed/deleted; for that we rebuild
	// the submenu's children only (not the top-level tray menu).
	profileSubmenu *application.Menu
	profileItemsMu sync.Mutex
	profileItems   []*application.MenuItem
}

// BuildTray constructs the system tray, wires it to app state, and starts
// the background ticker that keeps the menu labels in sync with proxy
// metrics. It is called once from main.go after window/services are wired.
//
// The returned cancel function stops the metrics-refresh goroutine so the
// caller can hook it into the application shutdown — calling app.SystemTray
// methods after the native tray is destroyed risks a panic in the v3 layer.
func BuildTray(svc *App, app *application.App, tray *application.SystemTray, window *application.WebviewWindow) (cancel func()) {
	if runtime.GOOS == "darwin" {
		// macOS template image is auto-coloured for the menu-bar appearance
		// (light/dark). The regular bytes provide a colour fallback for
		// platforms that don't render template images.
		tray.SetTemplateIcon(trayTemplateIcon)
	} else {
		tray.SetIcon(trayRegularIcon)
	}
	tray.SetTooltip("Burp Upstream HTTPS Proxy Adapter")

	ui := &trayUI{
		tray:   tray,
		window: window,
	}
	ui.menu = application.NewMenu()

	// ── Status row (disabled label) ─────────────────────────────
	ui.statusItem = ui.menu.Add("Status: Stopped").SetEnabled(false)
	ui.profileItem = ui.menu.Add("Profile: " + svc.activeProfileName()).SetEnabled(false)
	ui.menu.AddSeparator()

	// ── Start / Stop ────────────────────────────────────────────
	ui.startStopItem = ui.menu.Add("Start Proxy")
	ui.startStopItem.OnClick(func(_ *application.Context) {
		go ui.handleStartStop(svc)
	})
	ui.menu.AddSeparator()

	// ── Live metrics (disabled labels) ──────────────────────────
	ui.connItem = ui.menu.Add("Active: 0 connections").SetEnabled(false)
	ui.reqItem = ui.menu.Add("Total: 0 requests").SetEnabled(false)
	ui.menu.AddSeparator()

	// ── Profile submenu ─────────────────────────────────────────
	// AddSubmenu both inserts the "Profile" item into the parent menu and
	// returns a stable pointer to the child *Menu — we keep that pointer so
	// populateProfileSubmenu can clear/refill it in place when profiles are
	// added, renamed, or deleted.
	ui.profileSubmenu = ui.menu.AddSubmenu("Profile")
	ui.populateProfileSubmenu(svc, ui.profileSubmenu, svc.activeProfileName())
	ui.menu.AddSeparator()

	// ── Window control + Quit ───────────────────────────────────
	ui.menu.Add("Show Window").OnClick(func(_ *application.Context) {
		window.Show()
	})
	ui.menu.AddSeparator()
	ui.menu.Add("Quit").OnClick(func(_ *application.Context) {
		svc.Quit()
	})

	tray.SetMenu(ui.menu)

	// Left-click toggles the window. Combined with the menu, right-click
	// (or default platform gesture) opens the menu via systray's
	// applySmartDefaults logic.
	tray.OnClick(func() { window.Show() })

	// ── State observers ─────────────────────────────────────────
	svc.onStatusChange(func(running bool) {
		ui.refreshStatus(svc, running)
	})
	svc.onProfileChange(func(active string, _ []string) {
		ui.populateProfileSubmenu(svc, ui.profileSubmenu, active)
		ui.profileItem.SetLabel("Profile: " + active)
		ui.menu.Update()
	})

	// Initial sync so the menu reflects state at launch. This must run after
	// the application's main-thread dispatch is initialised (inside app.Run),
	// otherwise menu.Update() panics on nil dispatch — see the alpha.78
	// dispatchOnMainThread crash. ApplicationStarted fires once that's ready.
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(_ *application.ApplicationEvent) {
		ui.refreshStatus(svc, svc.GetStatus() == "running")
	})

	// ── Background tick: refresh metrics + error-driven icon state ──
	ctx, cancelFn := context.WithCancel(context.Background())
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				ui.refreshMetrics(svc)
			}
		}
	}()
	return cancelFn
}

// handleStartStop toggles the proxy from the tray menu. Errors are surfaced
// via the log channel (and the frontend, if open) — there is no inline UI
// feedback in the tray menu beyond the next tick reflecting the new state.
func (ui *trayUI) handleStartStop(svc *App) {
	if svc.GetStatus() == "running" {
		_ = svc.StopProxy()
	} else {
		_ = svc.StartProxy()
	}
}

// statusLabels returns (status row label, start/stop button label) given
// the live running/port state. Pulled out of refreshStatus so the wording
// can be unit-tested without booting Wails — the actual SetLabel/Update
// calls are cheap glue around these strings.
func statusLabels(running bool, port int) (statusLabel, actionLabel string) {
	if running {
		return fmt.Sprintf("Status: Running (%d)", port), "Stop Proxy"
	}
	return "Status: Stopped", "Start Proxy"
}

// refreshStatus updates the start/stop label, the status row, and the icon
// state in one place. Called both from the status observer and from the
// metrics tick (because error freshness can flip the icon without a status
// change).
func (ui *trayUI) refreshStatus(svc *App, running bool) {
	statusLabel, actionLabel := statusLabels(running, svc.boundPort())
	ui.statusItem.SetLabel(statusLabel)
	ui.startStopItem.SetLabel(actionLabel)
	ui.menu.Update()
}

// metricLabels returns (connections row, total requests row) so the
// formatting stays in one tested place and refreshMetrics is just glue.
func metricLabels(active, total int64) (connLabel, reqLabel string) {
	return fmt.Sprintf("Active: %d connections", active),
		fmt.Sprintf("Total: %d requests", total)
}

// trayTooltip is the pure formatter for the tray hover-tooltip. The order
// of cases (error → running → stopped) is the contract: a fresh error
// always wins over the running indicator so the user sees the most recent
// failure even if the server has since started accepting again.
func trayTooltip(running bool, port int, lastError string, lastErrorAt time.Time) string {
	const base = "Burp Upstream HTTPS Proxy Adapter"
	switch {
	case errorIsFresh(lastErrorAt):
		return base + " — Error: " + lastError
	case running:
		return fmt.Sprintf("%s — Running (port %d)", base, port)
	default:
		return base + " — Stopped"
	}
}

// refreshMetrics polls the proxy metrics and updates the menu labels and
// icon. Runs on a 1-second ticker; uses in-place SetLabel to avoid the
// SetMenu rebuild crash on Windows reported in wailsapp/wails#5227.
func (ui *trayUI) refreshMetrics(svc *App) {
	m := svc.GetMetrics()
	running := svc.GetStatus() == "running"

	connLabel, reqLabel := metricLabels(m.ActiveConnections, m.TotalRequests)
	ui.connItem.SetLabel(connLabel)
	ui.reqItem.SetLabel(reqLabel)
	ui.menu.Update()

	// Icon state — recompute every tick because error freshness expires.
	// macOS uses a template image (single-colour); the running/stopped/error
	// distinction there comes from a tooltip suffix until per-state template
	// art is added. On Linux/Windows, swap the regular icon variant for one
	// of three colour states (future enhancement: ship green/red variants).
	ui.tray.SetTooltip(trayTooltip(running, svc.boundPort(), m.LastError, m.LastErrorAt))
}

// errorIsFresh reports whether an error was recorded within the freshness
// window — used to drive the "error" tray state without leaving it sticky
// after the proxy recovers.
func errorIsFresh(at time.Time) bool {
	if at.IsZero() {
		return false
	}
	return time.Since(at) < errorFreshness
}

// populateProfileSubmenu fills the profile submenu with one radio item per
// profile, marking the active one. Called both at build time and whenever
// profiles are added/renamed/deleted.
//
// Rebuilds clear the previous radio items rather than mutating them in place
// because the profile *list* itself can change (rename, delete). The Wails
// bug that bites on full menu rebuild is in `SystemTray.SetMenu`; rebuilding
// a single submenu's children avoids it.
func (ui *trayUI) populateProfileSubmenu(svc *App, submenu *application.Menu, active string) {
	ui.profileItemsMu.Lock()
	defer ui.profileItemsMu.Unlock()

	submenu.Clear()
	ui.profileItems = ui.profileItems[:0]

	for _, p := range svc.ListProfiles() {
		name := p.Name
		item := submenu.AddRadio(name, name == active)
		item.OnClick(func(_ *application.Context) {
			go ui.switchProfile(svc, name)
		})
		ui.profileItems = append(ui.profileItems, item)
	}
}

// switchProfile is the tray-driven profile switch handler. It enforces the
// proxy-stopped invariant by stopping the proxy first when needed; the
// underlying SwitchProfile binding still rejects switches under a live
// proxy, but stopping ahead of time avoids surprising the user.
func (ui *trayUI) switchProfile(svc *App, name string) {
	if svc.GetStatus() == "running" {
		_ = svc.StopProxy()
	}
	_, _ = svc.SwitchProfile(name)
}
