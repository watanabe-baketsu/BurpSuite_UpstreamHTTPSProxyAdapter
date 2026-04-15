# Architecture

## Overview

This application is a local HTTP proxy that chains requests to an upstream HTTPS proxy with Basic authentication. It is designed to be transparent to Burp Suite — Burp sees a normal HTTP proxy on localhost, while the adapter handles TLS and credential injection behind the scenes.

```
┌─────────────┐    ┌─────────────┐    ┌──────────────────────┐    ┌───────────┐    ┌──────────┐
│ Burp Browser │───▶│ Burp Proxy  │───▶│ This Adapter (:18080)│───▶│ HTTPS     │───▶│ Internet │
│              │    │ (MITM)      │    │ - TLS connect        │    │ Squid     │    │          │
│              │    │             │    │ - Basic auth inject  │    │ Proxy     │    │          │
└─────────────┘    └─────────────┘    └──────────────────────┘    └───────────┘    └──────────┘
                         │                       │
                    HTTP history           No MITM here
                    captured here          (byte relay only)
```

## Technology Stack

| Layer | Technology |
|-------|-----------|
| Desktop framework | [Wails v2](https://wails.io/) |
| Backend | Go 1.23+ (standard library + minimal deps) |
| Frontend | React 18 + TypeScript + Vite |
| Password storage | [go-keyring](https://github.com/zalando/go-keyring) (macOS Keychain, Windows Credential Manager, Linux Secret Service) |
| IPC | Wails runtime bindings (Go functions callable from JS) |
| Events | Wails `runtime.EventsEmit` (Go → Frontend push) |

## Package Structure

```
.
├── main.go                          # Wails app bootstrap
├── app.go                           # Wails-bound methods (GUI ↔ Go bridge)
│
├── internal/
│   ├── config/
│   │   ├── config.go                # Config model, validation, JSON persistence
│   │   └── config_test.go
│   │
│   ├── keychain/
│   │   └── keychain.go              # OS keychain read/write/delete
│   │
│   ├── adapter/
│   │   ├── server.go                # HTTP server lifecycle (Start/Stop)
│   │   ├── connect.go               # CONNECT tunnel handler
│   │   ├── forward_http.go          # Plain HTTP forwarding handler
│   │   ├── headers.go               # Hop-by-hop header removal
│   │   ├── metrics.go               # Atomic counters for live stats
│   │   ├── adapter_test.go          # Integration tests (mock upstream)
│   │   └── headers_test.go
│   │
│   ├── upstream/
│   │   ├── dial_tls.go              # TLS dialer, custom CA loading
│   │   ├── auth.go                  # Basic auth header generation
│   │   ├── healthcheck.go           # Four diagnostic checks
│   │   ├── auth_test.go
│   │   └── dial_tls_test.go
│   │
│   └── logging/
│       └── logging.go               # Ring-buffer logger with event callback
│
├── frontend/
│   └── src/
│       ├── App.tsx                   # Main UI (4 tabs)
│       ├── types/index.ts           # TypeScript type definitions
│       ├── main.tsx                  # React entry point
│       └── style.css                # Dark theme stylesheet
│
├── build/                            # Wails build assets (icons, manifests)
├── scripts/
│   └── generate_icon.py             # Icon generator (Pillow)
└── .github/workflows/
    ├── ci.yml                        # PR / main test pipeline
    └── release.yml                   # Cross-platform release builds
```

## Data Flow

### CONNECT Tunnel (HTTPS sites)

This is the primary path for all HTTPS traffic from Burp.

```
1. Burp sends:     CONNECT example.com:443 HTTP/1.1
2. Adapter:        TLS-connect to upstream proxy
3. Adapter sends:  CONNECT example.com:443 HTTP/1.1
                   Proxy-Authorization: Basic <base64>
4. Upstream:       HTTP/1.1 200 Connection Established
5. Adapter:        HTTP/1.1 200 Connection Established  →  Burp
6. Bidirectional:  Burp ←─ io.Copy ─→ Upstream
                   (encrypted bytes, adapter doesn't inspect)
```

Key implementation details:
- Uses `http.Hijacker` to take over the raw TCP connection from Go's HTTP server
- Bidirectional relay via two `io.Copy` goroutines with `sync.WaitGroup`
- `CloseWrite()` for clean half-close when one side finishes
- Bytes transferred are tracked in atomic metrics

### Plain HTTP Forwarding

For non-HTTPS requests (rare with Burp, but supported).

```
1. Burp sends:     GET http://example.com/ HTTP/1.1
2. Adapter:        Clone request, clear RequestURI
                   Remove hop-by-hop headers
                   Set Proxy-Authorization header
3. Adapter:        http.Transport.RoundTrip() with Proxy = https://upstream
4. Response:       Remove hop-by-hop headers, copy to Burp
```

## State Management

### Backend State

| State | Storage | Lifecycle |
|-------|---------|-----------|
| Config (non-secret) | `~/.burp-upstream-adapter/adapter.config.json` | Persists across restarts |
| Password | OS keychain | Persists across restarts |
| Proxy server | `adapter.Server` struct | In-memory, created on Start |
| Metrics | `adapter.Metrics` (atomics) | In-memory, reset on restart |
| Logs | `logging.Logger` ring buffer (1000 entries) | In-memory, reset on restart |

### Frontend ↔ Backend Communication

| Direction | Mechanism | Example |
|-----------|-----------|---------|
| Frontend → Backend | Wails bound methods (async call) | `StartProxy()`, `SaveConfig()` |
| Backend → Frontend | `runtime.EventsEmit()` | `"log"` event, `"status"` event |
| Frontend polling | `setInterval` + bound method | `GetMetrics()` every 1 second |

## Concurrency Model

- The proxy server runs in a separate goroutine from Wails' main loop
- Each incoming connection spawns a goroutine (via `http.Server`)
- CONNECT tunnels have two additional goroutines for bidirectional relay
- Shutdown uses `context.WithCancel` to signal all goroutines, then `http.Server.Shutdown()` for graceful drain
- Metrics use `sync/atomic` for lock-free counter updates
- The `Server.mu` mutex protects Start/Stop state transitions only

## Security Model

1. **Password isolation**: Passwords flow from keychain → memory → auth header. Never serialized to disk or logs.
2. **Loopback binding**: Default bind is `127.0.0.1`, preventing network exposure.
3. **TLS verification**: Enabled by default. Disabling requires explicit user action and shows a persistent UI warning.
4. **No MITM**: The adapter relays encrypted bytes without inspection. Burp handles MITM upstream of this adapter.
5. **Hop-by-hop cleanup**: `Proxy-Authorization` and other hop-by-hop headers are stripped from forwarded requests to prevent credential leakage to destination servers.
