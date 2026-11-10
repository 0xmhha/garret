# Testing and definition of done

The first release is done when the scenario below passes as an automated
test and every quality gate is green.

## End-to-end scenario

Three users on one network: **A** runs the server and is the administrator,
**B** writes the fix, **C** reviews it.

| Step | Behavior | Verified by |
|---|---|---|
| 1 | A runs `garret serve`. The allowed network and the certificate fingerprint are printed | Go integration test |
| 2 | A creates two invitations. B and C accept them and create accounts | Go integration test |
| 3 | B pushes a branch with three commits and opens a review | Go integration test |
| 4 | C comments on individual commits. B and C edit the review description at the same time and both screens converge to the same text | Playwright, two browser contexts |
| 5 | B force-pushes a fix. Revision 2 appears, the diff between revisions 1 and 2 is shown, and C's unresolved comments appear at their new positions | Playwright |
| 6 | C approves and completes the review. The verdict and state change are recorded with user and time | Playwright |
| 7 | Requests from an address outside the allowed network, a push without a token, and a push with a token that belongs to a different user are all rejected | Go integration test |
| 8 | After a server restart, every record from steps 2 to 6 is still present | Go integration test |
| 9 | A exports and purges the project. No row, repository file or attachment of the project remains | Go integration test |

The three users are simulated inside the tests. Step 7 sets a narrow
`--allow` range and injects the peer address, so no second network is needed.
Before the first release the scenario is also run once by hand on three real
machines.

## Test layers

| Layer | Tool | Scope |
|---|---|---|
| Unit | `go test` | Packages in isolation, table-driven, error paths included |
| Integration | `go test` with a real `git` and a temporary data directory | HTTP API, Git push and fetch, revisions, purge |
| Component | Vitest | React components and client-side logic |
| End to end | Playwright | Steps 4 to 6 against a built binary |
| Fuzz | `go test -fuzz` | Diff parsing, anchor parsing, CIDR handling |

Time and randomness are injected, so tests are deterministic.

## Quality gates

| Gate | Requirement |
|---|---|
| Go tests | `go test -race ./...` passes |
| Go lint | `golangci-lint run` passes |
| Frontend | Type check, lint and Vitest pass |
| End to end | The Playwright suite passes |
| Secrets | `betterleaks` reports no findings |
| Vulnerabilities | `govulncheck` and `npm audit` report no known exploitable issue |
| Platforms | Required on macOS; also run on Linux in CI without blocking a release |
| Browsers | Latest Chrome, Firefox and Safari |
