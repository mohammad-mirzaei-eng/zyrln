# Relay layout

Go code for the Zyrln relay stack. **Desktop and Android import `zyrln/relay/core`** — that package re-exports the API; implementation lives in the packages below.

```
relay/
├── core/          # Stable API (re-exports route, appscript, mitm, conn, log, netdial)
├── route/         # Routing policy: Google direct, domestic bypass, TLS fragmentation
├── appscript/     # Domain-fronted HTTP relay + Coalescer (Apps Script / Worker chain)
├── mitm/          # Local HTTP CONNECT proxy, SOCKS5, MITM TLS, CA certs
├── tunnel/        # Raw TCP-over-HTTP tunnel (Android VPN path)
├── exit/          # Self-hosted VPS exit relay binary
├── deploy/        # Non-Go deployment assets
│   ├── apps-script/   # Code.gs template
│   └── cloudflare/    # Worker.js template
├── conn/          # BufferedConn, SOCKS helpers
├── log/           # Optional log + OnRequest hooks
└── netdial/       # Protected TCP dialer (VpnService.protect on Android)
```

| Package | Used by | Role |
|---------|---------|------|
| `route` | `tunnel`, `mitm` | Where traffic goes before relay/tunnel |
| `appscript` | `mitm`, `tunnel` | Outbound relay HTTP to Google Apps Script |
| `mitm` | Desktop only | Browser proxy + MITM |
| `tunnel` | Android only | CONNECT proxy over Apps Script tunnel |

When changing relay JSON or `Code.gs`, update **`deploy/apps-script/Code.gs`** and **`appscript`** together.

## What happens per stack (by host)

**Domestic** = `.ir` or domain on the bundled list → always **plain protected TCP** (no Apps Script, no MITM).  
**Direct** = `directEnabled` → Google uses **TLS fragmentation** instead of relay/tunnel.  
**Relay host** = everything else (YouTube, Twitter, Google when direct is off, etc.).

Domestic is not a toggle; it is always evaluated first (after Google+direct).

---

### Android

VPN uses `tunnel` (`StartTunnel`) or **direct-only** (`StartDirect`, no Apps Script). No SOCKS. No MITM.

```
Android
│
├─ tunnel + domestic + direct ON          (StartTunnel, user direct toggle ON)
│     digikala.com      → domestic plain pipe
│     www.google.com    → TLS fragment pipe
│     youtube.com       → Apps Script TCP tunnel (OpenSession → bridge)
│
├─ tunnel + domestic + direct OFF         (StartTunnel, user direct toggle OFF)
│     digikala.com      → domestic plain pipe
│     www.google.com    → Apps Script TCP tunnel  (Google NOT fragmented)
│     youtube.com       → Apps Script TCP tunnel
│
└─ direct only (no tunnel)                (StartDirect — forces direct ON, no relay URLs)
      Google services   → TLS fragment pipe
      everything else   → plain protected pipe  (digikala, youtube, … same path)
```

Plain HTTP to the local proxy in tunnel mode → **502** (HTTPS CONNECT only).

---

### Desktop

Uses `mitm` (HTTP CONNECT + optional SOCKS5). Foreign HTTPS on the relay path needs **CA installed** (MITM). SOCKS TLS follows the same host rules as CONNECT.

```
Desktop
│
├─ relay + domestic + direct ON           (Apps Script URLs + CA + directEnabled ON)
│     CONNECT / SOCKS TLS:
│       digikala.com      → domestic plain pipe
│       www.google.com    → TLS fragment pipe
│       youtube.com       → MITM fake cert → decrypt → coal.Submit (Apps Script relay)
│     plain HTTP to proxy:
│       digikala.com      → directHTTP (protected)
│       youtube.com       → coal.Submit
│
├─ relay + domestic + direct OFF          (URLs + CA + directEnabled OFF)
│     CONNECT / SOCKS TLS:
│       digikala.com      → domestic plain pipe
│       www.google.com    → MITM → coal.Submit   (Google through relay)
│       youtube.com       → MITM → coal.Submit
│     plain HTTP:
│       digikala.com      → directHTTP
│       google / foreign  → coal.Submit
│
├─ direct only (no relay URLs)            (StartDirectProxy / GUI direct-only)
│     CONNECT / SOCKS TLS:
│       digikala.com      → domestic plain pipe
│       www.google.com    → TLS fragment pipe   (direct forced ON)
│       youtube.com       → plain protected TCP pipe
│     plain HTTP:
│       all hosts         → directHTTP
│     SOCKS: no MITM, no coal
│
├─ nothing (no URLs, direct OFF)          (disconnected)
│     everything          → 502
│
└─ SOCKS note (any desktop mode above)
      TLS (HTTPS)         → same tree as CONNECT (fragment / domestic / MITM)
      cleartext HTTP      → direct-only: raw pipe; relay modes: domestic → directHTTP, else coal.Submit
```

---

### One-page mega tree (all stacks)

```
                              HOST (CONNECT / SOCKS SNI)
                                        │
                 ┌──────────────────────┼──────────────────────┐
                 │                      │                      │
          Google + direct ON?      domestic match?         relay host
                 │                      │                      │
                 ▼                      ▼                      │
            FRAGMENT               DOMESTIC                   │
            pipe                   plain pipe                  │
                 │                      │                      │
                 └────────── bypass ────┘                      │
                                         │                      ▼
                                         │         ┌────────────┴────────────┐
                                         │         │                         │
                                         │    ANDROID (tunnel)          DESKTOP (mitm)
                                         │         │                         │
                                         │    tunnel client?            coal + ca?
                                         │         │                         │
                                         │    OpenSession → bridge    MITM → coal.Submit
                                         │    (Google here only       (or 502 if no CA)
                                         │     if direct OFF)
                                         │
                    Android direct-only: relay host → plain pipe (no tunnel)
                    Desktop direct-only: relay host → plain pipe (no MITM)
```

**Only difference `direct` makes:** Google — **fragment** vs **relay/tunnel/MITM**. Domestic and foreign rules stay the same.
