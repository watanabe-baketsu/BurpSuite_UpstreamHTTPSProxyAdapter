# Configuration Reference

## Config File Location

```
~/.burp-upstream-adapter/adapter.config.json
```

The directory is created automatically on first save. The file is written with `0600` permissions (owner read/write only).

## Settings

### Upstream Proxy

| Field | JSON key | Type | Default | Description |
|-------|----------|------|---------|-------------|
| Host | `upstream.host` | string | `""` | Hostname or IP of the HTTPS proxy |
| Port | `upstream.port` | int | `3128` | Port of the HTTPS proxy |
| Username | `upstream.username` | string | `""` | Proxy auth username |
| Verify TLS | `upstream.verify_tls` | bool | `true` | Validate the proxy's TLS certificate |
| Custom CA Path | `upstream.custom_ca_path` | string | `""` | Path to a PEM file with custom CA certificates |
| Connect Timeout | `upstream.connect_timeout_sec` | int | `30` | Seconds to wait for upstream TLS connection |
| Idle Timeout | `upstream.idle_timeout_sec` | int | `300` | Seconds before idle connections are closed |

### Local Listener

| Field | JSON key | Type | Default | Description |
|-------|----------|------|---------|-------------|
| Bind Host | `local.bind_host` | string | `127.0.0.1` | IP address to listen on |
| Bind Port | `local.bind_port` | int | `18080` | Port to listen on |

## Example Config File

```json
{
  "upstream": {
    "host": "proxy.example.com",
    "port": 3128,
    "username": "proxy-user",
    "verify_tls": true,
    "custom_ca_path": "",
    "connect_timeout_sec": 30,
    "idle_timeout_sec": 300
  },
  "local": {
    "bind_host": "127.0.0.1",
    "bind_port": 18080
  }
}
```

## Password Storage

The upstream proxy password is **never** included in the JSON config file. It is stored in the OS keychain under the service name `burp-upstream-adapter`.

| Platform | Backend | Tool to inspect |
|----------|---------|-----------------|
| macOS | Keychain Services | Keychain Access app or `security find-generic-password` |
| Windows | Credential Manager | Control Panel > Credential Manager |
| Linux | Secret Service API | `seahorse` (GNOME Keyring) or `secret-tool` |

### Manual keychain operations

**macOS** — view stored password:
```bash
security find-generic-password -s "burp-upstream-adapter" -a "<username>" -w
```

**macOS** — delete stored password:
```bash
security delete-generic-password -s "burp-upstream-adapter" -a "<username>"
```

**Linux** — view stored password:
```bash
secret-tool lookup service burp-upstream-adapter username "<username>"
```

## Validation Rules

The following rules are enforced when saving config or starting the proxy:

| Field | Rule |
|-------|------|
| Upstream Host | Required, non-empty |
| Upstream Port | 1 – 65535 |
| Bind Host | Required, must be a valid IP address |
| Bind Port | 1 – 65535 |
| Connect Timeout | >= 1 second |
| Idle Timeout | >= 1 second |
| Custom CA Path | If set, file must exist |

## Custom CA Certificates

If your upstream HTTPS proxy uses a certificate signed by a private CA, you can provide the CA's PEM certificate:

1. In the **Configuration** tab, click **Browse** next to "Custom CA PEM"
2. Select a `.pem`, `.crt`, or `.cer` file containing the CA certificate(s)
3. Click **Save**

The PEM file can contain multiple certificates (certificate chain). Standard PEM format:

```
-----BEGIN CERTIFICATE-----
MIIBxTCCA...
-----END CERTIFICATE-----
```

## TLS Verification

| Setting | Behavior |
|---------|----------|
| `verify_tls: true` (default) | Full certificate chain validation. Requires valid cert from a trusted CA or a custom CA. |
| `verify_tls: false` | Skips all certificate validation. **Use only for testing.** The UI displays a warning. |

> Disabling TLS verification makes the connection vulnerable to man-in-the-middle attacks between this adapter and the upstream proxy. It does not affect the security of the Burp-to-adapter connection (which is plain HTTP on localhost).

## Timeouts

| Timeout | Applies to | Recommendation |
|---------|-----------|----------------|
| Connect Timeout | TLS handshake + CONNECT request to upstream | 10-30s for local/regional proxies. Increase for high-latency links. |
| Idle Timeout | Inactive CONNECT tunnels and HTTP keep-alive connections | 300s (5 min) is usually sufficient. Increase if Burp's scanner causes connections to idle for longer. |
