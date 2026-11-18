# garret

[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![Status](https://img.shields.io/badge/status-pre--release-orange.svg)](#project-status)
[![Contributions welcome](https://img.shields.io/badge/contributions-welcome-brightgreen.svg)](CONTRIBUTING.md)

**A self-hosted, LAN-only code review server for small teams sharing the same access point.**

garret lets a few people connected to the same Wi-Fi access point or router
review Git branches together, without pushing anything to a public host. Run
one binary on one machine, push branches to it, and review, discuss and track
changes from any browser on the same subnet.

The name is a nod to [Gerrit](https://www.gerritcodereview.com/), the code
review tool that inspired it. A *garret* is a small room tucked under the roof:
a private space inside the house.

---

## Table of contents

- [Why garret](#why-garret)
- [How it works](#how-it-works)
- [Features](#features)
- [Project status](#project-status)
- [Getting started](#getting-started)
- [Security model](#security-model)
- [Roadmap](#roadmap)
- [Contributing](#contributing)
- [Security policy](#security-policy)
- [License](#license)

## Why garret

When a vulnerability is found in a public open-source project, the fix cannot
be developed in the open. A public branch, pull request or review comment tells
everyone what is broken before a patched release exists.

The usual answers are a private fork, a private mirror, or a separate internal
Git host. All of them take time to set up, and time is what an urgent fix does
not have.

garret takes a smaller approach. If the people working on the fix are in the
same room, on the same access point, they only need a review server that is
reachable from that local network and nowhere else:

- **No public exposure.** The server accepts connections only from the local
  subnet. Nothing is pushed to a public host until the team decides to release.
- **Nothing to provision.** A single binary with an embedded database. No
  external services, accounts or cloud setup.
- **A full review trail.** Every revision, comment and decision is recorded,
  so the team can trace how the fix evolved.

garret is not meant to replace your main code review platform. It is a
short-lived, private room for work that is not ready to be seen.

## How it works

```
   same access point / subnet (e.g. 192.168.0.0/24)
 +-----------------------------------------------------------+
 |                                                           |
 |   developer A ---- git push ---->  +------------------+   |
 |                                    |  garret server   |   |
 |   developer B ---- browser ----->  |  - Git over HTTP |   |
 |                                    |  - review web UI |   |
 |   developer C ---- browser ----->  |  - database      |   |
 |                                    +------------------+   |
 +-----------------------------------------------------------+
            requests from outside the subnet are rejected
```

1. One team member runs `garret` on a machine connected to the shared access
   point.
2. Team members add the server as a Git remote and push their fix branches.
3. Reviewers open the web UI from the same network, compare branches, comment
   on lines and discuss the change.
4. New pushes to the same branch become new revisions, so earlier comments and
   the history of the fix stay attached to the change.

## Features

- Git repository hosting over HTTPS, so members can `git push` and `git fetch`
- Projects that start from a Git repository on the server's machine, with a
  "Sync from source" button to bring in new commits
- Branch-based review, read as a whole, one commit at a time, a run of
  commits, or the changes since an earlier revision
- Revision tracking: each push becomes a new, comparable revision, kept even
  after force pushes
- Inline comments with threads, carried over to new revisions
- Review states and verdicts recorded with who decided and when
- "Download patch" for the view on screen, to apply with `git am` on your
  own clone
- Discussion posts and review descriptions edited by several people at once
- Live updates, an in-app inbox with mentions, and file attachments
- Two ways to join: device certificates (mutual TLS) with invitations, or a
  browser and a password with accounts the administrator creates
- Personal access tokens for Git
- Access restricted to the local subnet, with automatically rotated 7-day
  certificates
- Administration: disable and remove users (their records stay), erase a
  single comment, attachment or collaborative text, export a project and
  purge it when the work is public
- Edit history for comments and collaborative documents
- English and Korean, following the browser or chosen under Account
- A web frontend and an embedded database in the same binary

See [docs/design](docs/design/README.md) for the full design.

## Project status

The first pre-release, `v0.1.0-rc.1`, is on the
[Releases](https://github.com/0xmhha/garret/releases) page, and `main` has
moved on since. APIs, data formats and configuration may still change before
`v0.1.0`; schema changes come with migrations that run at startup. Follow the
[Roadmap](#roadmap) and open issues to see what is being worked on.

## Getting started

Download the archive for your platform from
[Releases](https://github.com/0xmhha/garret/releases), check it, and put
`garret` on your `PATH`:

```sh
shasum -a 256 -c SHA256SUMS --ignore-missing
gh attestation verify garret_<version>_darwin_arm64.tar.gz --repo 0xmhha/garret
tar -xzf garret_<version>_darwin_arm64.tar.gz
```

The attestation shows that this repository's CI built the archive from the
tagged commit ([release.md](docs/specs/release.md)). The macOS binaries are
not notarized: download with `curl`, or allow the first run under System
Settings › Privacy & Security.

To build from source instead: `make release` (needs Go 1.26.11 or newer and
Node 24), which writes `bin/garret`. Then:

```sh
# On the host machine. The first start asks how members will join
# (see below) and prints the allowed network and the CA fingerprint.
garret serve
garret admin setup        # in a second terminal: the first administrator
```

Members join in one of two ways, chosen when the server first starts:

1. **Each member runs `garret enroll` once** (recommended). The device gets a
   client certificate, and only enrolled devices can even reach the sign-in
   page:

   ```sh
   garret enroll '<invitation link>'
   ```

2. **A browser and a password only.** Client certificates are off, so no
   program runs on members' machines. The administrator creates each account
   under Administration and passes on its temporary password; the member
   opens `https://<host>:8443`, signs in and changes the password under
   Account. Anyone on the allowed network can reach the sign-in page.
   The host name and the host's IP address on the network both work, so
   use the address if the `.local` name does not resolve on a member's
   machine.

   Browsers warn about the certificate until the member trusts garret's
   certificate authority once. The administrator passes on
   `tls/ca.crt` from the data directory (on macOS
   `~/Library/Application Support/garret`) together with the CA fingerprint
   that `garret serve` prints. The file is a public certificate, not a
   secret. On the member's Mac:

   ```sh
   # Compare with the fingerprint from the administrator (ignore the colons)
   openssl x509 -in ca.crt -noout -fingerprint -sha256
   security add-trusted-cert -r trustRoot -k ~/Library/Keychains/login.keychain-db ca.crt
   ```

   Then restart the browser. The certificate authority can only vouch for
   the server's own names and network, so trusting it does not let garret
   speak for other sites.

`garret serve --client-cert=on|off` overrides the choice for one run.

To remove someone who left, disable them under Administration and then
remove them. Their username becomes free again, while their reviews and
comments keep their name.

```sh
# Then use Git as usual, with a personal access token as the password
git remote add review https://<host-ip>:8443/git/<project>.git
git push review fix/my-branch

# And review at https://<host-ip>:8443
```

A project can also start from a repository already on the server's machine:
under Administration, give its path when creating the project. Members then
press "Sync from source" on the project page to bring in new commits.

### Running in the background

From a clone, `scripts/garret.sh` keeps the server running after the
terminal closes and prints the addresses it listens on:

```sh
scripts/garret.sh start [garret serve flags]   # also: stop, restart, status, log
```

It runs `bin/garret`, writes the log to `~/garret.log`, and also prints
how members join. The flags of a start are kept, so after
`scripts/garret.sh start --client-cert=off` a plain `start` or `restart`
uses `--client-cert=off` again; `start --` starts without them. If the
server is already running with other flags, `start` says so and does not
change it. `stop` waits for the server to finish its requests and says
when it has stopped.

### Server options

`garret serve --help` lists every flag; the main ones are:

| Flag | Default | Meaning |
|---|---|---|
| `--data` | `~/Library/Application Support/garret` on macOS, `~/.local/share/garret` on Linux | Data directory, mode 700 |
| `--host` | the machine's name plus `.local` | Host name in the certificate, the banner and clone URLs |
| `--allow` | the private network of the machine's interfaces; with several, the one of the first start | Allowed network in CIDR form, repeatable |
| `--listen`, `--enroll-listen` | every allowed address and loopback, ports 8443 and 8444 | Address of the main and the enrollment listener |
| `--client-cert` | chosen at the first start | `on` or `off` |
| `--tls-cert`, `--tls-key` | none | An externally issued server certificate |

Virtual machines, containers and Internet Sharing add private networks of
their own. If the machine is on several when garret first starts, it
refuses to guess and prints one `garret serve --allow <network>` command
per network; run the one for the Wi-Fi or Ethernet connection the team is
on. Later starts choose that network again by themselves.

The certificate authority is created at the first start and can vouch only
for the host name and the allowed networks of that start. If either changes,
for example when the machine moves to another network, `garret serve`
refuses to start. To issue a new authority, stop the server and move the
files in `tls/` under the data directory somewhere else; projects, reviews
and accounts stay in the database. Members then trust the new `ca.crt`, and
with client certificates on, enroll again.

Requirements: macOS or Linux, and Git on the host machine.

## Security model

garret is designed for a trusted, small group on a local network. Its
protections are scoped to that setting:

- **Network boundary.** Only clients on the configured local subnet are
  accepted. garret is not designed to be exposed to the internet, and it
  should not be placed behind a port forward or a public reverse proxy.
- **Local data.** Repositories and review data stay on the host machine.
- **Your responsibility.** A shared access point is only as private as its
  members. Use a network you control, and remove the data when the work is
  released.

The full threat model, authentication and encryption design are described in
[docs/design/security.md](docs/design/security.md).

## Roadmap

- [x] Architecture and data model ([docs/design](docs/design/README.md))
- [x] Git over HTTP (push and fetch)
- [x] Subnet-based access control
- [x] Review creation from a pushed branch
- [x] Diff view and inline comments
- [x] Revisions and review history
- [x] Web frontend
- [x] First pre-release (`v0.1.0-rc.1`)
- [ ] First release (`v0.1.0`)

## Contributing

Contributions are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md)
before opening an issue or pull request, and follow the
[Code of Conduct](CODE_OF_CONDUCT.md).

## Security policy

Please do not report security vulnerabilities through public issues. See
[SECURITY.md](SECURITY.md) for how to report them privately.

## License

garret is licensed under the [Apache License 2.0](LICENSE).
