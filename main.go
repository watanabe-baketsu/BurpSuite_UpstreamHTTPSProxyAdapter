package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var appIcon []byte

func init() {
	// Pre-register typed event payloads so the TypeScript binding generator
	// can emit accurate signatures on the frontend.
	application.RegisterEvent[string]("status")
}

func main() {
	svc := NewApp()

	// Activation policy: when the user has opted into "Hide dock icon", the app
	// runs as a macOS accessory (LSUIElement-equivalent) — only the tray icon
	// is visible. Otherwise it behaves as a normal foreground app. The flag is
	// resolved before app.New so the policy is applied at native launch.
	activation := application.ActivationPolicyRegular
	if svc.preferAccessoryActivation() {
		activation = application.ActivationPolicyAccessory
	}

	app := application.New(application.Options{
		Name:        "Burp Upstream HTTPS Proxy Adapter",
		Description: "HTTPS upstream proxy adapter for Burp Suite",
		Icon:        appIcon,
		Services: []application.Service{
			application.NewService(svc),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false,
			ActivationPolicy: activation,
		},
		OnShutdown: svc.onAppShutdown,
	})

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Burp Upstream HTTPS Proxy Adapter",
		Width:            960,
		Height:           720,
		MinWidth:         800,
		MinHeight:        600,
		BackgroundColour: application.NewRGBA(24, 24, 30, 255),
		URL:              "/",
	})

	// Close-button policy:
	//   * Already quitting (tray "Quit", Cmd-Q, etc.) → let the close run
	//     so [NSApp terminate:nil] can finish.
	//   * MinimizeToTrayOnClose=true  → hide the window, keep the tray alive.
	//   * MinimizeToTrayOnClose=false → request an app-level Quit so the
	//                                   tray icon disappears too.
	//
	// We need the explicit Quit because Mac.ApplicationShouldTerminateAfterLastWindowClosed
	// is set to false (so the tray-only mode can keep running with no window).
	// Without Quit, the default close would destroy the window but leave
	// the app — and its tray icon — alive in the background.
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if svc.IsQuitting() {
			return
		}
		if svc.minimizeToTrayOnClose() {
			window.Hide()
			e.Cancel()
			return
		}
		// Cancel the native close, then route through svc.Quit so the
		// quitting flag is set before NSApp.terminate fires close again on
		// this same window (which would otherwise re-enter this hook).
		// Dispatched on a goroutine so the hook returns quickly.
		e.Cancel()
		go svc.Quit()
	})

	// macOS Dock icon click: bring the (possibly hidden) window back.
	app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(_ *application.ApplicationEvent) {
		window.Show()
	})

	svc.attachWindow(window)
	svc.attachApp(app)

	tray := app.SystemTray.New()
	stopTray := BuildTray(svc, app, tray, window)
	// Stop the tray ticker as the very first step of shutdown so its
	// 1-second loop stops dispatching to the macOS main thread while
	// onAppShutdown is draining the proxy. The defer is the safety net for
	// abnormal exits where OnShutdown is never invoked; stopTray is
	// idempotent.
	svc.addShutdownHook(stopTray)
	defer stopTray()

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
