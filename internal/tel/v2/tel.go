// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package tel provides telemetry into the connector's internal operations.
//
// Metrics recorded by this package are exported in two ways:
//
//  1. As Google Cloud Monitoring "system" metrics under the
//     alloydb.googleapis.com/client/connector/* prefix. This export uses a
//     dedicated MeterProvider configured with the Google Cloud Monitoring
//     exporter and is gated by Config.Enabled.
//
//  2. Through the global OpenTelemetry MeterProvider (otel.GetMeterProvider).
//     The metrics are recorded under the meter name
//     "cloud.google.com/go/alloydbconn" with metric names like
//     alloydbconn.dial_count, alloydbconn.dial_latencies, etc. Any exporter
//     registered on the global MeterProvider — for example a Prometheus or
//     OTLP exporter — will collect them. This path is always active so that
//     external callers can observe connector behavior even when the GCP
//     system metric export is disabled.
package tel

import (
	"context"
	"errors"
	"strings"
	"time"

	"cloud.google.com/go/alloydbconn/debug"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	"google.golang.org/api/googleapi"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	cmexporter "github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

const (
	// sysMeterName is the meter name used for the GCP system metric export.
	sysMeterName = "alloydb.googleapis.com/client/connector"
	// pubMeterName is the meter name used for the public OpenTelemetry
	// metric export. External exporters that scope by meter name should use
	// this value.
	pubMeterName = "cloud.google.com/go/alloydbconn"
	// pubMetricPrefix is prepended to all public metric names.
	pubMetricPrefix = "alloydbconn."

	monitoredResource = "alloydb.googleapis.com/InstanceClient"
	dialCount         = "dial_count"
	dialLatency       = "dial_latencies"
	openConnections   = "open_connections"
	bytesSent         = "bytes_sent_count"
	bytesReceived     = "bytes_received_count"
	refreshCount      = "refresh_count"

	// ResourceType is a special attribute that the exporter
	// transforms into the MonitoredResource field.
	ResourceType = "gcp.resource_type"
	// ProjectID specifies the instance's parent project.
	ProjectID = "project_id"
	// Location specifies the instances region (aka location).
	Location = "location"
	// Cluster specifies the cluster name.
	Cluster = "cluster_id"
	// Instance specifies the instance name.
	Instance = "instance_id"
	// ClientID is a unique ID specifying the instance of the
	// alloydbconn.Dialer.
	ClientID = "client_uid"

	// connectorType is one of go or auth-proxy
	connectorType = "connector_type"
	// authType is one of iam or built-in
	authType = "auth_type"
	// isCacheHit reports whether connection info was available in the cache
	isCacheHit = "is_cache_hit"
	// status indicates whether the dial attempt succeeded or not.
	status = "status"
	// refreshType indicates whether the cache is a refresh ahead cache or a
	// lazy cache.
	refreshType = "refresh_type"
	// errorCodeAttr identifies the AlloyDB Admin API error code (or codes)
	// associated with a failed refresh.
	errorCodeAttr = "error_code"

	// DialSuccess indicates the dial attempt succeeded.
	DialSuccess = "success"
	// DialUserError indicates the dial attempt failed due to a user mistake.
	DialUserError = "user_error"
	// DialCacheError indicates the dialer failed to retrieved the cached
	// connection info.
	DialCacheError = "cache_error"
	// DialTCPError indicates a TCP-level error.
	DialTCPError = "tcp_error"
	// DialTLSError indicates an error with the TLS connection.
	DialTLSError = "tls_error"
	// DialMDXError indicates an error with the metadata exchange.
	DialMDXError = "mdx_error"
	// RefreshSuccess indicates the refresh operation to retrieve new
	// connection info succeeded.
	RefreshSuccess = "success"
	// RefreshFailure indicates the refresh operation failed.
	RefreshFailure = "failure"
	// RefreshAheadType indicates the dialer is using a refresh ahead cache.
	RefreshAheadType = "refresh_ahead"
	// RefreshLazyType indicates the dialer is using a lazy cache.
	RefreshLazyType = "lazy"
)

// Config holds all the necessary information to configure a MetricRecorder.
type Config struct {
	// Enabled specifies whether the GCP system metric export should be
	// enabled. The public OpenTelemetry export through the global meter
	// provider is always active regardless of this value.
	Enabled bool
	// Version is the version of the alloydbconn.Dialer.
	Version string
	// ClientID uniquely identifies the instance of the alloydbconn.Dialer.
	ClientID string
	// ProjectID is the project ID of the AlloyDB instance.
	ProjectID string
	// Location is the location of the AlloyDB instance.
	Location string
	// Cluster is the name of the AlloyDB cluster.
	Cluster string
	// Instance is the name of the AlloyDB instance.
	Instance string
}

// MetricRecorder defines the interface for recording metrics related to the
// internal operations of alloydbconn.Dialer.
type MetricRecorder interface {
	Shutdown(context.Context) error
	RecordBytesRxCount(context.Context, int64, Attributes)
	RecordBytesTxCount(context.Context, int64, Attributes)
	RecordDialCount(context.Context, Attributes)
	RecordDialLatency(context.Context, int64, Attributes)
	RecordOpenConnection(context.Context, Attributes)
	RecordClosedConnection(context.Context, Attributes)
	RecordRefreshCount(context.Context, Attributes)
}

// DefaultExportInterval is the interval that the metric exporter runs. It
// should always be 60s. This value is exposed as a var to faciliate testing.
var DefaultExportInterval = 60 * time.Second

// instruments is a set of OpenTelemetry instruments used to record connector
// metrics.
type instruments struct {
	dialCount    metric.Int64Counter
	dialLatency  metric.Float64Histogram
	openConns    metric.Int64UpDownCounter
	bytesTx      metric.Int64Counter
	bytesRx      metric.Int64Counter
	refreshCount metric.Int64Counter
}

func newInstruments(m metric.Meter, prefix string) (*instruments, error) {
	dc, err := m.Int64Counter(prefix + dialCount)
	if err != nil {
		return nil, err
	}
	dl, err := m.Float64Histogram(prefix + dialLatency)
	if err != nil {
		return nil, err
	}
	oc, err := m.Int64UpDownCounter(prefix + openConnections)
	if err != nil {
		return nil, err
	}
	bt, err := m.Int64Counter(prefix + bytesSent)
	if err != nil {
		return nil, err
	}
	br, err := m.Int64Counter(prefix + bytesReceived)
	if err != nil {
		return nil, err
	}
	rc, err := m.Int64Counter(prefix + refreshCount)
	if err != nil {
		return nil, err
	}
	return &instruments{
		dialCount:    dc,
		dialLatency:  dl,
		openConns:    oc,
		bytesTx:      bt,
		bytesRx:      br,
		refreshCount: rc,
	}, nil
}

// NewMetricRecorder creates a MetricRecorder. The returned recorder always
// records into the global OpenTelemetry MeterProvider so that any registered
// exporters can collect connector metrics. Additionally, when cfg.Enabled is
// true and a non-nil monitoring client is supplied, the recorder also exports
// metrics directly to Google Cloud Monitoring under the
// alloydb.googleapis.com/client/connector/* prefix.
func NewMetricRecorder(ctx context.Context, l debug.ContextLogger, cl *monitoring.MetricClient, cfg Config) MetricRecorder {
	r := &metricRecorder{
		dialerID: cfg.ClientID,
		// pubAttrs are added to every public metric so that external
		// observers can correlate connector metrics with a specific
		// instance, since they typically do not see the GCP-style monitored
		// resource.
		pubAttrs: attribute.NewSet(
			attribute.String(ProjectID, cfg.ProjectID),
			attribute.String(Location, cfg.Location),
			attribute.String(Cluster, cfg.Cluster),
			attribute.String(Instance, cfg.Instance),
			attribute.String(ClientID, cfg.ClientID),
		),
	}

	// Always set up the public OpenTelemetry instruments backed by the
	// process-global MeterProvider. Failures here are logged but otherwise
	// ignored: connector functionality must not depend on telemetry.
	pubMeter := otel.GetMeterProvider().Meter(
		pubMeterName, metric.WithInstrumentationVersion(cfg.Version),
	)
	pubInst, err := newInstruments(pubMeter, pubMetricPrefix)
	if err != nil {
		l.Debugf(ctx, "public OpenTelemetry instruments failed to initialize: %v", err)
	} else {
		r.pub = pubInst
	}

	// Optionally set up the GCP system metric exporter.
	if !cfg.Enabled {
		l.Debugf(ctx, "disabling built-in (GCP system) metrics")
		return r
	}
	if cl == nil {
		l.Debugf(ctx, "metric client is nil, disabling built-in (GCP system) metrics")
		return r
	}
	eopts := []cmexporter.Option{
		cmexporter.WithCreateServiceTimeSeries(),
		cmexporter.WithProjectID(cfg.ProjectID),
		cmexporter.WithMonitoringClient(cl),
		cmexporter.WithMetricDescriptorTypeFormatter(func(m metricdata.Metrics) string {
			return "alloydb.googleapis.com/client/connector/" + m.Name
		}),
		cmexporter.WithMonitoredResourceDescription(monitoredResource, []string{
			ProjectID, Location, Cluster, Instance, ClientID,
		}),
		// Don't add any resource attributes to metrics as metric labels.
		cmexporter.WithFilteredResourceAttributes(cmexporter.NoAttributes),
	}
	exp, err := cmexporter.New(eopts...)
	if err != nil {
		l.Debugf(ctx, "built-in metrics exporter failed to initialize: %v", err)
		return r
	}

	res := resource.NewWithAttributes(monitoredResource,
		attribute.String(ResourceType, monitoredResource),
		attribute.String(ProjectID, cfg.ProjectID),
		attribute.String(Location, cfg.Location),
		attribute.String(Cluster, cfg.Cluster),
		attribute.String(Instance, cfg.Instance),
		attribute.String(ClientID, cfg.ClientID),
	)
	p := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(
			exp,
			// The periodic reader runs every 60 seconds by default, but set
			// the value anyway to be defensive.
			sdkmetric.WithInterval(DefaultExportInterval),
		)),
		sdkmetric.WithResource(res),
	)
	sysMeter := p.Meter(sysMeterName, metric.WithInstrumentationVersion(cfg.Version))
	sysInst, err := newInstruments(sysMeter, "")
	if err != nil {
		_ = exp.Shutdown(ctx)
		l.Debugf(ctx, "built-in metrics exporter failed to initialize instruments: %v", err)
		return r
	}
	r.sysExporter = exp
	r.sysProvider = p
	r.sys = sysInst
	return r
}

// metricRecorder holds the various counters that track internal operations.
type metricRecorder struct {
	// sys/sysProvider/sysExporter implement the GCP system metric export.
	// They may be nil if the export is disabled.
	sys         *instruments
	sysProvider *sdkmetric.MeterProvider
	sysExporter sdkmetric.Exporter

	// pub holds the instruments backed by the global OpenTelemetry
	// MeterProvider. Records here flow to whatever external exporters the
	// caller has registered. Nil only if instrument creation failed.
	pub      *instruments
	pubAttrs attribute.Set

	dialerID string
}

// Shutdown should be called when the MetricRecorder is no longer needed.
func (m *metricRecorder) Shutdown(ctx context.Context) error {
	if m.sysProvider == nil {
		return nil
	}
	// Shutdown only the provider. The provider will shutdown the exporter as
	// part of its own shutdown, i.e., provider shuts down the reader, the
	// reader shuts down the exporter. So one shutdown call here is enough.
	return m.sysProvider.Shutdown(ctx)
}

func connectorTypeValue(userAgent string) string {
	if strings.Contains(userAgent, "auth-proxy") {
		return "auth_proxy"
	}
	return "go"
}

func authTypeValue(iamAuthn bool) string {
	if iamAuthn {
		return "iam"
	}
	return "built_in"
}

// Attributes holds all the various pieces of metadata to attach to a metric.
type Attributes struct {
	// IAMAuthN specifies whether IAM authentication is enabled.
	IAMAuthN bool
	// UserAgent is the full user-agent of the alloydbconn.Dialer.
	UserAgent string
	// CacheHit specifies whether connection info was present in the cache.
	CacheHit bool
	// DialStatus specifies the result of the dial attempt.
	DialStatus string
	// RefreshStatus specifies the result of the refresh operation.
	RefreshStatus string
	// RefreshType specifies the type of cache in use (e.g., refresh ahead or
	// lazy).
	RefreshType string
	// RefreshError, if non-nil, is the error returned from a failed refresh.
	// When the error wraps a googleapi.Error, its reason codes are attached
	// as the error_code attribute on the public refresh_count metric.
	RefreshError error
}

// pubAttrs returns the per-record attribute set for the public meter,
// combining the recorder's instance attributes with the supplied per-call
// attributes.
func (m *metricRecorder) addPubAttrs(extra ...attribute.KeyValue) metric.MeasurementOption {
	all := make([]attribute.KeyValue, 0, m.pubAttrs.Len()+len(extra))
	for _, kv := range m.pubAttrs.ToSlice() {
		all = append(all, kv)
	}
	all = append(all, extra...)
	return metric.WithAttributes(all...)
}

// RecordBytesRxCount records the number of bytes received for a particular
// instance.
func (m *metricRecorder) RecordBytesRxCount(ctx context.Context, bytes int64, a Attributes) {
	attrs := []attribute.KeyValue{
		attribute.String(connectorType, connectorTypeValue(a.UserAgent)),
	}
	if m.sys != nil {
		m.sys.bytesRx.Add(ctx, bytes, metric.WithAttributes(attrs...))
	}
	if m.pub != nil {
		m.pub.bytesRx.Add(ctx, bytes, m.addPubAttrs(attrs...))
	}
}

// RecordBytesTxCount records the number of bytes send for a paritcular
// instance.
func (m *metricRecorder) RecordBytesTxCount(ctx context.Context, bytes int64, a Attributes) {
	attrs := []attribute.KeyValue{
		attribute.String(connectorType, connectorTypeValue(a.UserAgent)),
	}
	if m.sys != nil {
		m.sys.bytesTx.Add(ctx, bytes, metric.WithAttributes(attrs...))
	}
	if m.pub != nil {
		m.pub.bytesTx.Add(ctx, bytes, m.addPubAttrs(attrs...))
	}
}

// RecordDialCount records increments the number of dial attempts.
func (m *metricRecorder) RecordDialCount(ctx context.Context, a Attributes) {
	attrs := []attribute.KeyValue{
		attribute.String(connectorType, connectorTypeValue(a.UserAgent)),
		attribute.String(authType, authTypeValue(a.IAMAuthN)),
		attribute.Bool(isCacheHit, a.CacheHit),
		attribute.String(status, a.DialStatus),
	}
	if m.sys != nil {
		m.sys.dialCount.Add(ctx, 1, metric.WithAttributes(attrs...))
	}
	if m.pub != nil {
		m.pub.dialCount.Add(ctx, 1, m.addPubAttrs(attrs...))
	}
}

// RecordDialLatency records a latency measurement for a particular dial
// attempt.
func (m *metricRecorder) RecordDialLatency(ctx context.Context, latencyMS int64, a Attributes) {
	attrs := []attribute.KeyValue{
		attribute.String(connectorType, connectorTypeValue(a.UserAgent)),
	}
	if m.sys != nil {
		m.sys.dialLatency.Record(ctx, float64(latencyMS), metric.WithAttributes(attrs...))
	}
	if m.pub != nil {
		m.pub.dialLatency.Record(ctx, float64(latencyMS), m.addPubAttrs(attrs...))
	}
}

// RecordOpenConnection increments the number of open connections.
func (m *metricRecorder) RecordOpenConnection(ctx context.Context, a Attributes) {
	attrs := []attribute.KeyValue{
		attribute.String(connectorType, connectorTypeValue(a.UserAgent)),
		attribute.String(authType, authTypeValue(a.IAMAuthN)),
	}
	if m.sys != nil {
		m.sys.openConns.Add(ctx, 1, metric.WithAttributes(attrs...))
	}
	if m.pub != nil {
		m.pub.openConns.Add(ctx, 1, m.addPubAttrs(attrs...))
	}
}

// RecordClosedConnection decrements the number of open connections.
func (m *metricRecorder) RecordClosedConnection(ctx context.Context, a Attributes) {
	attrs := []attribute.KeyValue{
		attribute.String(connectorType, connectorTypeValue(a.UserAgent)),
		attribute.String(authType, authTypeValue(a.IAMAuthN)),
	}
	if m.sys != nil {
		m.sys.openConns.Add(ctx, -1, metric.WithAttributes(attrs...))
	}
	if m.pub != nil {
		m.pub.openConns.Add(ctx, -1, m.addPubAttrs(attrs...))
	}
}

// RecordRefreshCount records the result of a refresh operation. When the
// refresh failed and the error wraps a googleapi.Error, the API error
// reason(s) are attached as an error_code attribute on the public metric.
// This preserves a useful detail that was previously available only via the
// OpenCensus refresh_failure_count metric.
func (m *metricRecorder) RecordRefreshCount(ctx context.Context, a Attributes) {
	sysAttrs := []attribute.KeyValue{
		attribute.String(connectorType, connectorTypeValue(a.UserAgent)),
		attribute.String(status, a.RefreshStatus),
		attribute.String(refreshType, a.RefreshType),
	}
	if m.sys != nil {
		m.sys.refreshCount.Add(ctx, 1, metric.WithAttributes(sysAttrs...))
	}
	if m.pub != nil {
		pubExtra := sysAttrs
		if a.RefreshStatus == RefreshFailure {
			if c := apiErrorCode(a.RefreshError); c != "" {
				pubExtra = append(pubExtra, attribute.String(errorCodeAttr, c))
			}
		}
		m.pub.refreshCount.Add(ctx, 1, m.addPubAttrs(pubExtra...))
	}
}

// apiErrorCode returns an error code as given from the AlloyDB Admin API,
// provided the error wraps a googleapi.Error type. If multiple error codes
// are returned from the API, then a comma-separated string of all codes is
// returned.
func apiErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var apiErr *googleapi.Error
	if !errors.As(err, &apiErr) {
		return ""
	}
	var codes []string
	for _, e := range apiErr.Errors {
		codes = append(codes, e.Reason)
	}
	return strings.Join(codes, ",")
}

// NullMetricRecorder implements the MetricRecorder interface with no-ops. It
// is useful for disabling the built-in metrics.
type NullMetricRecorder struct{}

// Shutdown is a no-op.
func (NullMetricRecorder) Shutdown(context.Context) error { return nil }

// RecordBytesRxCount is a no-op.
func (NullMetricRecorder) RecordBytesRxCount(context.Context, int64, Attributes) {}

// RecordBytesTxCount is a no-op.
func (NullMetricRecorder) RecordBytesTxCount(context.Context, int64, Attributes) {}

// RecordDialCount is a no-op.
func (NullMetricRecorder) RecordDialCount(context.Context, Attributes) {}

// RecordDialLatency is a no-op.
func (NullMetricRecorder) RecordDialLatency(context.Context, int64, Attributes) {}

// RecordOpenConnection is a no-op.
func (NullMetricRecorder) RecordOpenConnection(context.Context, Attributes) {}

// RecordClosedConnection is a no-op.
func (NullMetricRecorder) RecordClosedConnection(context.Context, Attributes) {}

// RecordRefreshCount is a no-op.
func (NullMetricRecorder) RecordRefreshCount(context.Context, Attributes) {}
