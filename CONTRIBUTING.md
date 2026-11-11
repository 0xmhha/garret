# Contributing to garret

Thank you for your interest in garret. This document explains how to propose
changes and what we expect from a contribution.

By participating, you agree to follow our [Code of Conduct](CODE_OF_CONDUCT.md).

## Before you start

- **Bugs and feature requests**: search the
  [existing issues](https://github.com/0xmhha/garret/issues) first. If nothing
  matches, open a new issue using one of the templates.
- **Questions**: use [Discussions](https://github.com/0xmhha/garret/discussions)
  rather than issues.
- **Security vulnerabilities**: do not open a public issue. Follow
  [SECURITY.md](SECURITY.md).
- **Larger changes**: open an issue to discuss the design before writing code.
  This avoids work that cannot be merged.

## Development setup

garret is in early development, and the toolchain may change. The current
expectations are:

- macOS or Linux
- Go 1.26.8 or newer
- Git
- Node.js (LTS) for the web frontend, once it exists

Read the [design documents](docs/design/README.md) and the
[development process](docs/development-process.md) before working on a
feature. Code changes follow that process: data definition and spec first,
then tests, then implementation from the lowest layer up. Changes that contradict [decisions.md](docs/design/decisions.md)
should start with an issue that proposes updating the decision.

```sh
git clone https://github.com/0xmhha/garret.git
cd garret
```

Build and test commands will be documented here as the code lands.

## Workflow

1. Fork the repository and create a branch from `main`.
2. Name the branch after the change, for example `feat/inline-comments` or
   `fix/diff-rename-detection`.
3. Make focused commits. One logical change per pull request.
4. Add or update tests for the behavior you change.
5. Make sure formatting, linting and tests pass locally.
6. Open a pull request against `main` and fill in the template.

## Commit messages

We follow [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<optional scope>): <imperative summary>
```

Common types: `feat`, `fix`, `docs`, `test`, `refactor`, `perf`, `build`,
`ci`, `chore`.

Examples:

```
feat(review): add threaded replies to inline comments
fix(git): reject pushes from clients outside the subnet
docs: describe the revision model
```

Keep the summary under 72 characters, in English, and in the imperative mood.
Use the body to explain *why* the change is needed when it is not obvious.

## Code style

### Go

- Format with `gofmt` (or `goimports`).
- Code must pass `go vet` and `golangci-lint`.
- Run tests with the race detector: `go test -race ./...`.
- Exported identifiers need doc comments that start with the identifier name.
- Return errors instead of panicking, and wrap them with `fmt.Errorf("...: %w", err)`.

### Frontend

Frontend conventions will be added when the frontend is introduced.

## Pull request checklist

- [ ] The change is focused and described in the pull request
- [ ] Tests cover the new or changed behavior, including error paths
- [ ] Formatting, linting and tests pass
- [ ] Documentation is updated if behavior or configuration changed
- [ ] No secrets, credentials or private network details are included

## Licensing of contributions

garret is licensed under the [Apache License 2.0](LICENSE). Unless you state
otherwise, any contribution you intentionally submit for inclusion is licensed
under the same terms, as described in section 5 of the license.
