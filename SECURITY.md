# Security Policy

## Supported Versions

Security fixes are released for the latest minor release line only. Please upgrade to the newest release before reporting.

| Version | Supported |
| ------- | --------- |
| 1.27.x  | Yes       |
| < 1.27  | No        |

## Reporting a Vulnerability

Please report vulnerabilities privately, using either:

- **GitHub private vulnerability reporting (preferred):** <https://github.com/MikkoParkkola/trvl/security/advisories/new>
- **Email:** mikko.parkkola@iki.fi

Do NOT open a public issue, discussion, or pull request for security vulnerabilities.

Please include affected version(s), reproduction steps, and the potential impact. trvl is maintained by a single maintainer, so responses are best effort: reports are acknowledged and triaged as soon as practical, and fixes are coordinated with the reporter before public disclosure.

## Scope

trvl accesses Google's public-facing internal APIs using Chrome TLS fingerprint impersonation. It does not:
- Store or transmit user credentials
- Access authenticated Google accounts
- Bypass rate limits or access controls
- Cache personal data

## Dependencies

The security-load-bearing dependencies (those that touch the network, the browser, or the user's machine) are:

- `github.com/refraction-networking/utls`, `github.com/bogdanfinn/{utls,fhttp,tls-client}`, `github.com/cloudflare/circl` — TLS/HTTP fingerprint impersonation
- `github.com/chromedp/chromedp`, `github.com/chromedp/cdproto` — headless-Chrome automation for JS-gated providers
- `github.com/browserutils/kooky` — reads browser cookie databases off the local disk (e.g. Booking.com auth); cookies are used locally and never transmitted to trvl
- `github.com/grafana/sobek` — embedded JS runtime for provider script evaluation
- `github.com/spf13/cobra` — CLI framework
- `golang.org/x/{time,net}` — rate limiting, HTTP/2 + proxy

This list is illustrative, not exhaustive (21 direct dependencies total). All dependencies are version-pinned in go.mod / go.sum and scanned by `govulncheck` and OSV in CI.
