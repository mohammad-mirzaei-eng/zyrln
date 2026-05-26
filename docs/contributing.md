# Contributing

For a high-level overview of what each component does, see the [Components table in the README](../README.md#components).

## Project Structure

```
zyrln/
├── platforms/
│   ├── desktop/        # Desktop CLI binary (main package)
│   │   ├── main.go     # CLI flags, probe runner, relay-fetch, proxy launcher
│   │   └── main_test.go
│   └── mobile/         # gomobile bindings for Android
│       └── mobile.go   # Exported API: StartTunnel, StartDirect, Stop, Ping, …
│
├── relay/
│   ├── README.md       # Package map (see relay/README.md)
│   ├── core/           # Stable API re-export (desktop + mobile import this)
│   ├── route/          # Direct / domestic / fragmentation routing
│   ├── appscript/      # Domain-fronted HTTP relay + Coalescer
│   ├── mitm/           # Local HTTP+SOCKS MITM proxy (desktop)
│   ├── tunnel/         # TCP-over-HTTP tunnel (Android)
│   ├── exit/           # VPS exit relay binary
│   └── deploy/
│       ├── apps-script/Code.gs
│       └── cloudflare/worker.js
│
├── android/            # Android Studio project
│   └── app/src/main/java/com/zyrln/relay/
│       ├── MainActivity.kt      # UI: connect/disconnect, config import, direct toggle
│       └── RelayVpnService.kt   # VpnService: StartTunnel/StartDirect, system HTTP proxy
│
├── docs/               # Setup guides
├── Makefile
└── go.mod
```

## Key Concepts

**`relay/core`** is the heart of the project. Both the desktop binary and the Android app import it.

- `relay.go`: builds and sends a relay request through domain-fronting. The domain-fronting trick is that `req.URL.Host` is set to the front domain (e.g. `www.google.com`) so TLS connects to Google's IPs, while `req.Host` carries the real Apps Script hostname inside the encrypted TLS tunnel.
- `proxy.go`: HTTP proxy that intercepts browser traffic. HTTP requests are relayed directly; HTTPS connections use `CONNECT` tunneling with local TLS termination (MITM).
- `cert.go`: generates a local CA and signs per-hostname leaf certificates on demand, cached in memory.
- `direct.go`: direct mode for Google services. Detects Google domains and dials them directly with fragmentation — no MITM, no relay.
- `fragment.go`: splits the first TLS ClientHello into 87 random-boundary chunks with 5ms delay each, preventing SNDPI from reading the SNI field.

**`platforms/mobile`** exposes a flat string-based API (`StartTunnel`, `StartDirect`, `Stop`, `Ping`, etc.) because gomobile only supports primitive types at the boundary. All errors are returned as strings, not Go `error` values.

## Running Tests

```bash
go test ./relay/... ./platforms/desktop/...
```

Or everything at once:

```bash
go test ./...
```

Tests use only the standard library, no external test frameworks.

## Building

```bash
make desktop          # build local ./zyrln CLI binary for this machine
make desktop-release  # build release binaries in dist/ for Linux, Windows, and macOS
make android          # build signed release APK (requires keystore)
```

Desktop release binaries use platform-specific names such as `zyrln-VERSION-linux-amd64`, `zyrln-VERSION-windows-amd64.exe`, `zyrln-VERSION-darwin-arm64`, and `zyrln-VERSION-darwin-amd64`.

First-time gomobile setup:

```bash
go install golang.org/x/mobile/cmd/gomobile@latest
gomobile init
export ANDROID_HOME=~/Android/Sdk
```

## Adding a New Probe

Probes are defined in `platforms/desktop/main.go` in the `defaultProbes()` function. Each probe is a `probe` struct:

```go
{
    ID:          "unique-id",
    Name:        "Human readable name",
    Category:    "baseline",   // used for --category filtering
    Method:      http.MethodGet,
    URL:         "https://example.com/",
    Expectation: "what a passing result means",
}
```

## Changing the Relay Protocol

The relay payload format is defined in `relay/appscript/relay.go` (`buildRelayPayload`) and must match what `relay/deploy/apps-script/Code.gs` expects. If you change either side, update both.

The Apps Script response format is `workerResponse` in `relay.go`:

```go
type workerResponse struct {
    Status  int               `json:"s"`
    Headers map[string]string `json:"h"`
    Body    string            `json:"b"` // base64-encoded
    Error   string            `json:"e"`
}
```

## Secrets and Gitignore

Never commit:
- `config.env`: contains your Apps Script URL and auth key
- `certs/`: contains the local CA private key
- Any file containing `AUTH_KEY` or relay keys

These are covered by `.gitignore`. See [Key Generation in the README](../README.md#prerequisites) for how to generate a key.
