# Configuration Reference

## Config File Location

```
~/.burp-upstream-adapter/adapter.config.json
```

The directory is created automatically on first save. The file is written with `0600` permissions (owner read/write only).

The on-disk schema is **profile-aware**: every saved upstream definition lives under `profiles.<name>`, with a top-level `active_profile` selector and a shared `local` listener block. A legacy single-upstream layout (pre-profile config files) is auto-migrated to a single profile named `default` on first load — no manual action required.

## Top-Level Schema

```jsonc
{
  "active_profile": "production-aws",
  "profiles": {
    "production-aws": { /* ProfileConfig */ },
    "staging":        { /* ProfileConfig */ }
  },
  "local": { /* LocalConfig */ }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `active_profile` | string | Name of the profile currently in use. Must be a key in `profiles`. |
| `profiles` | map<string, ProfileConfig> | All known profiles, keyed by name. |
| `local` | LocalConfig | Listener + UX settings. Shared across profiles (the adapter listens on a single socket). |

## Profile Config (`profiles.<name>`)

| Field | JSON key | Type | Default | Description |
|-------|----------|------|---------|-------------|
| Host | `host` | string | `""` | Hostname or IP of the HTTPS proxy. |
| Port | `port` | int | `3128` | Port of the HTTPS proxy. |
| Username | `username` | string | `""` | Proxy auth username. The password is **not** stored here — see [Password Storage](#password-storage). |
| Verify TLS | `verify_tls` | bool | `true` | Validate the proxy's TLS certificate. |
| Custom CA PEM | `custom_ca_pem` | string | `""` | **Inline PEM content** for a custom CA bundle (replaces the legacy `custom_ca_path` field). Multi-cert chains are supported. |
| Connect Timeout | `connect_timeout_sec` | int | `30` | Seconds to wait for the upstream TLS handshake + CONNECT. |
| Idle Timeout | `idle_timeout_sec` | int | `300` | Seconds before idle keep-alive connections are closed. |

### Profile name rules

Profile names are validated by a regex (see `internal/config/config.go`):

- 1 – 32 characters
- Letters, digits, hyphen, or underscore only

These rules are enforced both in the UI dialog (Create/Duplicate/Rename) and on the Go side, so an edited config file with an invalid profile name is rejected on load.

## Local Config (`local`)

| Field | JSON key | Type | Default | Description |
|-------|----------|------|---------|-------------|
| Bind Host | `bind_host` | string | `127.0.0.1` | IP address to listen on. Loopback only by default. |
| Bind Port | `bind_port` | int | `18080` | Port to listen on. |
| Minimize to tray on close | `minimize_to_tray_on_close` | bool | `false` | When `true`, clicking the window close button hides the window to the tray instead of quitting. |
| Hide Dock icon | `hide_dock_icon` | bool | `false` | macOS only. When `true`, the app launches as an accessory app (no Dock icon, no Cmd+Tab presence) — the menu-bar icon becomes the only visible UI. Takes effect on next launch. |

## Example Config File

```json
{
  "active_profile": "production-aws",
  "profiles": {
    "production-aws": {
      "host": "proxy.example.com",
      "port": 3128,
      "username": "proxy-user",
      "verify_tls": true,
      "custom_ca_pem": "-----BEGIN CERTIFICATE-----\nMIIBxTCCA...\n-----END CERTIFICATE-----\n",
      "connect_timeout_sec": 30,
      "idle_timeout_sec": 300
    },
    "staging": {
      "host": "staging-proxy.example.com",
      "port": 3128,
      "username": "stage-user",
      "verify_tls": true,
      "custom_ca_pem": "",
      "connect_timeout_sec": 10,
      "idle_timeout_sec": 60
    }
  },
  "local": {
    "bind_host": "127.0.0.1",
    "bind_port": 18080,
    "minimize_to_tray_on_close": true,
    "hide_dock_icon": false
  }
}
```

## Password Storage

The upstream proxy password is **never** included in the JSON config file. It is stored in the OS keychain under the service name `burp-upstream-adapter`, keyed by the **profile name + username** so that two profiles with different credentials don't collide.

| Platform | Backend | Tool to inspect |
|----------|---------|-----------------|
| macOS | Keychain Services | Keychain Access app or `security find-generic-password` |
| Windows | Credential Manager | Control Panel > Credential Manager |
| Linux | Secret Service API | `seahorse` (GNOME Keyring) or `secret-tool` |

Profile rename / duplicate / delete operations migrate or remove the corresponding keychain entries (best-effort — a missing entry is fine, but the password follows the rename).

### Manual keychain operations

**macOS** — view stored password:

```bash
security find-generic-password \
  -s "burp-upstream-adapter" \
  -a "<profile>:<username>" \
  -w
```

**macOS** — delete stored password:

```bash
security delete-generic-password \
  -s "burp-upstream-adapter" \
  -a "<profile>:<username>"
```

**Linux** — view stored password:

```bash
secret-tool lookup service burp-upstream-adapter username "<profile>:<username>"
```

> The exact account-key format is `<profile>:<username>` (constructed in `internal/keychain/keychain.go`). Use that as the `-a` value above.

## Validation Rules

The following rules are enforced when saving config or starting the proxy:

| Field | Rule |
|-------|------|
| `active_profile` | Required, must reference a key in `profiles` |
| `profiles` | At least one profile required |
| Profile name | 1–32 chars, `[A-Za-z0-9_-]+` |
| Upstream Host | Required, non-empty |
| Upstream Port | 1 – 65535 |
| Bind Host | Required, must be a valid IP address |
| Bind Port | 1 – 65535 |
| Connect Timeout | ≥ 1 second |
| Idle Timeout | ≥ 1 second |
| Custom CA PEM | If set, must parse as one or more X.509 certificates |

The active profile is always validated in full. Inactive profiles are validated with a lighter ruleset so a partially-configured profile can still be saved.

## Custom CA Certificates

If your upstream HTTPS proxy uses a certificate signed by a private CA, you can provide the CA's PEM certificate(s):

1. In the **Configuration** tab, click **Load** next to "Custom CA PEM".
2. Select a `.pem`, `.crt`, or `.cer` file containing the CA certificate(s).
3. Click **Save**.

The file content is **copied into the profile config** (the `custom_ca_pem` field). The original file path is not retained, so the file can be moved or deleted afterwards without breaking the adapter.

Standard PEM format with one or more certificates is supported:

```
-----BEGIN CERTIFICATE-----
MIIBxTCCA...
-----END CERTIFICATE-----
-----BEGIN CERTIFICATE-----
MIIBzDCCA...
-----END CERTIFICATE-----
```

## TLS Verification

| Setting | Behavior |
|---------|----------|
| `verify_tls: true` (default) | Full certificate chain validation. Requires a valid cert from a trusted CA or a custom CA. |
| `verify_tls: false` | Skips all certificate validation. **Use only for testing.** The UI displays a warning. |

> Disabling TLS verification makes the connection vulnerable to man-in-the-middle attacks between this adapter and the upstream proxy. It does not affect the security of the Burp-to-adapter connection (which is plain HTTP on localhost).

## Timeouts

| Timeout | Applies to | Recommendation |
|---------|-----------|----------------|
| Connect Timeout | TLS handshake + CONNECT request to upstream | 10–30s for local/regional proxies. Increase for high-latency links. |
| Idle Timeout | Inactive CONNECT tunnels and HTTP keep-alive connections | 300s (5 min) is usually sufficient. Increase if Burp's scanner causes connections to idle for longer. |

## System-Tray Behaviour

The tray icon reflects three states with a 30-second freshness window for errors:

| State | Trigger |
|-------|---------|
| Stopped | `IsRunning() == false` |
| Running | `IsRunning() == true` and no recent errors |
| Error (recent) | An error was recorded by the proxy within the last 30 seconds (`metrics.LastErrorAt` within `errorFreshness`) |

Tray menu items refresh every second via an in-process ticker (no `SetMenu` rebuilds — labels and enabled state are mutated in place to avoid the Windows tray crash reported in [wailsapp/wails#5227](https://github.com/wailsapp/wails/issues/5227)).

The two related toggles in the **Configuration** tab:

- **Minimize to tray on close** — closing the window keeps the app running in the tray. Without this toggle, closing the window destroys the window but the app remains alive (because `ApplicationShouldTerminateAfterLastWindowClosed = false`); the user must use **Quit** from the tray to fully exit.
- **Hide Dock icon (macOS)** — sets `MacOptions.ActivationPolicy = ActivationPolicyAccessory` at launch (LSUIElement-equivalent). Restart the app for the change to take effect.
