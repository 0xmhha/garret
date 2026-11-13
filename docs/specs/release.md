# Release specification

Decision D3 of the team access review: releases are built by GitHub Actions
from a tag, with checksums and build provenance, so that anyone who downloads
garret can check that this repository's CI built it.

## 1. Trigger

Pushing a tag `v<major>.<minor>.<patch>` runs `.github/workflows/release.yml`.
A tag with a suffix, such as `v0.1.0-rc.1`, is published as a pre-release.
A pull request that changes the workflow or the `Makefile` runs the same
build as a dry run, without attesting or publishing anything.

## 2. Steps

1. Check out with full history, so that `go build` stamps the tag as the
   module version (`garret version` shows it).
2. `make build test`: the web interface is built and tested, then the Go
   tests run with the race detector against it.
3. `make dist`: one archive per platform, each holding `garret`, `LICENSE`
   and `README.md`, built with `-tags webui -trimpath` and `CGO_ENABLED=0`:

   | Archive | Platform |
   |---|---|
   | `garret_<version>_darwin_arm64.tar.gz` | macOS, Apple silicon |
   | `garret_<version>_darwin_amd64.tar.gz` | macOS, Intel |
   | `garret_<version>_linux_amd64.tar.gz` | Linux, x86-64 |
   | `garret_<version>_linux_arm64.tar.gz` | Linux, ARM64 |

   `dist/SHA256SUMS` lists the SHA-256 of this version's archives. Archives
   are named by version and overwritten on a rebuild; the staging folders
   stay under `dist/`, which Git ignores. `make dist` refuses to run when the
   web interface is not built.
4. Tags only: `actions/attest-build-provenance` signs a SLSA build provenance
   statement for every archive (GitHub's Sigstore instance; the job gets
   `id-token: write` and `attestations: write` for this step's job only).
5. Tags only: `gh release create` publishes the archives and `SHA256SUMS`
   with generated notes. The job has `contents: write`; nothing else does.

## 3. Verifying a download

```sh
shasum -a 256 -c SHA256SUMS --ignore-missing
gh attestation verify garret_<version>_darwin_arm64.tar.gz --repo 0xmhha/garret
```

## 4. Not done

The macOS binaries are not notarized: that needs a paid Apple developer
account. A file downloaded with a browser carries the quarantine attribute,
and macOS refuses to open it until it is allowed under System Settings ›
Privacy & Security; a download with `curl` does not.
