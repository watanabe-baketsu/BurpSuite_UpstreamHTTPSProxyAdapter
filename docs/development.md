# Development Guide

## Prerequisites

| Tool | Version | Install |
|------|---------|---------|
| Go | 1.25+ | [go.dev/dl](https://go.dev/dl/) |
| Bun | 1.3+ | `curl -fsSL https://bun.sh/install \| bash` ([alternatives](https://bun.sh/docs/installation)) |
| Wails3 CLI | `v3.0.0-alpha.78` | `go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-alpha.78` |
| Task | 3.x | `go install github.com/go-task/task/v3/cmd/task@latest` (or `brew install go-task`) |

> The frontend toolchain uses **bun** instead of npm. bun reads the same `package.json`, so existing scripts (`dev`, `build`, `preview`) work unchanged. The lockfile is `frontend/bun.lock` (text format since bun 1.3) and is committed to the repo. If you have only npm, you can still use `npm ci` / `npm run build` — the scripts are compatible — but the CI / Taskfile expect bun.

### Linux-specific dependencies

The system tray relies on `libayatana-appindicator3` in addition to the GTK + WebKit packages.

```bash
# Ubuntu / Debian (uses webkit2gtk-4.1, which Wails v3 targets by default)
sudo apt-get install \
  libgtk-3-dev libwebkit2gtk-4.1-dev libayatana-appindicator3-dev \
  build-essential pkg-config

# Fedora
sudo dnf install gtk3-devel webkit2gtk4.1-devel libayatana-appindicator3-devel gcc-c++ pkgconf-pkg-config

# Arch
sudo pacman -S gtk3 webkit2gtk-4.1 libayatana-appindicator base-devel
```

### Verify your environment

```bash
wails3 doctor
```

## Development Workflow

### Running in dev mode

```bash
wails3 dev
```

This starts the Vite dev server, generates bindings, builds the Go binary in dev mode, and runs it — all in one process. Saving a `.go` file triggers a rebuild + restart; saving a `.tsx` / `.ts` file is hot-reloaded by Vite.

`wails3 dev` reads its config from [`build/config.yml`](../build/config.yml) and orchestrates:

| Step | Command |
|------|---------|
| 1. Vite dev server | `wails3 task common:dev:frontend` (in background) |
| 2. Build dev binary | `wails3 build DEV=true` (blocking) |
| 3. Run binary | `wails3 task run` (primary process) |

If you prefer two-shell mode (e.g. you want to run the Go process under `dlv`):

```bash
# Terminal 1 — Vite dev server (via bun)
wails3 task common:dev:frontend
# or directly:
( cd frontend && bun run dev )

# Terminal 2 — Go binary against the dev server
go run .
```

### Building for production

```bash
# Single command — handles bindings + bun install + Vite build + go build per platform
wails3 task build       # current OS
wails3 task package     # build + .app / .deb-.rpm-.AppImage / NSIS-MSIX

# Or invoke a specific platform task:
wails3 task darwin:build
wails3 task darwin:package
wails3 task linux:build
wails3 task linux:package
wails3 task windows:build
wails3 task windows:package
```

Output is `bin/burp-upstream-adapter` (or `.app` / `.exe` on the appropriate platform). The macOS `.app` bundle is ad-hoc-signed by `wails3 task darwin:package`; the release-macos workflow re-signs it with Developer ID and notarizes on top.

### Running tests

```bash
# All tests
go test ./internal/... -v

# With race detector (recommended)
go test ./internal/... -v -race

# Specific package
go test ./internal/adapter/... -v -run TestCONNECT

# With coverage
go test ./internal/... -coverprofile=coverage.out
go tool cover -html=coverage.out
```

### Frontend only

```bash
cd frontend
bun install
bun x tsc --noEmit  # typecheck
bun run build       # production build
bun run dev         # vite dev server (standalone, no Go backend)
```

## Project Structure

See [architecture.md](architecture.md) for the full package breakdown.

Key entry points:
- `main.go` — Wails v3 application bootstrap (`application.New(...) → Window.NewWithOptions → app.Run()`)
- `app.go` — the singleton `App` service whose exported methods are bound to the frontend
- `tray.go` — system-tray menu, status/metrics ticker, profile-switch submenu
- `internal/adapter/server.go` — proxy server Start/Stop lifecycle
- `internal/adapter/connect.go` — CONNECT tunnel handler (most critical code)
- `frontend/src/App.tsx` — single-component React UI with 4 tabs
- `frontend/src/wails-api.ts` — thin shim over the auto-generated bindings + `@wailsio/runtime` event bus

## Adding a New Wails Binding

1. Add an exported method on the `*App` struct in `app.go` (lowercase methods stay package-private and are *not* exposed to the frontend).
2. Re-run the binding generator:
   ```bash
   wails3 generate bindings -ts -d frontend/bindings
   ```
3. Import and call from React (via the shim):
   ```ts
   import { MyMethod } from './wails-api';
   ```
4. New struct types appear under `frontend/bindings/burp-upstream-adapter/models.ts` (or per-package `models.ts` files for nested packages).

## Adding a Backend → Frontend Event

```go
// In Go (anywhere with access to *application.App, e.g. as a.app on the App service):
a.app.Event.Emit("my-event", payload)

// In React (App.tsx) — use the wails-api shim's EventsOn/Off helpers:
import { EventsOn, EventsOff } from './wails-api';

useEffect(() => {
  EventsOn('my-event', (data) => {
    console.log('received:', data);
  });
  return () => EventsOff('my-event');
}, []);
```

For typed event payloads, register the event type once in `main.go` so the binding generator emits a typed wrapper:

```go
func init() {
  application.RegisterEvent[string]("my-event")
}
```

## Test Architecture

Tests are organized by package:

| Package | Test type | What it tests |
|---------|-----------|---------------|
| `internal/config` | Unit | Validation rules, save/load, defaults, profile-name validation, legacy config migration |
| `internal/upstream` | Unit | Basic auth encoding, TLS config building, CA loading |
| `internal/adapter` | Integration | Full proxy flow with mock upstream (`httptest.NewTLSServer`), metrics counters |

> The system tray (`tray.go`), Wails service wiring (`app.go`), and `main.go` are not unit-tested — they require a Wails runtime. Verify these by running `go run .` locally and exercising the tray menu.

### Integration test design

`internal/adapter/adapter_test.go` creates a mock upstream HTTPS proxy using `httptest.NewTLSServer`. The mock:
- Validates `Proxy-Authorization` headers against expected credentials
- Handles `CONNECT` by dialing the target and relaying bytes
- Handles plain HTTP by forwarding the request

This allows testing the full path: client → adapter → upstream → target, all in-process.

### Running a subset

```bash
# Only CONNECT tests
go test ./internal/adapter/... -v -run TestCONNECT

# Only config validation tests
go test ./internal/config/... -v -run TestValidate
```

### Manually verifying the system tray

The tray code path is not exercised by `go test`. To verify:

```bash
go run .
```

Then check:

1. The menu-bar / system-tray icon appears.
2. Right-click (macOS) or click (other platforms) shows the menu with **Status**, **Profile**, **Start/Stop**, metrics, **Profile** submenu, **Show Window**, **Quit**.
3. Clicking **Start Proxy** flips the label to "Stop Proxy" and the status row updates.
4. The metrics rows update once a second.
5. Closing the window with **Minimize to tray on close** enabled hides the window; **Show Window** brings it back.
6. **Quit** stops the proxy and exits the process cleanly.

Common pitfall on Linux: if the tray icon is invisible, `libayatana-appindicator3` is not installed or the desktop environment doesn't include the AppIndicator extension (GNOME requires it; KDE/Cinnamon ship it by default).

## CI Pipelines

### ci.yml (every PR and main push)

| Job | Runs on | What it does |
|-----|---------|--------------|
| `go-test` | ubuntu, macos, windows | `go vet` + `go test -race` on all 3 platforms |
| `frontend` | ubuntu | `wails3 generate bindings -ts` + `bun install --frozen-lockfile` + `bun x tsc --noEmit` + `bun run build` |
| `wails-build` | ubuntu | Full Linux build smoke-test via `wails3 task linux:build` |

### release.yml (manual trigger or `v*` tag push)

Orchestrates per-platform reusable workflows and publishes a GitHub Release named after the version tag (e.g. `v1.0.0`). Three platform builds run in parallel:

| Platform | Workflow | Build step | Output |
|----------|----------|------------|--------|
| macOS arm64 | `release-macos.yml` | `wails3 task darwin:package` (ad-hoc) → re-sign Developer ID + notarize | `.app` bundle, zipped |
| Windows amd64 | `release-windows.yml` | `wails3 task windows:build` | `.exe` zipped |
| Linux amd64 | `release-linux.yml` | `wails3 task linux:build` | binary in `tar.gz` |

The macOS workflow takes the ad-hoc-signed `.app` bundle from `wails3 task darwin:package` and re-signs it with Developer ID (overwriting the ad-hoc signature) before notarizing — this keeps the Wails-native packaging logic while preserving our existing notarization secrets and flow.

To switch Linux to AppImage/deb/rpm or Windows to NSIS/MSIX, change the workflow build step from `<os>:build` to `<os>:package` and update the artifact-collection step accordingly. The platform Taskfiles already define those tasks.

See [release.yml](../.github/workflows/release.yml) for the dispatch logic.

## Regenerating the App Icon

```bash
python3 scripts/generate_icon.py
```

Requires Pillow (`pip3 install Pillow`). Outputs:
- `build/appicon.png` (1024x1024, macOS/Linux)
- `build/windows/icon.ico` (multi-resolution, Windows)
- `build/tray/tray-template.png` (32x32 black + alpha, macOS menu-bar template)
- `build/tray/tray-regular.png` (32x32 colour, Linux/Windows tray)

## Code Style

- Go: standard `gofmt` formatting. No additional linter config.
- TypeScript: strict mode enabled in `tsconfig.json`.
- CSS: vanilla CSS with CSS custom properties (no preprocessor).
- Commit messages: [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`, `ci:`, `docs:`).
