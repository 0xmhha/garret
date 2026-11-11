# Development process

garret is built spec first, test first, and bottom up. This document is the
rule set for every code change. It applies to maintainers, contributors and
coding assistants alike.

## 1. Spec Driven Development

No production code is written before its spec is reviewed.

1. **Data first.** Define the data a feature reads and writes: entities,
   fields, types, constraints, and the moment each piece of data is created,
   read, updated, or deleted. Update the ERD and the schema migration plan in
   [design/data-model.md](design/data-model.md) before anything else. How data
   moves and where it is read or written shapes the rest of the design.
2. **Feature spec.** Write `docs/specs/<feature>.md` with:
   - the data flow: which component sends which data to which, and when
   - every function to be added, with its contract (see section 3)
   - the concurrency model: which goroutines exist, what they share, and how
     access is synchronized (see section 5)
   - the test case table for each function
3. **Review.** The spec is merged or approved before implementation starts.
   A change in behavior during implementation updates the spec first.

## 2. Test Driven Development

Each function is implemented in a red, green, refactor loop:

1. Write the test cases from the spec. Run them and watch them fail.
2. Write the smallest code that makes them pass.
3. Refactor with the tests green.

Tests are table-driven. Error paths are tested as thoroughly as the happy
path.

## 3. Function contracts

Before writing a function, define:

| Part | Content |
|---|---|
| Input | Each parameter, its type, its valid range, and what happens for invalid values |
| Output | Return values, the error values and when each one is returned |
| Logic | The steps the function performs, in order |
| Side effects | Everything the function changes outside its return values (database rows, files, channels, shared state). The default is none |
| Concurrency | Whether it is safe to call from several goroutines, and why |

Test cases must cover:

- **Normal cases**: typical valid inputs.
- **Boundary cases**: empty, zero, one, maximum, minimum, exact limits and
  one past them.
- **Invalid cases**: malformed, nil, out-of-range and hostile input.
- **Failure cases**: every dependency failing (database, Git, file system,
  timeout, cancelled context).
- **Side-effect checks**: on failure, the function leaves no partial state
  behind; on success, it changes only what the contract says.

A function whose cases cannot be enumerated is too large and is split.

## 4. Bottom-up construction

Build from the lowest layer up:

1. **Atomic functions**: pure functions with no I/O (parsing, validation,
   position mapping, hashing).
2. **Components**: functions that combine atomic functions with one
   dependency each (a repository method, a Git command wrapper).
3. **Services**: business operations that coordinate components inside one
   transaction or one unit of work.
4. **Transport**: HTTP, WebSocket and Git handlers that translate requests
   into service calls.

A layer is implemented only after the layer below it is tested. Higher layers
depend on interfaces of lower layers, so they can be tested with fakes.

## 5. Concurrency and data consistency

- Every goroutine has a defined owner and a defined way to stop (a context or
  a closed channel). A goroutine that cannot be stopped is not started.
- Share data by communicating over channels where an owner can be named;
  protect shared state with a mutex where it cannot. Each mutex documents what
  it guards.
- Bounded parallelism uses a semaphore (for example, the number of concurrent
  Git subprocesses).
- Database writes that must be consistent with each other happen in one
  transaction. Operations that read and then write the same rows serialize
  through the owner described in the spec.
- Every blocking call has a timeout or a context, so a stuck dependency
  cannot stop the whole server.
- Concurrent code is tested with `-race`, and time-dependent code with
  `testing/synctest`.

## 6. Design choices

Patterns, data structures and libraries are chosen for correctness first and
performance second, from current and well-established sources. A spec that
introduces a pattern or a data structure states why it fits, what it costs,
and which alternative was rejected. Performance claims are backed by
benchmarks (`go test -bench`).

## 7. Choosing a library

A library is never chosen for a single reason such as "it is already in the
module graph" or "it is the most popular". Candidates, including writing the
code ourselves, are compared on these criteria in this order:

1. **Correctness and standards.** Does it do the job correctly, and does it
   follow the relevant standard?
2. **Fit with project rules.** Can time, randomness and I/O be injected? Are
   results deterministic? Does it avoid package-level mutable state?
3. **Maintenance and supply chain.** Who maintains it (an organization or one
   person), how recent is the latest release, and what does it pull into the
   build? A runtime-path module without a release in over a year needs a
   stated reason.
4. **Features.** Does it cover what the spec needs without large unused parts?
5. **Performance.** Measured with a benchmark when it matters.
6. **Footprint.** How many modules it adds to `go list -deps`.

A new dependency is worth adding when it is clearly better on the higher
criteria. Footprint alone does not decide. The spec records the comparison
table, the choice and the rejected candidates.

## 8. Definition of done for a change

- The spec and the design documents match the code.
- All tests pass with `-race`, and the linters pass.
- New behavior is reachable from the real entry point, not only from tests.
