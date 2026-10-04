# Development Guide

This guide applies to the entire repository and provides context for agents and
human contributors. See [README.md](README.md) for usage examples and
[CONTRIBUTING.md](CONTRIBUTING.md) for the contribution process.

## Background

The AlloyDB Go Connector (`cloud.google.com/go/alloydbconn`) is a Go library for
connecting applications to Google Cloud AlloyDB. It handles IAM authorization,
short-lived client certificates, and TLS 1.3 connections, with optional automatic
IAM database authentication. Applications use it with `database/sql` or pgx.

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
| `dialer.go` | Public `NewDialer`, `Dial`, and `Close` lifecycle; per-instance caches, TCP/TLS connections, metadata exchange, and connection instrumentation. |
| `options.go` | Functional configuration options: `Option` configures a dialer; `DialOption` configures individual connections or their defaults. Includes credentials, IAM authentication, network selection, and refresh strategy. |
| `internal/alloydb/` | Instance URI parsing, AlloyDB Admin API calls, certificate handling, and connection-info caches. `instance.go` implements refresh-ahead caching, `lazy.go` refreshes on demand, and `static.go` supplies development-only static data without refreshing it. |
| `driver/postgres/` | Preferred `database/sql` adapter, built on pgx v5. `RegisterDriver` returns a cleanup function. |
| `driver/pgxv4/`, `driver/pgxv5/` | Deprecated compatibility wrappers that delegate to `driver/postgres`; retain compatibility when making changes. |
| `internal/tel/`, `internal/tel/v2/` | Existing OpenCensus metrics/traces and OpenTelemetry-based built-in Cloud Monitoring metrics, respectively. |
| `debug/`, `errtype/` | Public logging interfaces and structured configuration, refresh, and dial errors. |
| `internal/mock/` | Fake AlloyDB instances, Admin API responses, and a local server proxy for tests. |
| Root `*_test.go` files | Unit tests, live integration tests, and connection examples/helpers for `database/sql` and `pgxpool`. |
| `scripts/`, `.github/workflows/` | Local formatting, lint, and test commands plus CI configuration. |

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

Reuse dialers across connections. Close pools/connections and call `Dialer.Close`
or the registered driver's cleanup function when finished so background refresh
and telemetry work can stop. Connector examples use `sslmode=disable` because
the connector already establishes TLS.

## How to Run the Tests

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

Use `.envrc.example` as the configuration template. Set values in your local
environment or an ignored `.envrc` file and load them before running tests.

- Set `ALLOYDB_INSTANCE_NAME`, `ALLOYDB_DB`, `ALLOYDB_USER`, `ALLOYDB_PASS`, and
  `ALLOYDB_IAM_USER`. The named instance needs public IP enabled for public-IP
  tests, and the IAM database user must be configured for IAM authentication.
- Enable the AlloyDB API and provide Google Cloud credentials with access to
  the instance. The prerequisites in `README.md` describe the required IAM
  roles. Credential-option tests additionally require
  `GOOGLE_APPLICATION_CREDENTIALS` to point to a credentials JSON file; a local
  ADC login alone does not satisfy that test requirement.
- For the full suite, also set `ALLOYDB_INSTANCE_IP` and
  `ALLOYDB_PSC_INSTANCE_URI` and run from a network that can reach both the
  private IP and the PSC instance.

```sh
# Run without private-network access, using the public-IP test instance.
./scripts/test_system.sh --skip-private-ip

# Run the full suite, including private IP, PSC, and direct connections.
./scripts/test_system.sh
```

Both script modes enable race detection and coverage. `--skip-private-ip` skips
private-IP connector tests, PSC tests, and direct-connection tests; it does not
remove the need for cloud credentials or a live public-IP instance. The
underlying custom Go test flag is defined only in the root package:

```sh
go test -v . -skip-private-ip
```

Use the script to apply this mode across all packages rather than passing the
custom flag to `go test ./...`. `-short` skips all integration tests regardless
of the private-IP flag. CI runs unit tests across macOS, Windows, and Linux;
live integration tests run on a configured Linux runner and are skipped for
pull requests from forks and Dependabot.

## Contribution Guidelines

- Read the relevant implementation and nearby tests before editing. Keep
  changes focused and preserve unrelated work already present in the checkout.
- Preserve public API compatibility and the distinction between dialer-level
  and per-connection options. Add Go doc comments for exported symbols and
  update `README.md` or `doc.go` when usage changes.
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
