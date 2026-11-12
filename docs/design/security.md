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
| An uninvited device on the same network | Someone else on a shared office Wi-Fi, including colleagues who are not part of the fix |
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

## Transport security and device certificates

garret runs a small private certificate authority. It authenticates the
server to members, and members' devices to the server (mutual TLS). A device
on the same access point without a valid client certificate cannot complete a
TLS handshake, so it learns nothing beyond the fact that a port is open.

### Certificate authority

- On first start garret creates an ECDSA P-256 CA key and a CA certificate.
  The CA carries X.509 name constraints that limit it to the server's
  addresses and host name, so a leaked CA key cannot impersonate other sites.
- The CA key never leaves the data directory and is written with mode `0600`.
  Project purge does not remove it; resetting the server does.
- The CA's SHA-256 fingerprint is printed at every start and embedded in every
  invitation link, so new members verify the CA without comparing it by eye.
  The fingerprint is not secret; the link carries no secret at all. The
  invitation code travels through a different channel and is typed at a
  hidden prompt, so it never sits in chat history, browser history, shell
  history or the process list (see [specs/transport.md](../specs/transport.md)).

### Server certificate

- The server certificate is issued by the CA, valid for 7 days, and covers
  the listen addresses and the host name.
- garret issues a new one before the current one expires and swaps it in
  without a restart (`tls.Config.GetCertificate`). Members trust the CA, so
  rotation needs no action from them.

### Client certificates

Client certificates are required by default. Each certificate identifies one
device of one user and is valid for 7 days.

| Case | How a device gets a certificate |
|---|---|
| New member | `garret enroll <invitation link>` generates a key on the device, verifies the CA fingerprint from the link, asks for the invitation code at a hidden prompt, sends a certificate signing request with the code over the enrollment listener, and receives a certificate. The private key never leaves the device |
| Existing member | `garret renew` connects with the current, still valid certificate and requests a new one for the same key or a new key. A device without a valid certificate cannot renew |
| Expired certificate | The device must be enrolled again with a new invitation |
| First administrator | `garret admin setup` runs on the host, reads the data directory directly, and issues the administrator's certificate locally |

- `garret enroll` installs the certificate into the macOS login keychain (or
  a file on Linux), configures Git (`http.sslCert`, `http.sslKey`,
  `http.sslCAInfo`) for the server's URL, and registers a background renewal
  job (launchd on macOS, a systemd user timer on Linux). The job renews when
  less than half of the validity remains.
- Disabling a user revokes all of the user's client certificates at once. The
  server checks every handshake against its own revocation list in the
  database.
- The client certificate is bound to the user account. After the handshake
  the user still logs in (browser) or presents a personal access token (Git).
  A request whose session or token belongs to a different user than the
  certificate is rejected.

### Enrollment listener

A device that is not enrolled has no client certificate, so enrollment uses a
separate listener. It accepts TLS without client certificates and serves only
the enrollment endpoint. It is open only while at least one unused,
unexpired invitation exists, and closes when the last one is used, revoked or
expired.

### Turning client certificates off

`--client-cert=off` disables mutual TLS for teams on an access point they
fully control. Login and tokens still apply. garret prints a warning at every
start while client certificates are off.

### Other transport settings

- `--tls-cert` and `--tls-key` load an externally issued server certificate.
  Client certificates are still issued by garret's CA.
- Plain HTTP is available only with `--insecure-http`. It implies
  `--client-cert=off` and prints a warning at every start.
- TLS 1.3 is the minimum version. Every client the project supports (current
  Git, Chrome and Safari) speaks it.
- Supported browsers are the latest Chrome and Safari, which read client
  certificates from the system keychain. Firefox keeps its own certificate
  store, which the renewal job cannot update, so it is not supported while
  client certificates are on.

## Authentication

| Credential | Storage | Lifetime (proposed default) |
|---|---|---|
| Password | Argon2id hash with per-user salt | Until changed |
| Invitation code | SHA-256 hash, single use; six random words, given to the member separately from the link | 30 minutes |
| Session cookie | SHA-256 hash; cookie is `HttpOnly`, `Secure`, `SameSite=Strict` | 7 days idle, 30 days maximum |
| Personal access token | SHA-256 hash, shown once at creation | Until revoked |
| First-administrator setup token | Printed to the terminal, single use | Until used or the server restarts |

- Tokens and session identifiers are 256 bits from `crypto/rand`.
- Credentials are compared in constant time.
- Login and token checks are rate-limited per source address and per username.
- Disabling a user revokes their sessions, tokens and client certificates
  immediately.
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

Supply-chain risk is judged by everything compiled into the binary, not by
the popularity of the top-level package.

- **Adding a dependency.** The pull request states who maintains it (an
  organization or a single person), its latest release, its license, and how
  many modules it adds to `go list -deps`. Unmaintained modules (no release in
  over a year) and single-maintainer forks in the runtime path are avoided.
- **Pinning.** `go.mod` and `go.sum` pin every module and the Go checksum
  database verifies them. Build-time tools such as sqlc are pinned with the
  `tool` directive in `go.mod`. The frontend uses a lockfile and `npm ci`.
- **Cooldown.** A new release is adopted only after it has been public for at
  least 7 days, unless it fixes a vulnerability that affects garret.
- **Monitoring.** CI runs `govulncheck` and `npm audit`. Dependabot proposes
  security updates.
- **Generated code.** Code generated by sqlc is committed, so a compromised
  generator shows up as a reviewable diff.
