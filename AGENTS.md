# Development Guide

This guide applies to the entire repository and provides context for agents and
human contributors. See [README.md](README.md) for usage examples and
[CONTRIBUTING.md](CONTRIBUTING.md) for the contribution process.

## Background

The AlloyDB Go Connector (`cloud.google.com/go/alloydbconn`) is a Go library for
connecting applications to Google Cloud AlloyDB. It handles IAM authorization,
short-lived client certificates, and TLS 1.3 connections, with optional automatic
IAM database authentication. Applications use it with `database/sql` or pgx.

The connector exists to make strong connection security easier to operate.
Standard PostgreSQL drivers support TLS encryption and, when configured,
certificate verification, but do not manage AlloyDB's IAM integration or client
certificate lifecycle. The connector verifies the server certificate and
presents a short-lived client certificate for the AlloyDB server-side proxy to
verify, providing mutual TLS. It combines this with Cloud IAM authorization and
automatic client certificate issuance and rotation. These capabilities are
especially useful in high-security environments that require verified identities,
centrally controlled access, and minimal manual certificate management.

The connector supplies secure connections; the PostgreSQL driver handles the
database protocol, and `database/sql` or `pgxpool` manages connection pooling.
It does not create network connectivity. Private IP is the default and requires
an accessible private network; public IP and Private Service Connect (PSC) are
explicit dial options. Instance identifiers have this form:

```text
projects/PROJECT/locations/REGION/clusters/CLUSTER/instances/INSTANCE
```

The project uses Go modules and follows semantic versioning. Consult `go.mod`
for the minimum Go version (currently 1.25.8) and `.github/workflows/tests.yaml`
for the tested versions and platforms.

## Architecture

| Location | Responsibility |
| --- | --- |
| Core dialer | Public dialer lifecycle; per-instance caches, TCP/TLS connections, metadata exchange, and connection instrumentation. |
| Configuration | Functional options for credentials, IAM authentication, network selection, and certificate refresh behavior. |
| AlloyDB integration | AlloyDB Admin API integration, certificate lifecycle, instance metadata, and connection-info caching. |
| Database drivers | Preferred `database/sql` adapter built on pgx v5, plus deprecated pgx compatibility adapters. |
| Telemetry | OpenCensus metrics/traces and OpenTelemetry-based built-in Cloud Monitoring metrics. |
| Supporting packages | Public debug logging, structured connector errors, and test fakes for AlloyDB instances and the local server proxy. |
| Tests and automation | Unit and live integration tests, formatting and lint commands, and CI workflows. |

A normal connection follows this sequence:

1. `NewDialer` resolves credentials and options and initializes API clients.
2. `Dial` parses the instance URI and retrieves cached connection information.
   The default cache refreshes metadata and certificates in the background;
   `WithLazyRefresh` refreshes them when a connection needs fresh information.
3. The dialer selects the private IP, public IP, or PSC address, opens TCP port
   5433, and performs TLS 1.3 authentication using the client certificate and
   instance CA.
4. A metadata exchange runs over the established TLS connection, including IAM
   authentication data when enabled. The resulting instrumented `net.Conn` is
   returned to the database driver.

**Connector best practices:** Reuse dialers across connections. Close pools/connections and call `Dialer.Close`
or the registered driver's cleanup function when finished so background refresh
and telemetry work can stop. Connector examples use `sslmode=disable` because
the connector already establishes TLS.

## Testing

Run commands from the repository root with a Go toolchain compatible with
`go.mod`. Go downloads module dependencies as needed. The shell scripts require
Bash; the race detector also requires a supported platform and a C toolchain.

### Unit tests

No Google Cloud credentials or live AlloyDB instance are needed. Some tests
start local network listeners.

```sh
# Fast local run; -short skips the live integration tests.
go test -short ./...

# CI-style run with verbose output, race detection, and coverage.
./scripts/test_unit.sh
# Equivalent: go test -v -race -cover -short ./...

# Focus on a package or test while iterating.
go test -short ./internal/alloydb
go test -short -run '^TestDialerCanConnectToInstance$' .
```

Keep `-short` when running locally without cloud infrastructure. Plain
`go test ./...` also runs live integration tests.

### Integration tests

See the [end-to-end tests](e2e_test.go) for the authoritative list of required
environment variables, test cases, and connection modes. Set those values in
your local environment or an ignored `.envrc` file before running tests.

- Set `ALLOYDB_INSTANCE_NAME`, `ALLOYDB_DB`, `ALLOYDB_USER`, `ALLOYDB_PASS`, and
  `ALLOYDB_IAM_USER`. The named instance needs public IP enabled for public-IP
  tests, and the IAM database user must be configured for IAM authentication.
- Enable the AlloyDB API and provide Google Cloud credentials with access to
  the instance. The prerequisites in `README.md` describe the required IAM
  roles. Credential-option tests additionally require
  `GOOGLE_APPLICATION_CREDENTIALS` to point to a credentials JSON file; a local
  ADC login alone does not satisfy that test requirement.
- Set `ALLOYDB_PSC_INSTANCE_URI` for the PSC test and run from a network that
  can reach the private IP and PSC instance.

```sh
# Run the integration suite, including private IP, public IP, PSC, and direct connections.
./scripts/test_system.sh
```

The script enables race detection and coverage. `-short` skips all integration
tests. CI runs unit tests across macOS, Windows, and Linux; live integration
tests run on a configured Linux runner and are skipped for pull requests from
forks and Dependabot.

## Contribution Guidelines

- Search existing issues and PRs before starting work. Open an issue first for
  a new feature, public API change, substantial refactor, or a bug whose solution
  needs discussion. Describe the use case or reproduction and proposed approach
  so maintainers can help establish scope. Small, well-understood bug fixes,
  documentation corrections, and test improvements can go directly to a PR;
  link any related issue and explain the change. Use `SECURITY.md` for security
  reports rather than a public issue.
- Use [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/)
  for commit messages: `type(optional-scope): short description`.
  [Release Please](https://github.com/googleapis/release-please) parses these
  messages to prepare version bumps and release notes; this repository's
  configuration is in `.github/release-please.yml`. Use `feat` for new features,
  `fix` for bug fixes, and appropriate types such as `docs`, `test`, `refactor`,
  `ci`, or `chore` for other work. For example:

  ```text
  feat: add a new dial option
  fix(alloydb): refresh expired client certificates before dialing
  docs: clarify integration test prerequisites
  ```

- Read the relevant implementation and nearby tests before editing. Keep
  changes focused and preserve unrelated work already present in the checkout.
- Preserve public API compatibility and the distinction between dialer-level
  and per-connection options. Add Go doc comments for exported symbols and
  update `README.md` or package documentation when usage changes. Breaking
  changes to the public API are unacceptable; maintain compatibility for
  existing users.
- Follow existing context, error-wrapping, locking, and cleanup patterns.
  Changes to dialing or refresh logic should account for cancellation,
  certificate expiry, concurrent callers, and resource cleanup. Keep the
  metadata exchange after TLS establishment.
- Add or update tests for behavior changes. Prefer the existing
  `internal/mock` helpers for unit coverage; keep live tests gated by
  `testing.Short()` and use the existing private-network guard where relevant.
  Do not introduce cloud credentials or live services into unit tests.
- Format Go code with `goimports` and use the repository lint configuration
  (`.golangci.yml`, golangci-lint v2). With `goimports` and `golangci-lint`
  installed and available on `PATH`, run:

  ```sh
  ./scripts/format.sh
  ./scripts/lint.sh
  ./scripts/test_unit.sh
  ```

  The formatter rewrites Go files across the repository, so review its diff.
  The lint script runs `go mod tidy`, checks `go.mod` and `go.sum` against Git,
  and then runs golangci-lint. Intentional uncommitted dependency changes will
  cause that diff check to fail; review those changes and run
  `golangci-lint run --timeout 3m` separately when needed.
- Keep dependencies tidy and include both `go.mod` and `go.sum` changes when
  appropriate. Follow existing Google LLC / Apache-2.0 headers in new source
  files. Keep credentials, tokens, passwords, and local `.envrc` values out of
  commits and logs.
- Submit changes through a reviewed pull request and satisfy the Google
  Contributor License Agreement described in `CONTRIBUTING.md`. Explain the
  behavior change and report the checks run, including any integration coverage
  that was not available. CI also checks coverage against the base branch.
- Follow `CODE_OF_CONDUCT.md` and the community guidance in `CONTRIBUTING.md`.
  Report vulnerabilities through the process in `SECURITY.md`.
