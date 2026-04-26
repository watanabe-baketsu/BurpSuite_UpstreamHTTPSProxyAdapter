# Burp Upstream HTTPS Proxy Adapter

A native desktop application that sits between **Burp Suite** and an **authenticated HTTPS proxy** (e.g. Squid on AWS). It lets you keep Burp's full HTTP history while routing all traffic through a remote, credential-protected HTTPS proxy.

```
Burp Browser ─▶ Burp Proxy ─▶ This Adapter (localhost) ─▶ HTTPS Proxy (remote) ─▶ Internet
```

Built with **Wails v3 (alpha)** (Go + React + TypeScript). Runs on macOS, Windows, and Linux. Includes a system tray / menu-bar icon with Start/Stop, status, metrics, and profile-switch controls.

---

## Why This Exists

Burp Suite's upstream proxy feature supports plain HTTP proxies natively, but connecting to an **HTTPS proxy with Basic authentication** requires an intermediate adapter. This app handles the TLS handshake and credential injection transparently, so Burp only sees a normal local HTTP proxy.

## Features

- Local HTTP proxy that accepts both `CONNECT` tunnels and plain HTTP requests from Burp
- Upstream connection to HTTPS proxies with TLS + Basic auth
- **Multiple profiles** — switch between environments (e.g. production / staging) without re-typing credentials each time
- **System tray / menu-bar icon** with Start/Stop, status, live metrics, and a profile-switch submenu (optional "minimize to tray on close" + macOS "hide Dock icon" toggles)
- Custom CA certificate support for private proxy infrastructure (PEM stored inline in the profile)
- OS keychain integration for password storage (macOS Keychain, Windows Credential Manager, Linux Secret Service)
- Built-in diagnostics: TLS handshake, auth, CONNECT tunnel, and HTTP connectivity tests
- Live log viewer and connection metrics
- Cross-platform: macOS (Apple Silicon / arm64), Windows (amd64), Linux (amd64)

## Quick Start

### 1. Download

Download the latest release from [Releases](https://github.com/watanabe-baketsu/BurpSuite_UpstreamHTTPSProxyAdapter/releases).

| Platform | File |
|----------|------|
| macOS (Apple Silicon) | `burp-upstream-adapter-darwin-arm64.zip` |
| Windows | `burp-upstream-adapter-windows-amd64.zip` |
| Linux | `burp-upstream-adapter-linux-amd64.tar.gz` |

### 2. Configure the Adapter

1. Launch the application. A `default` profile is created on first run.
2. In the **Configuration** tab, fill in:
   - **Profile**: keep `default` or click **New** / **Duplicate** / **Rename** to organise multiple environments.
   - **Upstream Proxy**: host, port, username, password of your HTTPS proxy.
   - **Local Listener**: leave defaults (`127.0.0.1:18080`) or customize.
   - **System Tray** (optional): toggle "Minimize to tray on close" or, on macOS, "Hide Dock icon" for a menu-bar-only experience.
3. Click **Save**, then **Start Proxy** (or use the tray's **Start Proxy** menu item).
4. Use the **Diagnostics** tab to verify connectivity.

To switch profiles later, use the dropdown in the header or the **Profile** submenu in the system tray. The proxy must be stopped before switching.

### 3. Configure Burp Suite

Open Burp Suite and navigate to:

**Settings > Network > Connections > Upstream proxy servers > Add**

| Field | Value |
|-------|-------|
| Destination host | `*` |
| Proxy host | `127.0.0.1` |
| Proxy port | `18080` |
| Authentication type | None |

> Authentication to the upstream HTTPS proxy is handled entirely by this adapter. Burp does not need credentials.

### 4. Browse

Open Burp's embedded browser or configure your browser to use Burp's proxy. All traffic will flow through the adapter to your HTTPS proxy, and Burp's HTTP history will capture everything as usual.

## Building from Source

### Prerequisites

| Tool | Version |
|------|---------|
| [Go](https://go.dev/dl/) | 1.25+ |
| [Bun](https://bun.sh/) | 1.x (frontend package manager + runner; replaces npm) |
| [Wails3 CLI](https://v3alpha.wails.io/) | `v3.0.0-alpha.78` |

> The frontend uses **bun** (not npm). Install with `curl -fsSL https://bun.sh/install | bash` or your package manager. bun reads the same `package.json` as npm and runs the same scripts; the lockfile is `bun.lock` (text-format, committed to the repo).

**Linux only** — install GTK, WebKit, and AppIndicator development libraries (the last is needed for the system tray):

```bash
# Ubuntu / Debian
sudo apt-get install \
  libgtk-3-dev libwebkit2gtk-4.1-dev libayatana-appindicator3-dev \
  build-essential pkg-config
```

### Build

The project uses the **standard Wails v3 Taskfile pipeline** (`wails3 task ...`), patched to use bun instead of npm. Install the toolchain once:

```bash
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-alpha.78
go install github.com/go-task/task/v3/cmd/task@latest   # or `brew install go-task`
```

Then for everyday development:

```bash
# Hot-reload dev mode (Vite + Go binary, restarts on .go changes)
wails3 dev

# Production build for the current OS (binary lands in bin/)
wails3 task build

# Production package for the current OS (.app on macOS, .deb/.rpm/.AppImage on Linux,
# NSIS/MSIX on Windows — see build/<os>/Taskfile.yml for details)
wails3 task package

# Just regenerate the TypeScript bindings (after editing exported Go methods)
wails3 task bindings

# Run the unit tests
wails3 task test
```

Direct `go run .` / `go build` still works for ad-hoc cases — `wails3 dev` is the convenience wrapper that runs the Vite dev server, the Go binary, and the bindings generator together with hot-reload on Go file changes.

The first build will run `bun install` automatically. The output binary is in `bin/`.

### Run Tests

```bash
go test ./internal/... -v -race
```

## Configuration

### Settings File

Non-secret settings are stored in JSON:

```
~/.burp-upstream-adapter/adapter.config.json
```

### Password Storage

Passwords are **never** written to the config file. They are stored in the OS keychain:

| Platform | Backend |
|----------|---------|
| macOS | Keychain (via Security framework) |
| Windows | Credential Manager |
| Linux | Secret Service (GNOME Keyring / KWallet) |

### Security Defaults

| Setting | Default | Notes |
|---------|---------|-------|
| Bind host | `127.0.0.1` | Loopback only; not exposed to the network |
| Verify TLS | `true` | Disabling shows a warning in the UI |
| Password logging | Disabled | Passwords are never written to logs |

## Application Tabs

### Configuration

Manage **profiles**, set upstream proxy connection details, TLS settings, timeouts, and local listener address. Toggle system-tray behaviour ("Minimize to tray on close", "Hide Dock icon" on macOS). Save and Start/Stop the proxy from here.

### Diagnostics

Four built-in tests to validate your setup before using Burp:

- **Test Upstream TLS** — verifies TLS handshake to the upstream proxy
- **Test Proxy Auth** — verifies Basic authentication credentials
- **Test CONNECT** — performs a full CONNECT tunnel to `example.com:443`
- **Test HTTP GET** — sends an HTTP request through the proxy to `http://example.com/`

### Activity

- Live log stream with color-coded severity levels
- Real-time metrics: active connections, total requests, bytes in/out
- Last error display

### Burp Setup

Step-by-step guide for configuring Burp Suite, dynamically populated with your current adapter settings.

## System Tray

The tray icon shows the current proxy state and gives quick access to the most common actions without opening the window:

- **Status: Running (port) / Stopped** — live state, refreshes every second
- **Profile: ${active}** — current active profile name
- **Start Proxy / Stop Proxy** — toggle the proxy
- **Active connections** / **Total requests** — live metrics
- **Profile** submenu — radio list of all profiles for quick switching (stops the proxy first if running)
- **Show Window** — restore the window if hidden
- **Quit** — stop the proxy and exit the app

## Documentation

Detailed technical documentation is in the [`docs/`](docs/) folder:

- [Architecture](docs/architecture.md) — system design, data flow, package structure
- [Configuration Reference](docs/configuration.md) — all settings with explanations
- [Development Guide](docs/development.md) — how to build, test, and contribute
- [Proxy Protocol](docs/proxy-protocol.md) — how CONNECT and HTTP forwarding work

## License

[Apache License 2.0](LICENSE)
