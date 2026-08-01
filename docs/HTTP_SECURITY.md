# HTTP security and threat model

Last revalidated against current OWASP guidance: 2026-08-01.

This document covers Bridra's deployed Go HTTP surfaces: JSON RPC at `/rpc`,
streaming RPC, and managed file transfer under `/rpc/files/`. It does not turn
the development static token into production identity, terminate TLS, or replace
an edge rate limiter, firewall, identity provider, or incident-response system.

The model follows the maintained OWASP threat-modeling questions: what is being
built, what can go wrong, what will be done, and whether the controls were
validated. The risk inventory also checks the current
[OWASP API Security Top 10](https://owasp.org/API-Security/editions/2023/en/0x11-t10/),
[Logging Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html),
and [TLS Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Transport_Layer_Security_Cheat_Sheet.html).

## System and trust boundaries

```text
Untrusted Flutter/Web client
        |
        | HTTPS, authentication credential
        v
Trusted edge / TLS terminator  ---- invalid-credential and perimeter limits
        |
        | restricted upstream connection
        v
Go HTTP server
  HTTPObservationHandler ---- audit, metrics, tracing context
        |
        |-- /rpc ------------ Authenticator -> RateLimiter -> Router -> app
        `-- /rpc/files/ ----- short-lived capability or upload token -> Store
```

Assets to protect:

- Bearer credentials, upload tokens, file-transfer capabilities, and session
  state;
- Principal identity, permissions, application data, uploaded/downloaded files,
  and integrity hashes;
- backend CPU, memory, goroutines, bandwidth, file descriptors, storage, and
  dependent services;
- audit evidence, metrics, trace context, deployment configuration, and signing
  or TLS keys owned by the operator.

The network client, all headers, request bodies, RPC methods, file metadata, and
forwarding headers are untrusted. The direct backend connection is trusted only
when the operator prevents clients from bypassing the edge. Application
Controllers and Services remain responsible for object-level and business-rule
authorization after Bridra establishes a Principal and method permission.

## Threat and control inventory

| Threat | Framework control | Required production control / residual risk |
| --- | --- | --- |
| Stolen or compiled shared token | Strict Bearer parsing, constant-time static-token comparison, redacted auth failures | `NewStaticTokenAuthenticator` is development-only. Use expiring user/session credentials with rotation, revocation, and secure client storage. A token compiled into Web is public. |
| Broken method or object authorization | Exact `RequirePermission` method policies and Principal propagation | Every data lookup by an attacker-controlled ID still needs application-owned object authorization. Test tenant and ownership isolation. |
| Forged proxy identity | Default rate-limit keys use the direct socket IP and ignore `Forwarded` and `X-Forwarded-For` | Restrict direct backend access. Parse forwarded headers only in an explicit function that validates the complete trusted-proxy chain. |
| Invalid-credential brute force | Authentication happens before body parsing; failures are stable and redacted | Principal rate limiting occurs after authentication. Limit invalid credentials by direct IP/account at the trusted edge or identity provider. |
| Valid-user resource exhaustion | Bounded request bodies, process-local token bucket, stream backpressure, server header/read/write/idle limits | Use a shared limiter for multiple instances, plus edge connection/body/bandwidth limits and application-specific budgets for expensive methods. |
| Identity-key memory exhaustion | Memory limiter has a fixed key capacity and fails closed for new identities | Size the capacity from real traffic and alert on sustained 429 responses. A distributed deployment needs a bounded shared store. |
| Oversized or slow headers/body | Explicit `MaxHeaderBytes`, `ReadHeaderTimeout`, body limits, and server read/write timeouts | Long file transfers need deployment-specific limits. The edge must enforce total request, connection, upload, and concurrency budgets. |
| CORS misconfiguration | Empty CORS configuration denies browser origins; generated direct-server default is fail-closed | Configure one exact production origin. The `*` default used by local `bridra dev` and repository Make targets is development-only. CORS does not protect native clients. |
| File capability disclosure | Random short-lived capability IDs, `no-store`, `nosniff`, single-consumer semantics, and audit events that never record URL paths | Capability possession authorizes the transfer. Use HTTPS, short TTLs, safe referrer policy at the application edge, and never place capabilities in analytics, proxy logs, or support tickets. |
| Secret or PII leakage through logs | JSON events omit headers, URLs, query strings, bodies, params, tokens, and capability IDs; values are bounded and Principal subjects are SHA-256 pseudonyms | Direct client IP remains security data. Apply an approved retention period, access control, encryption, deletion, and jurisdiction-specific privacy review. |
| Log injection or log-volume exhaustion | Structured JSON encoding and bounded attacker-controlled fields | Export asynchronously with backpressure, quotas, rotation, tamper protection, and an alert when collection stops. Do not let a remote sink block request goroutines. |
| Request-correlation spoofing | Bridra generates a fresh 128-bit `X-Request-ID`; it does not accept a client value | Preserve the ID across trusted internal calls. Do not treat it as authentication or authorization evidence. |
| Observer failure | Observer panics are contained and logged; the application response continues | A blocking custom observer can still retain a request goroutine. Keep hooks bounded and move network export behind an application-owned queue. |
| Missing TLS or edge bypass | Release Flutter builds require HTTPS endpoints; sensitive RPC responses use `no-store` | Terminate modern TLS at a trusted edge, restrict the backend listener, secure the edge-to-backend hop, and configure HSTS at the public HTTPS boundary where appropriate. |
| Replay of a valid business action | Request IDs provide correlation only | Use application-owned idempotency keys, nonce/version checks, and transaction rules for non-repeatable or financial operations. |
| Vulnerable downstream call or SSRF | Bridra's HTTP adapter does not fetch user URLs | Controllers/Services that call external systems must validate destinations, use egress controls, bound responses, and treat third-party data as untrusted. |
| Metrics cardinality or endpoint leakage | Built-in `HTTPMetrics` uses fixed counters without Principal, IP, path, or RPC labels | Authenticate any metrics endpoint and add only reviewed bounded labels in an external exporter. |

## Audit and observability contract

Wrap the complete HTTP mux with `HTTPObservationHandler`. It generates a server
request ID, returns it as `X-Request-ID`, and places it in the request Context for
application logs. Its observer lifecycle supports three uses without coupling the
framework to one vendor:

- `NewJSONHTTPObserver` writes one structured completion event for audit and
  incident correlation;
- `HTTPMetrics` records fixed active/total/outcome/security counters and duration
  totals/maxima for an application-owned exporter;
- a custom `HTTPObserver` can start a tracing span in `BeginHTTP`, return the
  enriched Context, and finish it in `EndHTTP`.

The built-in JSON event contains only:

```text
request_id, http_method, direct client_ip, surface, rpc_method,
principal_sha256, status, error_code, outcome, response_bytes,
duration_micros
```

It intentionally excludes the URL path because file-transfer paths contain
bearer capabilities. It also excludes headers, query strings, request/response
bodies, RPC params, raw Principal subjects, Bearer credentials, and upload
tokens. Custom observers must preserve that rule unless an explicit data
classification and retention decision permits more.

Recommended alerts:

- sudden or sustained increases in `unauthenticated`, `forbidden`, `rate_limited`,
  server-error, or canceled outcomes;
- latency or active-request growth without matching traffic growth;
- audit collection stopping, queue overflow, exporter failure, or log deletion;
- repeated expensive RPC methods, upload errors, capability misses, and edge
  invalid-credential throttling.

## Production checklist

- [ ] Public API accepts HTTPS only; backend and metrics listeners cannot bypass
  the trusted edge.
- [ ] Static/shared development token is replaced by expiring, rotating, and
  revocable application identity.
- [ ] Every privileged method has an exact permission policy; every object lookup
  has tenant/owner authorization tests.
- [ ] CORS names one exact production origin; wildcard remains local-only.
- [ ] Invalid-credential, authenticated-user, connection, bandwidth, upload, and
  expensive-business-flow limits exist at the correct layers.
- [ ] Multi-instance rate limiting uses a bounded shared store with failure and
  recovery alerts.
- [ ] Server and edge header/body/time/concurrency limits match the largest valid
  RPC, stream, and file transfer.
- [ ] Audit events reach protected storage with rotation, retention, access,
  integrity, deletion, and collection-health controls.
- [ ] 401, 403/RPC `forbidden`, 429, 5xx, latency, active requests, cancellations,
  and exporter failures have dashboards and actionable alerts.
- [ ] TLS, credential rotation/revocation, proxy-chain validation, backup/restore,
  incident response, and dependency patching have been exercised, not only
  documented.

## Validation evidence

Framework tests verify direct-IP use despite spoofed forwarding headers, request
ID propagation, Principal/RPC/error observation, secret and capability-path
exclusion from JSON logs, observer-panic containment, streaming flush behavior,
concurrent metrics, authentication/rate-limit redaction, body limits, CORS, and
race safety. Deployment-specific controls above require the application owner to
retain separate configuration, load-test, penetration-test, and incident-drill
evidence.
