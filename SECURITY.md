# Security policy

## Supported versions

Only the `main` branch and the most recently tagged release receive
security fixes. There is no LTS line.

| Version    | Supported          |
| ---------- | ------------------ |
| `main`     | :white_check_mark: |
| latest tag | :white_check_mark: |
| older tags | :x:                |

## Reporting a vulnerability

Please report security issues **privately** via GitHub's "Report a
vulnerability" workflow on the repository's
[Security tab](https://github.com/vancanhuit/url-shortener/security)
(GitHub private vulnerability reporting). Do **not** open a public
issue or pull request that describes the vulnerability.

Include, where possible:

- a clear description of the issue and the affected code path,
- reproduction steps or a minimal proof of concept,
- the commit SHA or release tag you tested against,
- your assessment of the impact (confidentiality / integrity /
  availability), and
- any suggested remediation.

You should receive an acknowledgement within a few business days.
Coordinated disclosure timelines will be agreed on a per-report basis;
the default working assumption is a fix-and-release window of 30–90
days for non-trivial issues.

## Scope

The following are in scope for vulnerability reports:

- the Go service in `cmd/url-shortener` and `internal/`,
- the SQL migrations in `migrations/`,
- the embedded Svelte SPA in `web/`,
- the Docker image build (`Dockerfile`, `compose.yaml`),
- the CI / release automation in `.github/`.

The following are explicitly **out of scope**:

- vulnerabilities in third-party services the binary connects to
  (Postgres, Redis, your reverse proxy) — report those upstream;
- denial-of-service via unbounded resource use by an authenticated
  operator (e.g. setting `URL_SHORTENER_RATE_LIMIT_RPS=999999`);
- physical access, social engineering, and supply-chain attacks
  against dependencies (please report supply-chain CVEs to the
  upstream maintainer first);
- security weaknesses that require code modification of the
  deployment (e.g. "if you set `Skip*` to true, the check is
  bypassed") — that is by design.

## Threat model summary

`url-shortener` is a public-facing HTTP service that accepts redirect
targets from anonymous callers and rewrites them to short codes. The
primary security goals are:

1. **Server-side request forgery (SSRF) containment.** The redirect
   handler must not be weaponisable as a proxy to internal
   infrastructure (cloud metadata services, Redis admin ports,
   internal microservices). The validator rejects RFC 1918 / loopback
   / link-local / ULA / CGNAT IP literals, bare `localhost`, and
   IPv6 link-local literals with scope IDs at create-link time. See
   the SSRF protection section in the README for the exact list.
2. **Input-bound resource caps.** Request bodies are capped
   (`URL_SHORTENER_MAX_REQUEST_BODY_BYTES`, default 16 KiB), the
   per-IP create-link path can be rate-limited
   (`URL_SHORTENER_RATE_LIMIT_RPS` + `_BURST`), and HTTP read /
   write / idle timeouts are conservative by default. Long bodies,
   slowloris-style clients, and request floods should fail closed.
3. **Persistence safety.** All SQL is parameterised via sqlc-generated
   code; no string concatenation reaches the driver. Migrations are
   wrapped in an advisory lock to prevent concurrent runners from
   interleaving DDL.
4. **Operational secret hygiene.** `Config.Redacted()` substitutes
   passwords in the URLs printed by `url-shortener config`; CI does
   not echo secrets; the binary does not log raw connection strings.

The following are **explicitly assumed handled upstream** (not by this
service):

- **Authentication / authorisation.** The HTTP API is open by design.
  A reverse proxy or API gateway is expected to provide auth where
  the deployment requires it.
- **TLS termination.** Built-in TLS exists and is exercised by
  integration tests, but production deployments typically terminate
  TLS at a fronting proxy (Caddy / nginx / Traefik / a load balancer).
- **Egress network policy.** SSRF containment is best-effort in-band.
  Pair it with an egress allow-list at the pod / VM level for
  defence in depth.

## Known limitations

- DNS names other than `localhost` are not resolved at validation
  time. Adding per-request DNS would both slow the create-link path
  and open the door to DNS rebinding. Operators that need
  hostname-based SSRF protection must rely on egress network policy.
- The in-memory rate limiter is per-instance. Horizontal scale-out
  multiplies the effective per-IP budget by the replica count;
  enforce a global budget at the fronting proxy / WAF for clusters
  larger than a single replica.
- Click-counter updates fire asynchronously after the redirect; a
  hard process kill (SIGKILL) can drop in-flight increments.
  Counts are best-effort, not audit-grade.
