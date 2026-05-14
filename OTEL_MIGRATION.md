# Migration Guide: OpenCensus to OpenTelemetry

The AlloyDB Go connector now produces metrics and traces through
[OpenTelemetry][otel] using the process-global `MeterProvider` and
`TracerProvider`.

[otel]: https://opentelemetry.io/

To make the move staged rather than all-at-once, this release **keeps emitting
the original OpenCensus metrics through OpenCensus** alongside the new
OpenTelemetry metrics. Existing OpenCensus-based exporters (for example
`contrib.go.opencensus.io/exporter/stackdriver`) continue to work without any
changes. The OpenCensus emission is deprecated and will be removed in the
release after the next.

## TL;DR

- Existing OpenCensus pipelines keep working in this release.
- To start consuming the new OpenTelemetry metrics, register an
  OpenTelemetry `MeterProvider` (and optionally a `TracerProvider`) at
  process startup. The connector picks them up automatically.
- When your dashboards and alerts are migrated to the new metric names,
  remove the OpenCensus exporter setup before upgrading past the deprecation
  release.

## Why the change?

OpenCensus has been deprecated in favor of OpenTelemetry. See the [2023
announcement][announcement]. After three additional years of supporting
OpenCensus, now is the time to move to OpenTelemetry.

[announcement]: https://opentelemetry.io/blog/2023/sunsetting-opencensus/

## Deprecation timeline

| Release | OpenCensus metrics | OpenTelemetry metrics |
|---|---|---|
| Current | Emitted (deprecated) | Emitted |
| Next    | Emitted (deprecated) | Emitted |
| After   | **Removed**          | Emitted |

Plan to have dashboards and alerts on the new metric names before
upgrading past the second release listed above.

## Setting up the OpenTelemetry pipeline

Register an OpenTelemetry `MeterProvider` and (optionally) a
`TracerProvider`. The connector takes no telemetry configuration of its
own — it reads `otel.GetMeterProvider()` and `otel.GetTracerProvider()`.

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

Any OpenTelemetry-compatible exporter works (Prometheus, OTLP, Jaeger,
etc.). The Google Cloud exporters are shown here because they replace the
most common previous setup.

## Metric name mapping

The new OpenTelemetry metrics are published under the meter name
`cloud.google.com/go/alloydbconn`. Each metric carries the resource
attributes `project_id`, `location`, `cluster_id`, `instance_id`, and
`client_uid`.

| OpenCensus (kept this release) | OpenTelemetry (new) |
|---|---|
| `alloydbconn/dial_latency` (Distribution) | `alloydbconn.dial_latencies` (Histogram) |
| `alloydbconn/open_connections` (LastValue) | `alloydbconn.open_connections` (Int64 UpDownCounter) |
| `alloydbconn/dial_failure_count` (Count) | `alloydbconn.dial_count{status!="success"}` |
| `alloydbconn/refresh_success_count` (Count) | `alloydbconn.refresh_count{status="success"}` |
| `alloydbconn/refresh_failure_count` (Count, with `alloydb_error_code`) | `alloydbconn.refresh_count{status="failure", error_code=…}` |
| `alloydbconn/bytes_sent` (Sum) | `alloydbconn.bytes_sent_count` (Counter) |
| `alloydbconn/bytes_received` (Sum) | `alloydbconn.bytes_received_count` (Counter) |

Notes:

- The OpenTelemetry `dial_latencies` histogram uses explicit bucket
  boundaries `0, 5, 25, 100, 250, 500, 1000, 2000, 5000, 30000` (ms),
  matching the original OpenCensus distribution. Only successful dial
  attempts are recorded, matching the existing OpenCensus behavior; if you
  need a count of failed dials, use `dial_count` filtered by `status`.
- The dial counter has been unified into a single `dial_count` that records
  every dial attempt. Failures show up as
  `status=user_error / cache_error / tcp_error / tls_error / mdx_error` and
  successes as `status=success`. The previous OpenCensus
  `dial_failure_count` is equivalent to `sum(dial_count{status!="success"})`.

### Attribute mapping

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
- `error_code` on `refresh_count` when `status="failure"`. The value is taken
  from the `Reason` field of the `googleapi.Error` returned by the AlloyDB
  Admin API; when the API returns multiple sub-errors, their reasons are
  joined into a single comma-separated string. The attribute is omitted if
  the refresh failed for a reason that does not wrap a `googleapi.Error`
  (for example a network or context-cancellation error).

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

Traces do **not** have a dual-emission compatibility period — there is no
OpenCensus trace fallback. If you were exporting OpenCensus spans, register
an OpenTelemetry `TracerProvider` in this release.

## Google Cloud Monitoring "system" metrics

The connector still pushes a parallel set of metrics directly to Google
Cloud Monitoring under the `alloydb.googleapis.com/client/connector/*`
prefix. These provide telemetry to improve the Connector's performance and
stability. They are **unrelated** to the public metrics described above.
They may be disabled with:

```go
alloydbconn.NewDialer(ctx, alloydbconn.WithOptOutOfBuiltInTelemetry())
```

Calling `WithOptOutOfBuiltInTelemetry` only disables the system metric
export to Google Cloud Monitoring. It does **not** affect the public
OpenTelemetry or OpenCensus metrics — those flow through whichever
`MeterProvider` (or OpenCensus view/exporter pipeline) you have configured.
