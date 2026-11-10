# Security design

garret exists to keep undisclosed fixes private. This document states what it
protects, against whom, and how.

## Threat model

**Assets**: repository contents, review comments and discussions,
attachments (logs, proofs of concept), account credentials.

**Trusted**: invited members and the person running the host machine.

**Threats in scope**

| Threat | Example |
|---|---|
| Access from outside the local network | A port forward, a misconfigured router, a host with a public address |
| An uninvited device on the same network | Someone else on a shared office or conference Wi-Fi |
| Passive eavesdropping on the network | A person who knows the Wi-Fi password capturing traffic |
| A member's stolen token or session | A token pasted into a log, a laptop left unlocked |
| Malicious content from a member | Script injection in comments, crafted file names, oversized uploads |
| Data left behind after release | The fix is public, but the review server still holds the private history |

**Out of scope**: a compromised host machine, a malicious administrator, and
denial of service by a member.

## Network boundary

The network filter runs on every accepted connection, before TLS, routing or
authentication.

### Determining the allowed network

1. If `--allow` is given (repeatable CIDR values), use exactly those.
2. Otherwise, list interfaces that are up, not loopback, and have a private
   IPv4 address (RFC 1918). If there is exactly one, allow its subnet.
3. If there are none or more than one, stop with a message that lists the
   candidates and asks for `--allow`.
4. Every allowed range must lie inside private space: RFC 1918 for IPv4,
   `fc00::/7` or `fe80::/10` for IPv6. Anything else stops the server.
5. Loopback is always allowed, so the host can use its own browser.

By default the server listens on the chosen interface address rather than on
all interfaces.

### Checking a connection

The filter uses the TCP peer address only. `X-Forwarded-For`, `Forwarded` and
similar headers are ignored. A rejected connection is closed without a
response body and logged with its address.

## Transport security

- On first start garret creates an ECDSA P-256 key and a self-signed
  certificate covering the listen address and the host name, valid for
  7 days. Key files are written with mode `0600`. How the certificate is
  renewed before it expires, and how members keep trusting it across
  renewals, is an open decision.
- The SHA-256 fingerprint is printed at every start and shown in the web UI.
  Members compare it once, then trust the certificate in the browser and pass
  it to Git with `http.sslCAInfo`.
- `--tls-cert` and `--tls-key` load an existing certificate instead.
- Plain HTTP is available only with `--insecure-http`, which prints a warning
  at every start.
- TLS 1.2 is the minimum version. Responses carry
  `Strict-Transport-Security` only when a user-supplied certificate is used,
  so a regenerated self-signed certificate cannot lock members out.

## Authentication

| Credential | Storage | Lifetime (proposed default) |
|---|---|---|
| Password | Argon2id hash with per-user salt | Until changed |
| Invitation token | SHA-256 hash, single use | 24 hours |
| Session cookie | SHA-256 hash; cookie is `HttpOnly`, `Secure`, `SameSite=Strict` | 7 days idle, 30 days maximum |
| Personal access token | SHA-256 hash, shown once at creation | Until revoked |
| First-administrator setup token | Printed to the terminal, single use | Until used or the server restarts |

- Tokens and session identifiers are 256 bits from `crypto/rand`.
- Credentials are compared in constant time.
- Login and token checks are rate-limited per source address and per username.
- Disabling a user revokes their sessions and tokens immediately.
- Secrets never appear in logs, error messages or audit events.

## Authorization

The first release has two roles. Members read and write every project:
fetch, push, review, comment, discuss and upload. Administrators can also
create projects, manage invitations and users, permanently erase content, and
export or purge projects. Authors may edit and delete their own comments;
administrators may delete any comment.

Every API handler checks authorization in the service layer, not only in the
UI.

## Web application hardening

- Comment and discussion text is Markdown, rendered on the client and passed
  through an allow-list sanitizer. Raw HTML is not allowed.
- `Content-Security-Policy` restricts scripts, styles and connections to the
  server's own origin.
- `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer` and
  `X-Frame-Options: DENY` on every response.
- State-changing requests require the CSRF token header and a matching
  `Origin`.
- Attachments are served as downloads with a fixed safe media type for
  anything not on a short list of image types. Original file names are kept
  only as metadata, never as paths.

## Git handling

- Project names are validated against `[a-z0-9][a-z0-9._-]{0,63}` before any
  path is built, and the resolved path must stay inside `data/repos`.
- `git` runs without a shell, with an explicit minimal environment, and every
  invocation has a context timeout.
- Pushes to `refs/garret/*` are rejected. Hidden refs are not advertised.
- Repository size and push size limits are **proposed defaults** to be
  measured during implementation.

## Data at rest

- The data directory is created with mode `0700`; files inside with `0600`.
  garret refuses to start if the directory is readable by other users.
- garret does not encrypt stored data in the first release. At startup it
  warns when disk encryption appears to be off (FileVault on macOS, a
  dm-crypt mapping on Linux). Detection is best effort and never blocks
  startup.
- SQLite runs with `secure_delete` on, so erased rows are overwritten.

## After the fix is released

Administrators export a project to one archive and then purge it. Purge
removes database rows, the repository and attachments, then compacts the
database. See [data-model.md](data-model.md#retention) for the exact effect.
The archive itself is sensitive; where it is kept is the operator's
responsibility.

## Dependencies

New third-party dependencies are pinned, reviewed for maintenance status and
license, and checked with `govulncheck` and `npm audit` in CI.
