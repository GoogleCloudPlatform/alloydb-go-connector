# Connector metrics

The connector emits the following OpenTelemetry metrics through Go's global
`MeterProvider`. Applications that configure that provider before creating a
Dialer automatically receive these metrics through their configured exporter.
The connector does not configure, flush, or shut down the global provider.

The instrumentation scope is `cloud.google.com/go/alloydbconn`. Metric names,
types, units, and attributes in this document are the public v1 contract.

| Metric | Type | Unit | Description |
| --- | --- | --- | --- |
| `alloydb.connector.dial.count` | Counter | `{dial}` | Dial attempts. |
| `alloydb.connector.dial.duration` | Histogram | `ms` | Duration of successful dial operations. |
| `alloydb.connector.connections.open` | UpDownCounter | `{connection}` | Open connector connections. |
| `alloydb.connector.bytes.sent` | Counter | `By` | Bytes sent through connector connections. |
| `alloydb.connector.bytes.received` | Counter | `By` | Bytes received through connector connections. |
| `alloydb.connector.refresh.count` | Counter | `{refresh}` | Connection-information refresh operations. |

Every metric has these target attributes:

| Attribute | Description |
| --- | --- |
| `alloydb.project_id` | AlloyDB instance project ID. |
| `alloydb.location` | AlloyDB instance location. |
| `alloydb.cluster_id` | AlloyDB cluster ID. |
| `alloydb.instance_id` | AlloyDB instance ID. |

Additional attributes are emitted only where relevant:

| Attribute | Metrics | Values |
| --- | --- | --- |
| `alloydb.connector.type` | All metrics | `go`, `auth_proxy` |
| `alloydb.auth.type` | Dial count, open connections | `built_in`, `iam` |
| `alloydb.cache.hit` | Dial count | `true`, `false` |
| `alloydb.status` | Dial count, refresh count | Dial: `success`, `user_error`, `cache_error`, `tcp_error`, `tls_error`, `mdx_error`; refresh: `success`, `failure` |
| `alloydb.refresh.type` | Refresh count | `refresh_ahead`, `lazy` |

The AlloyDB target attributes are bounded by the instances an application can
connect to. The connector deliberately does not export its per-Dialer UUID,
which would create a new time series after every application restart.

## Exporting metrics

Configure OpenTelemetry once during application startup, before creating the
Dialer. For Prometheus, install a Prometheus metric reader on the global
provider and serve the reader's HTTP handler from the application. For OTLP,
install an OTLP metric exporter and periodic reader, then send metrics to an
OpenTelemetry Collector or another OTLP-compatible backend.

For example, a Prometheus setup can register a reader and expose the default
Prometheus registry:

```go
exporter, err := prometheus.New()
if err != nil {
	return err
}
provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter))
otel.SetMeterProvider(provider)
http.Handle("/metrics", promhttp.Handler())
defer provider.Shutdown(ctx)
```

An OTLP/gRPC setup can send the same metrics to a Collector:

```go
exporter, err := otlpmetricgrpc.New(ctx)
if err != nil {
	return err
}
provider := sdkmetric.NewMeterProvider(
	sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)),
)
otel.SetMeterProvider(provider)
defer provider.Shutdown(ctx)
```

The Prometheus example uses `go.opentelemetry.io/otel/exporters/prometheus` and
`github.com/prometheus/client_golang/prometheus/promhttp`; the OTLP example
uses `go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc`.

The connector deliberately does not choose an exporter, open an HTTP listener,
or own provider shutdown. Use SDK Views or Collector processors to filter or
rename metrics for a particular backend.

`WithOptOutOfBuiltInTelemetry` disables both this global-OpenTelemetry path and
the connector's built-in Cloud Monitoring exporter. It does not disable the
legacy OpenCensus metrics described below.

## Legacy OpenCensus metrics

The connector continues to register and record its existing OpenCensus views
for compatibility. Configure an OpenCensus exporter separately if those legacy
metrics are needed. New external observability integrations should use the
OpenTelemetry metrics documented above.
