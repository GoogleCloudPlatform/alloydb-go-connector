# AlloyDB Go Connector

The _AlloyDB Go Connector_ is an AlloyDB connector designed for use with the Go
language. Using an AlloyDB connector provides the following benefits:

* **IAM Authorization:** uses IAM permissions to control who/what can connect to
  your AlloyDB instances

* **Improved Security:** uses TLS 1.3 encryption and identity verification
  between the client connector and the server-side proxy, independent of the
  database protocol.

* **Convenience:** removes the requirement to use and distribute SSL
  certificates, as well as manage firewalls or source/destination IP addresses.

## Installation

You can install this repo with `go get`:
```sh
go get cloud.google.com/go/alloydbconn
```

## Usage

This package provides several functions for authorizing and encrypting
connections. These functions can be used with your database driver to connect to
your AlloyDB instance.

### APIs and Services

This package requires the following to connect successfully:

- IAM principal (user, service account, etc.) with the [AlloyDB
  Client][client-role] role or equivalent. [Credentials](#credentials) for the IAM principal are used to authorize connections to an AlloyDB instance.

- The [AlloyDB Admin API][admin-api] to be enabled within your Google Cloud
  Project. By default, the API will be called in the project associated with the
  IAM principal.

[admin-api]:   https://console.cloud.google.com/apis/api/alloydb.googleapis.com
[client-role]: https://cloud.google.com/alloydb/docs/auth-proxy/overview#how-authorized

### Credentials

This repo uses the [Application Default Credentials (ADC)][adc] strategy for
resolving credentials. Please see the [golang.org/x/oauth2/google][google-auth]
documentation for more information in how these credentials are sourced.

To explicitly set a specific source for the Credentials to use, see [Using
Options](#using-options) below.

[adc]: https://cloud.google.com/docs/authentication
[google-auth]: https://pkg.go.dev/golang.org/x/oauth2/google#hdr-Credentials

### Connecting with pgx

To use the dialer with [pgx](https://github.com/jackc/pgx), use
[pgxpool](https://pkg.go.dev/github.com/jackc/pgx/v4/pgxpool) by configuring a
[Config.DialFunc][dial-func] like so:

``` go
// Configure the driver to connect to the database
dsn := fmt.Sprintf("user=%s password=%s dbname=%s sslmode=disable", pgUser, pgPass, pgDB)
config, err := pgxpool.ParseConfig(dsn)
if err != nil {
    log.Fatalf("failed to parse pgx config: %v", err)
}

// Create a new dialer with any options
d, err := alloydbconn.NewDialer(ctx)
if err != nil {
    log.Fatalf("failed to initialize dialer: %v", err)
}
defer d.Close()

// Tell the driver to use the Cloud SQL Go Connector to create connections
config.ConnConfig.DialFunc = func(ctx context.Context, _ string, instance string) (net.Conn, error) {
    return d.Dial(ctx, "projects/<PROJECT>/locations/<REGION>/clusters/<CLUSTER>/instances/<INSTANCE>")
}

// Interact with the dirver directly as you normally would
conn, err := pgxpool.ConnectConfig(context.Background(), config)
if err != nil {
    log.Fatalf("failed to connect: %v", connErr)
}
defer conn.Close()
```

[dial-func]: https://pkg.go.dev/github.com/jackc/pgconn#Config

### Using Options

If you need to customize something about the `Dialer`, you can initialize
directly with `NewDialer`:

```go
ctx := context.Background()
d, err := alloydbconn.NewDialer(
    ctx,
    alloydbconn.WithCredentialsFile("key.json"),
)
if err != nil {
    log.Fatalf("unable to initialize dialer: %s", err)
}

conn, err := d.Dial(ctx, "projects/<PROJECT>/locations/<REGION>/clusters/<CLUSTER>/instances/<INSTANCE>")
```

For a full list of customizable behavior, see alloydbconn.Option.

### Using DialOptions

If you want to customize things about how the connection is created, use
`DialOption`:

```go
conn, err := d.Dial(
    ctx,
    "projects/<PROJECT>/locations/<REGION>/clusters/<CLUSTER>/instances/<INSTANCE>",
    alloydbconn.WithPrivateIP(),
)
```

You can also use the `WithDefaultDialOptions` Option to specify DialOptions to
be used by default:

```go
d, err := alloydbconn.NewDialer(
    ctx,
    alloydbconn.WithDefaultDialOptions(
        alloydbconn.WithTCPKeepAlive(30*time.Second),
    ),
)
```

### Using the dialer with database/sql

Using the dialer directly will expose more configuration options. However, it is
possible to use the dialer with the `database/sql` package.

To use `database/sql`, use `pgxv4.RegisterDriver` with any necessary Dialer
configuration. Note: the connection string must use the keyword/value format
with host set to the instance connection name.

``` go
package foo

import (
    "database/sql"

    "cloud.google.com/go/alloydbconn"
    "cloud.google.com/go/alloydbconn/driver/pgxv4"
)

func Connect() {
    cleanup, err := pgxv4.RegisterDriver("alloydb", alloydbconn.WithIAMAuthN())
    if err != nil {
        // ... handle error
    }
    defer cleanup()

    db, err := sql.Open(
        "alloydb",
        "host=projects/<PROJECT>/locations/<REGION>/clusters/<CLUSTER>/instances/<INSTANCE> user=myuser password=mypass dbname=mydb sslmode=disable",
	)
    // ... etc
}
```

### Enabling Metrics and Tracing

This library includes support for metrics and tracing using [OpenCensus][]. To
enable metrics or tracing, you need to configure an [exporter][]. OpenCensus
supports many backends for exporters.

For example, to use [Cloud Monitoring][] and [Cloud Trace][], you would
configure an exporter like so:

```golang
package main

import (
    "contrib.go.opencensus.io/exporter/stackdriver"
    "go.opencensus.io/trace"
)

func main() {
    sd, err := stackdriver.NewExporter(stackdriver.Options{
        ProjectID: "mycoolproject",
    })
    if err != nil {
        // handle error
    }
    defer sd.Flush()
    trace.RegisterExporter(sd)

    sd.StartMetricsExporter()
    defer sd.StopMetricsExporter()

    // Use alloydbconn as usual.
    // ...
}
```

[OpenCensus]: https://opencensus.io/
[exporter]: https://opencensus.io/exporters/
[Cloud Monitoring]: https://cloud.google.com/monitoring
[Cloud Trace]: https://cloud.google.com/trace

## Support policy

### Major version lifecycle

This project uses [semantic versioning](https://semver.org/), and uses the
following lifecycle regarding support for a major version:

**Active** - Active versions get all new features and security fixes (that
wouldn’t otherwise introduce a breaking change). New major versions are
guaranteed to be "active" for a minimum of 1 year.

**Deprecated** - Deprecated versions continue to receive security and critical
bug fixes, but do not receive new features. Deprecated versions will be
supported for 1 year.

**Unsupported** - Any major version that has been deprecated for >=1 year is
considered unsupported.

## Supported Go Versions

We test and support at minimum, the latest three Go versions. Changes in
supported Go versions will be considered a minor change, and will be listed in
the release notes.

### Release cadence

This project aims for a release on at least a monthly basis. If no new features
or fixes have been added, a new PATCH version with the latest dependencies is
released.
