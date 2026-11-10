# Security Policy

## Supported versions

garret has not had a release yet. Until the first release, security fixes are
applied to the `main` branch only.

| Version | Supported |
| ------- | --------- |
| `main`  | Yes       |

## Reporting a vulnerability

Please **do not** report security vulnerabilities through public GitHub
issues, discussions or pull requests.

Report them privately through GitHub's
[private vulnerability reporting](https://github.com/0xmhha/garret/security/advisories/new).

Include as much of the following as you can:

- The type of issue (for example, access control bypass or path traversal)
- The affected component and file paths
- The version or commit you tested
- Steps to reproduce, and proof-of-concept code if available
- The impact you expect an attacker could achieve

## What to expect

- We aim to acknowledge a report within 7 days.
- We will keep you informed while we investigate and fix the issue.
- We will credit you in the advisory unless you prefer to stay anonymous.

## Scope

garret is designed to run on a trusted local network and to accept only
clients on the local subnet. Reports are especially welcome for:

- Ways to reach the server from outside the allowed subnet
- Ways to read or modify repositories or review data without authorization
- Path traversal or command injection through Git operations

Deploying garret on a public network, behind a port forward or a public
reverse proxy is outside its intended use. Issues that only occur in such a
setup are still welcome, but may be treated as hardening rather than
vulnerabilities.
