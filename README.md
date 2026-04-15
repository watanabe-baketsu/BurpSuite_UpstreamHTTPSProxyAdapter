# Burp Upstream HTTPS Proxy Adapter

A native desktop application that sits between **Burp Suite** and an **authenticated HTTPS proxy** (e.g. Squid on AWS). It lets you keep Burp's full HTTP history while routing all traffic through a remote, credential-protected HTTPS proxy.

```
Burp Browser ─▶ Burp Proxy ─▶ This Adapter (localhost) ─▶ HTTPS Proxy (remote) ─▶ Internet
```

Built with **Wails v2** (Go + React + TypeScript). Runs on macOS, Windows, and Linux.

---

## Why This Exists

Burp Suite's upstream proxy feature supports plain HTTP proxies natively, but connecting to an **HTTPS proxy with Basic authentication** requires an intermediate adapter. This app handles the TLS handshake and credential injection transparently, so Burp only sees a normal local HTTP proxy.

## Features

- Local HTTP proxy that accepts both `CONNECT` tunnels and plain HTTP requests from Burp
- Upstream connection to HTTPS proxies with TLS + Basic auth
- Custom CA certificate support for private proxy infrastructure
- OS keychain integration for password storage (macOS Keychain, Windows Credential Manager, Linux Secret Service)
- Built-in diagnostics: TLS handshake, auth, CONNECT tunnel, and HTTP connectivity tests
- Live log viewer and connection metrics
- Cross-platform: macOS (arm64 / amd64), Windows (amd64), Linux (amd64)

## Quick Start

### 1. Download

Download the latest release from [Releases](https://github.com/watanabe-baketsu/BurpSuite_UpstreamHTTPSProxyAdapter/releases).

| Platform | File |
|----------|------|
| macOS (Apple Silicon) | `burp-upstream-adapter-darwin-arm64.zip` |
| macOS (Intel) | `burp-upstream-adapter-darwin-amd64.zip` |
| Windows | `burp-upstream-adapter-windows-amd64.zip` |
| Linux | `burp-upstream-adapter-linux-amd64.tar.gz` |

### 2. Configure the Adapter

1. Launch the application
2. In the **Configuration** tab, fill in:
   - **Upstream Proxy**: host, port, username, password of your HTTPS proxy
   - **Local Listener**: leave defaults (`127.0.0.1:18080`) or customize
3. Click **Save**, then **Start Proxy**
4. Use the **Diagnostics** tab to verify connectivity

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
| [Go](https://go.dev/dl/) | 1.23+ |
| [Node.js](https://nodejs.org/) | 20+ |
| [Wails CLI](https://wails.io/docs/gettingstarted/installation) | v2.11+ |

**Linux only** — install GTK and WebKit development libraries:

```bash
# Ubuntu / Debian
sudo apt-get install libgtk-3-dev libwebkit2gtk-4.0-dev build-essential pkg-config
```

### Build

```bash
# Install Wails CLI (if not already installed)
go install github.com/wailsapp/wails/v2/cmd/wails@v2.11.0

# Production build
wails build

# Development mode with hot-reload
wails dev
```

The built binary is in `build/bin/`.

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

Set upstream proxy connection details, TLS settings, timeouts, and local listener address. Save and Start/Stop the proxy from here.

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

## Documentation

Detailed technical documentation is in the [`docs/`](docs/) folder:

- [Architecture](docs/architecture.md) — system design, data flow, package structure
- [Configuration Reference](docs/configuration.md) — all settings with explanations
- [Development Guide](docs/development.md) — how to build, test, and contribute
- [Proxy Protocol](docs/proxy-protocol.md) — how CONNECT and HTTP forwarding work

## License

[MIT](LICENSE)
