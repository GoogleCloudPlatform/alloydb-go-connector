# Migration Guide: OpenCensus to OpenTelemetry

The AlloyDB Go connector has dropped its dependency on OpenCensus. All
metrics and traces are now produced through [OpenTelemetry][otel] using the
process-global `MeterProvider` and `TracerProvider`.

[otel]: https://opentelemetry.io/

This guide explains what changed and how to update an application that consumed
the previous OpenCensus-based telemetry.

## TL;DR

- Remove every OpenCensus exporter, view registration, and import.
- Register an OpenTelemetry `MeterProvider` and `TracerProvider` at process
  startup. The connector picks them up automatically — there is nothing to
  configure on the `Dialer`.
- Update dashboards and alerts to use the new metric names listed below.

## Why the change?

OpenCensus has been deprecated in favor of OpenTelemetry. See the [announcement
from 2023][announcement]. After three additional years of supporting
OpenCensus, now is the time to move to OpenTelemetry.

[announcement]: https://opentelemetry.io/blog/2023/sunsetting-opencensus/

The connector previously carried two parallel metric implementations — an
OpenCensus-based "public" path and an OpenTelemetry-based "system" path.

This release migrates the existing OpenCensus path to use OpenTelemetry.

## Setting up exporters

Previously you registered an OpenCensus stats exporter and view exporter.
Now you register an OpenTelemetry `MeterProvider` and (optionally) a
`TracerProvider`:

```go
import (
    "go.opentelemetry.io/otel"

    mexporter "github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/metric"
    texporter "github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/trace"
    sdkmetric "go.opentelemetry.io/otel/sdk/metric"
    sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func setupTelemetry(ctx context.Context, projectID string) (func(), error) {
    me, err := mexporter.New(mexporter.WithProjectID(projectID))
    if err != nil { return nil, err }
    mp := sdkmetric.NewMeterProvider(
        sdkmetric.WithReader(sdkmetric.NewPeriodicReader(me)),
    )
    otel.SetMeterProvider(mp)

    te, err := texporter.New(texporter.WithProjectID(projectID))
    if err != nil { return nil, err }
    tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(te))
    otel.SetTracerProvider(tp)

    return func() {
        _ = mp.Shutdown(ctx)
        _ = tp.Shutdown(ctx)
    }, nil
}
```

Any OpenTelemetry-compatible exporter works (Prometheus, OTLP, Jaeger, etc.)
— this example just shows the Google Cloud exporters because they replace
the most common previous setup.

The connector itself takes no telemetry configuration.

## Metric name and shape changes

The connector publishes metrics under the meter name
`cloud.google.com/go/alloydbconn`.

Each metric carries the resource attributes `project_id`, `location`,
`cluster_id`, `instance_id`, and `client_uid` so consumers can correlate to a
specific AlloyDB instance and `Dialer`.

What follows is the OpenCensus-based metric followed by the new OpenTelemetry
metric with some notes.

* `alloydbconn/dial_latency` (Distribution) -> `alloydbconn.dial_latencies` (Histogram)

Notes: Default OpenTelemetry histogram buckets are used. If you relied on the
OpenCensus buckets (`0, 5, 25, 100, 250, 500, 1000, 2000, 5000, 30000`),
register a [View][otel-view] with `metric.WithExplicitBucketBoundaries(...)` to
restore them.

[otel-view]: https://pkg.go.dev/go.opentelemetry.io/otel/sdk/metric#NewView

* `alloydbconn/open_connections` (LastValue) -> `alloydbconn.open_connections` (Int64 UpDownCounter)

Notes: Semantically equivalent. The new instrument increments on dial and
decrements on close.

* `alloydbconn/dial_failure_count` (Count) -> `alloydbconn.dial_count{status=…}`

Notes: The dial counter has been unified. Failures show up as
`status=user_error / cache_error / tcp_error / tls_error / mdx_error`;
successes as `status=success`. There is no longer a dedicated failure metric.

* `alloydbconn/refresh_success_count` (Count) -> `alloydbconn.refresh_count{status="success"}`

Notes: Filter by `status` to recover the old view.

* `alloydbconn/refresh_failure_count` (Count, with `alloydb_error_code` tag) -> `alloydbconn.refresh_count{status="failure", error_code=…}`

Notes: The error code is preserved. When the underlying error wraps a
`googleapi.Error`, its `Reason` codes are joined with `,` and attached as the
`error_code` attribute.

* `alloydbconn/bytes_sent` (Sum) -> `alloydbconn.bytes_sent_count` (Counter)

Notes: Same data, monotonic counter.

* `alloydbconn/bytes_received` (Sum) -> `alloydbconn.bytes_received_count` (Counter)

Notes: Same data, monotonic counter.

### Attribute changes

OpenCensus tagged the views with `alloydb_instance` and `alloydb_dialer_id`.
OpenTelemetry attaches a richer set of identifying attributes to every
metric:

- `project_id`, `location`, `cluster_id`, `instance_id` — replace
  `alloydb_instance`. Together they uniquely identify the AlloyDB instance.
- `client_uid` — replaces `alloydb_dialer_id`.
- `connector_type` (`go` or `auth_proxy`)
- `auth_type` (`iam` or `built_in`) on dial- and connection-related metrics
- `is_cache_hit` and `status` on `dial_count`
- `refresh_type` (`refresh_ahead` or `lazy`) on `refresh_count`
- `error_code` on `refresh_count` when `status="failure"`

Update PromQL/MQL queries and dashboards to filter by the new attribute
keys.

## Trace changes

Spans are now produced via the OpenTelemetry tracer named
`cloud.google.com/go/alloydbconn`. The span names are unchanged:

- `cloud.google.com/go/alloydbconn.Dial`
- `cloud.google.com/go/alloydbconn/internal.InstanceInfo`
- `cloud.google.com/go/alloydbconn/internal.Connect`
- `cloud.google.com/go/alloydbconn/internal.RefreshConnection`
- `cloud.google.com/go/alloydbconn/internal.FetchMetadata`
- `cloud.google.com/go/alloydbconn/internal.FetchEphemeralCert`

Span attribute keys were renamed to match OpenTelemetry conventions:

| Old (OpenCensus) | New (OpenTelemetry) |
|---|---|
| `/alloydb/instance` | `alloydb.instance` |
| `/alloydb/dialer_id` | `alloydb.dialer_id` |

Errors are recorded with `span.RecordError` and `span.SetStatus(codes.Error, …)`.

## Google Cloud Monitoring "system" metrics

The connector still pushes a parallel set of metrics directly to Google
Cloud Monitoring under the `alloydb.googleapis.com/client/connector/*`
prefix. These provide telemetry to improve the Connector's performance and
stability. They are **unrelated** to the public OpenTelemetry metrics described
above. They may be disabled with:

```go
alloydbconn.NewDialer(ctx, alloydbconn.WithOptOutOfBuiltInTelemetry())
```

Calling `WithOptOutOfBuiltInTelemetry` only disables the system metric
export to Google Cloud Monitoring. It does **not** affect the public
OpenTelemetry metrics — those flow through whichever `MeterProvider` you
have configured globally.
