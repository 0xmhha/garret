# garret

[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![Status](https://img.shields.io/badge/status-early%20development-orange.svg)](#project-status)
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
- [Planned features](#planned-features)
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

## Planned features

garret is in early development. The items below describe the intended scope,
not what works today.

- Git repository hosting over HTTPS, so members can `git push` and `git fetch`
- Branch-based review, read commit by commit or as a whole
- Revision tracking: each push becomes a new, comparable revision, kept even
  after force pushes
- Inline comments with threads, carried over to new revisions
- Review states and verdicts recorded with who decided and when
- Discussion posts and review descriptions edited by several people at once
- Live updates, an in-app inbox with mentions, and file attachments
- Invitation-only accounts, device certificates (mutual TLS) and personal
  access tokens for Git
- Access restricted to the local subnet and to enrolled devices, with
  automatically rotated 7-day certificates
- Edit history for everything, and a purge command for when the work is public
- A web frontend and an embedded database in the same binary

See [docs/design](docs/design/README.md) for the full design.

## Project status

garret is under active design. There is no usable release yet, and APIs, data
formats and configuration may change without notice. Follow the
[Roadmap](#roadmap) and open issues to see what is being worked on.

## Getting started

Build and installation instructions will be added with the first working
version. The intended workflow looks like this:

```sh
# On the host machine: prints the allowed network and the CA fingerprint
garret serve

# On a developer machine on the same network, once per device
garret enroll '<invitation link>'

# Then use Git as usual
git remote add review https://<host-ip>:8443/git/<project>.git
git push review fix/my-branch

# Then open https://<host-ip>:8443 in a browser
```

Requirements (planned): macOS or Linux, Git on the host machine, and Go 1.26.8
or newer to build from source.

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
- [ ] Git over HTTP (push and fetch)
- [ ] Subnet-based access control
- [ ] Review creation from a pushed branch
- [ ] Diff view and inline comments
- [ ] Revisions and review history
- [ ] Web frontend
- [ ] First release

## Contributing

Contributions are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md)
before opening an issue or pull request, and follow the
[Code of Conduct](CODE_OF_CONDUCT.md).

## Security policy

Please do not report security vulnerabilities through public issues. See
[SECURITY.md](SECURITY.md) for how to report them privately.

## License

garret is licensed under the [Apache License 2.0](LICENSE).
