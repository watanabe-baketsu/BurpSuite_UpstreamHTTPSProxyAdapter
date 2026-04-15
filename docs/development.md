# Development Guide

## Prerequisites

| Tool | Version | Install |
|------|---------|---------|
| Go | 1.23+ | [go.dev/dl](https://go.dev/dl/) |
| Node.js | 20+ | [nodejs.org](https://nodejs.org/) |
| Wails CLI | v2.11+ | `go install github.com/wailsapp/wails/v2/cmd/wails@v2.11.0` |

### Linux-specific dependencies

```bash
# Ubuntu / Debian
sudo apt-get install libgtk-3-dev libwebkit2gtk-4.0-dev build-essential pkg-config

# Fedora
sudo dnf install gtk3-devel webkit2gtk4.0-devel gcc-c++ pkgconf-pkg-config

# Arch
sudo pacman -S gtk3 webkit2gtk-4.1 base-devel
```

### Verify your environment

```bash
wails doctor
```

## Development Workflow

### Running in dev mode

```bash
wails dev
```

This starts the Go backend and the Vite dev server with hot-reload for the frontend. Changes to Go files trigger a rebuild; changes to React/TypeScript files are hot-reloaded instantly.

### Building for production

```bash
wails build
```

Output: `build/bin/burp-upstream-adapter` (or `.app` on macOS, `.exe` on Windows).

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
npm install
npx tsc --noEmit    # typecheck
npm run build       # production build
npm run dev         # vite dev server (standalone, no Go backend)
```

## Project Structure

See [architecture.md](architecture.md) for the full package breakdown.

Key entry points:
- `main.go` — Wails app initialization, window config, embed directive
- `app.go` — all methods exposed to the frontend via Wails bindings
- `internal/adapter/server.go` — proxy server Start/Stop lifecycle
- `internal/adapter/connect.go` — CONNECT tunnel handler (most critical code)
- `frontend/src/App.tsx` — single-component React UI with 4 tabs

## Adding a New Wails Binding

1. Add a public method to `App` struct in `app.go`
2. Run `wails dev` or `wails build` — bindings are auto-generated in `frontend/wailsjs/go/main/App.{js,d.ts}`
3. Import and call from React: `import { MyMethod } from '../wailsjs/go/main/App'`
4. For new struct types used in bindings, they appear in `frontend/wailsjs/go/models.ts`

## Adding a Backend → Frontend Event

```go
// In Go (app.go or any code with access to a.ctx):
runtime.EventsEmit(a.ctx, "my-event", payload)

// In React (App.tsx):
import { EventsOn } from '../wailsjs/runtime/runtime';

useEffect(() => {
  EventsOn('my-event', (data) => {
    console.log('received:', data);
  });
}, []);
```

## Test Architecture

Tests are organized by package:

| Package | Test type | What it tests |
|---------|-----------|---------------|
| `internal/config` | Unit | Validation rules, save/load, defaults |
| `internal/upstream` | Unit | Basic auth encoding, TLS config building, CA loading |
| `internal/adapter` | Integration | Full proxy flow with mock upstream (httptest.NewTLSServer) |

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

## CI Pipelines

### ci.yml (every PR and main push)

| Job | Runs on | What it does |
|-----|---------|--------------|
| `go-test` | ubuntu, macos, windows | `go vet` + `go test -race` on all 3 platforms |
| `frontend` | ubuntu | `npm ci` + `tsc --noEmit` + `npm run build` |
| `wails-build` | ubuntu | Full `wails build` (only on main push or `full-build` label) |

### release.yml (manual trigger)

Triggered via `workflow_dispatch` with a version tag input. Builds on 4 platform/arch combinations and creates a GitHub Release.

See [release.yml](../.github/workflows/release.yml) for details.

## Regenerating the App Icon

```bash
python3 scripts/generate_icon.py
```

Requires Pillow (`pip3 install Pillow`). Outputs:
- `build/appicon.png` (1024x1024, macOS/Linux)
- `build/windows/icon.ico` (multi-resolution, Windows)

## Code Style

- Go: standard `gofmt` formatting. No additional linter config.
- TypeScript: strict mode enabled in `tsconfig.json`.
- CSS: vanilla CSS with CSS custom properties (no preprocessor).
- Commit messages: [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`, `ci:`, `docs:`).
