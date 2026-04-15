# Proxy Protocol Details

This document explains how the adapter handles HTTP proxy requests at the protocol level.

## CONNECT Tunneling

CONNECT is used for all HTTPS traffic. This is the dominant path when Burp is proxying browser traffic.

### Full Sequence

```
Client (Burp)                    Adapter                         Upstream HTTPS Proxy                Target
    │                               │                                   │                              │
    │  CONNECT example.com:443      │                                   │                              │
    │  HTTP/1.1                     │                                   │                              │
    │──────────────────────────────▶│                                   │                              │
    │                               │  TLS handshake                    │                              │
    │                               │──────────────────────────────────▶│                              │
    │                               │  ◀─ TLS established ─▶           │                              │
    │                               │                                   │                              │
    │                               │  CONNECT example.com:443          │                              │
    │                               │  HTTP/1.1                         │                              │
    │                               │  Proxy-Authorization: Basic ...   │                              │
    │                               │──────────────────────────────────▶│                              │
    │                               │                                   │  TCP connect                 │
    │                               │                                   │─────────────────────────────▶│
    │                               │  HTTP/1.1 200 Connection          │                              │
    │                               │  Established                      │                              │
    │                               │◀──────────────────────────────────│                              │
    │                               │                                   │                              │
    │  HTTP/1.1 200 Connection      │                                   │                              │
    │  Established                  │                                   │                              │
    │◀──────────────────────────────│                                   │                              │
    │                               │                                   │                              │
    │  ◀════════════ bidirectional byte relay (io.Copy) ═══════════════════════════════════════════════▶│
    │  (TLS from Burp to target,    │  (encrypted bytes relayed         │  (encrypted bytes relayed    │
    │   adapter sees ciphertext)    │   without inspection)             │   without inspection)        │
```

### Implementation (connect.go)

1. **Hijack** the HTTP connection from Go's `http.Server` via `http.Hijacker`
2. **TLS-dial** the upstream proxy using `tls.DialWithDialer`
3. **Send CONNECT** with `Proxy-Authorization: Basic <base64(user:pass)>` over the TLS connection
4. **Read response**: if `200`, proceed; otherwise forward the error to Burp
5. **Reply `200 Connection Established`** to Burp
6. **Relay** bytes in both directions using two goroutines with `io.Copy`
7. **Clean shutdown**: `CloseWrite()` on half-close, then `Close()` both connections

### Error Handling

| Upstream Response | Adapter Behavior |
|-------------------|-----------------|
| 200 | Relay established, return 200 to Burp |
| 407 Proxy Authentication Required | Forward 407 to Burp, log error |
| 403 Forbidden | Forward to Burp, log |
| 502/503 | Return 502 to Burp with message |
| Connection refused | Return 502 to Burp |
| TLS handshake failure | Return 502 to Burp with TLS error details |
| Timeout | Return 504 to Burp |

## Plain HTTP Forwarding

For `http://` URLs (non-CONNECT). Less common with Burp but fully supported.

### Sequence

```
Client (Burp)                    Adapter                         Upstream HTTPS Proxy       Target
    │                               │                                   │                     │
    │  GET http://example.com/      │                                   │                     │
    │  HTTP/1.1                     │                                   │                     │
    │──────────────────────────────▶│                                   │                     │
    │                               │  Clone request                    │                     │
    │                               │  Clear RequestURI                 │                     │
    │                               │  Remove hop-by-hop headers        │                     │
    │                               │  Add Proxy-Authorization          │                     │
    │                               │                                   │                     │
    │                               │  http.Transport.RoundTrip()       │                     │
    │                               │  (Proxy = https://upstream)       │                     │
    │                               │──────────────────────────────────▶│                     │
    │                               │                                   │─────────────────────│
    │                               │                                   │◀────────────────────│
    │                               │  Response                         │                     │
    │                               │◀──────────────────────────────────│                     │
    │                               │                                   │                     │
    │                               │  Remove hop-by-hop headers        │                     │
    │  Response                     │  Copy remaining headers           │                     │
    │◀──────────────────────────────│                                   │                     │
```

### Implementation (forward_http.go)

1. **Clone** the incoming request (`r.Clone(r.Context())`)
2. **Clear `RequestURI`** (required for `http.Client` usage)
3. **Remove hop-by-hop headers** from the request
4. **Set `Proxy-Authorization`** header
5. **Forward** via `http.Transport` with `Proxy` set to the upstream HTTPS URL
6. **Remove hop-by-hop headers** from the response
7. **Copy** response headers and body back to Burp

## Hop-by-Hop Headers

These headers are specific to a single connection and must not be forwarded:

| Header | Purpose |
|--------|---------|
| `Connection` | Connection management |
| `Keep-Alive` | Keep-alive parameters |
| `Proxy-Authenticate` | Proxy auth challenge |
| `Proxy-Authorization` | Proxy auth credentials |
| `Proxy-Connection` | Non-standard proxy connection |
| `Te` | Transfer encoding preferences |
| `Trailer` | Trailer header fields |
| `Transfer-Encoding` | Message body encoding |
| `Upgrade` | Protocol upgrade |

Additionally, any headers listed in the `Connection` header value are also removed.

## Authentication

The adapter uses HTTP Basic authentication for the upstream proxy:

```
Proxy-Authorization: Basic base64(username:password)
```

The header is:
- **Added** to every CONNECT request and HTTP forwarding request sent to the upstream
- **Removed** from requests before forwarding to the final destination (hop-by-hop header)

The password is loaded from the OS keychain at proxy start time and held in memory for the lifetime of the proxy server.

## Timeouts and Deadlines

| Timeout | Where | Default | Purpose |
|---------|-------|---------|---------|
| Connect Timeout | `tls.DialWithDialer` | 30s | Time allowed for TCP + TLS handshake to upstream |
| Read Timeout | `http.Server.ReadTimeout` | 30s | Time allowed to read a full request from Burp |
| Write Timeout | `http.Server.WriteTimeout` | 0 (none) | Disabled for CONNECT tunnels (long-lived) |
| Idle Timeout | `http.Server.IdleTimeout` | 300s | Time before idle keep-alive connections are closed |
| Context Timeout | `context.WithTimeout` in CONNECT | 30s | Upstream CONNECT request timeout |

Write timeout is intentionally set to 0 because CONNECT tunnels are long-lived bidirectional streams. Setting a write timeout would kill active tunnels.
