# TODO

## High Priority

- **Android Manual Config (#6):** Add a "Manual Entry" dialog to the Android app. Users should be able to type in their Apps Script URL and Auth Key directly without relying on JSON/Clipboard.
- **Custom Proxy IP (#7):** Allow specifying a fixed IP for the domain-fronted connection to bypass specific IP-based blocks on Google's edge.
- **Google-fronted Direct Fragmenter:** Integrate the successful standalone probe from `tools/utls-frag-probe`. Lab result: `CONNECT www.google.com:443` or `CONNECT script.google.com:443` with inner TLS SNI `www.youtube.com`, Chrome-like ClientHello, and `87` equal first-write fragments at `5ms` completed TLS where direct YouTube failed. App integration idea: when the browser asks the local proxy for a YouTube/Google direct domain, dial an allowed Google front host but pipe the original client TLS stream unchanged so the visible CONNECT target looks like Google while the fragmented inner SNI remains the requested target.

## Performance & Stealth

- **HTTP/3 (QUIC) Support:** Implement QUIC for the client-to-Google connection. This will reduce handshake latency and improve stability under packet loss (0-RTT support).
- **Proactive URL Rotation (#7):** Add an optional mode to rotate to the next Apps Script URL every ~500 requests to distribute load across multiple Google accounts.
- **Quota Tracking (#7):** Add a simple UI counter to estimate daily request usage against the 20k Google quota.

## Cloud Run Transport (Pinned Apps — Instagram, Twitter, etc.)

- **Cloud Run relay backend (~100 lines Go + Dockerfile):** Deploy a transparent TCP CONNECT proxy on Cloud Run (`*.run.app` is reachable from Iran). No MITM, TLS is end-to-end so certificate pinning is bypassed. Fits existing multi-URL failover — just add the Cloud Run URL alongside Apps Script URLs.
- **Android TUN mode:** Switch Android from HTTP proxy + MITM to TUN mode (raw IP packet interception → CONNECT tunnel through Cloud Run). Builds on the existing `feat/tun-socks5-doh` branch work. Enables Instagram, Twitter and other pinned apps to work.
- **DNS:** Cloud Run backend handles DoH so the DNS-over-UDP issue that killed the previous TUN attempt is resolved.

## Maintenance & Features

- **Safer Config Mode:** Add a way to pass the auth key via a secure file or environment variable to avoid it appearing in shell history.
- **Download Path (#2):** Allow Android users to customize the storage location for downloaded assets.
- **Chain Proxy (#7):** Explore supporting an upstream proxy (SSH/VLESS) at the exit relay level.
