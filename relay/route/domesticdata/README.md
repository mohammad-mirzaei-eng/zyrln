# Bundled domestic routing list

Shipped inside the app via `go:embed` (no runtime download).

| File | Source | Update |
|------|--------|--------|
| `domains.txt` | [iran-hosted-domains releases](https://github.com/bootmortis/iran-hosted-domains/releases) | Replace from latest `domains.txt` asset |

Matching uses `.ir` suffix plus this list (with subdomain walk, e.g. `www.digikala.com` → `digikala.com`).

After replacing the file, rebuild (`make android` / desktop). Pinned in tree as of 2026-05-18.
