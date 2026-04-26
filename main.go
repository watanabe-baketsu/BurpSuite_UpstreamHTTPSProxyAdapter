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

	// Intercept the close button so it can hide-to-tray when the user has
	// opted in. Without that opt-in we fall through to the default close,
	// which (with ApplicationShouldTerminateAfterLastWindowClosed=false on
	// macOS) destroys the window but keeps the tray alive.
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if svc.minimizeToTrayOnClose() {
			window.Hide()
			e.Cancel()
		}
	})

	// macOS Dock icon click: bring the (possibly hidden) window back.
	app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(_ *application.ApplicationEvent) {
		window.Show()
	})

	svc.attachWindow(window)
	svc.attachApp(app)

	tray := app.SystemTray.New()
	stopTray := BuildTray(svc, app, tray, window)
	defer stopTray()

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
