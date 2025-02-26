// Copyright 2024 Google LLC
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
package tel

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	"google.golang.org/api/option"

	cmexporter "github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

const (
	meterName         = "alloydb.googleapis.com/client/connector"
	monitoredResource = "alloydb.googleapis.com/InstanceClient"
	dialCount         = "dial_count"
	dialLatency       = "dial_latencies"
	openConnections   = "open_connections"
	bytesSent         = "bytes_sent_count"
	bytesReceived     = "bytes_received_count"
	refreshCount      = "refresh_count"
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
	// DialSuccess indicates the dial attempt succeeded.
	DialSuccess = "success"
	// DialUserError indicates the dial attempt failed due to a user mistake.
	DialUserError = "user-error"
	// DialCacheError indicates the dialer failed to retrieved the cached
	// connection info.
	DialCacheError = "cache-error"
	// DialTCPErrordialer indicates a TCP-level error.
	DialTCPError = "tcp-error"
	// DialTLSError indicates an error with the TLS connection.
	DialTLSError = "tls-error"
	// DialMDXError indicates an error with the metadata exchange.
	DialMDXError = "mdx-error"
	// RefreshSuccess indicates the refresh operation to retrieve new
	// connection info succeeded.
	RefreshSuccess = "success"
	// RefreshFailure indicates the refresh operation failed.
	RefreshFailure = "failure"
	// RefreshAheadType indicates the dialer is using a refresh ahead cache.
	RefreshAheadType = "refresh-ahead"
	// RefreshLazyType indicates the dialer is using a lazy cache.
	RefreshLazyType = "lazy"
)

// MetricRecorder holds the various counters that track internal operations.
type MetricRecorder struct {
	exporter      sdkmetric.Exporter
	provider      *sdkmetric.MeterProvider
	dialerID      string
	mDialCount    metric.Int64Counter
	mDialLatency  metric.Float64Histogram
	mOpenConns    metric.Int64UpDownCounter
	mBytesTx      metric.Int64Counter
	mBytesRx      metric.Int64Counter
	mRefreshCount metric.Int64Counter
}

// Config holds all the necessary information to configure a MetricRecorder.
type Config struct {
	Enabled   bool
	Version   string
	ClientID  string
	ProjectID string
	Location  string
	Cluster   string
	Instance  string
}

// NullExporter is an OpenTelemetry sdkmetric.Exporter that does nothing.
type NullExporter struct{}

func (NullExporter) Temporality(ik sdkmetric.InstrumentKind) metricdata.Temporality {
	return sdkmetric.DefaultTemporalitySelector(ik)
}

func (NullExporter) Aggregation(ik sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(ik)
}

func (NullExporter) Export(context.Context, *metricdata.ResourceMetrics) error {
	return nil
}

func (NullExporter) ForceFlush(context.Context) error { return nil }
func (NullExporter) Shutdown(context.Context) error   { return nil }

// NewMetricRecorder creates a MetricRecorder with a 1:1 correspondance to
// an alloydbconn.Dialer.
func NewMetricRecorder(ctx context.Context, cfg Config, opts ...option.ClientOption) (*MetricRecorder, error) {
	var (
		exp sdkmetric.Exporter = NullExporter{}
		err error
	)
	if cfg.Enabled {
		opts := []cmexporter.Option{
			cmexporter.WithCreateServiceTimeSeries(),
			cmexporter.WithProjectID(cfg.ProjectID),
			cmexporter.WithMonitoringClientOptions(opts...),
			cmexporter.WithMetricDescriptorTypeFormatter(func(m metricdata.Metrics) string {
				return "alloydb.googleapis.com/client/connector/" + m.Name
			}),
			cmexporter.WithMonitoredResourceDescription(monitoredResource, []string{
				ProjectID, Location, Cluster, Instance, ClientID,
			}),
		}
		exp, err = cmexporter.New(opts...)
		if err != nil {
			return nil, err
		}
	}

	res := resource.NewWithAttributes(monitoredResource,
		attribute.String("gcp.resource_type", monitoredResource),
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
			sdkmetric.WithInterval(60*time.Second),
		)),
		sdkmetric.WithResource(res),
	)
	m := p.Meter(meterName, metric.WithInstrumentationVersion(cfg.Version))

	mDialCount, err := m.Int64Counter(dialCount)
	if err != nil {
		return nil, errors.Join(err, exp.Shutdown(context.Background()))
	}
	mDialLatency, err := m.Float64Histogram(dialLatency)
	if err != nil {
		return nil, errors.Join(err, exp.Shutdown(context.Background()))
	}
	mOpenConns, err := m.Int64UpDownCounter(openConnections)
	if err != nil {
		return nil, errors.Join(err, exp.Shutdown(context.Background()))
	}
	mBytesTx, err := m.Int64Counter(bytesSent)
	if err != nil {
		return nil, errors.Join(err, exp.Shutdown(context.Background()))
	}
	mBytesRx, err := m.Int64Counter(bytesReceived)
	if err != nil {
		return nil, errors.Join(err, exp.Shutdown(context.Background()))
	}
	mRefreshCount, err := m.Int64Counter(refreshCount)
	if err != nil {
		return nil, errors.Join(err, exp.Shutdown(context.Background()))
	}
	mr := &MetricRecorder{
		exporter:      exp,
		provider:      p,
		dialerID:      cfg.ClientID,
		mDialCount:    mDialCount,
		mDialLatency:  mDialLatency,
		mOpenConns:    mOpenConns,
		mBytesTx:      mBytesTx,
		mBytesRx:      mBytesRx,
		mRefreshCount: mRefreshCount,
	}
	return mr, nil
}

// Shutdown should be called when the MetricRecorder is no longer needed.
func (m *MetricRecorder) Shutdown(ctx context.Context) error {
	return errors.Join(m.exporter.Shutdown(ctx), m.provider.Shutdown(ctx))
}

func connectorTypeValue(userAgent string) string {
	if strings.Contains("auth-proxy", userAgent) {
		return "auth_proxy"
	}
	return "go"
}

func authTypeValue(iamAuthn bool) string {
	if iamAuthn {
		return "iam"
	}
	return "built-in" // TODO update to "built-in" from "native"
}

// Attributes holds all the various pieces of metadata to attach to a metric.
type Attributes struct {
	IAMAuthN      bool
	UserAgent     string
	CacheHit      bool
	DialStatus    string
	RefreshStatus string
	RefreshType   string
}

// RecordBytesRxCount records the number of bytes received for a particular
// instance.
func (m *MetricRecorder) RecordBytesRxCount(ctx context.Context, bytes int64, a Attributes) {
	m.mBytesRx.Add(ctx, bytes,
		metric.WithAttributeSet(attribute.NewSet(
			attribute.String(connectorType, connectorTypeValue(a.UserAgent)),
		)),
	)
}

// RecordBytesTxCount records the number of bytes send for a paritcular
// instance.
func (m *MetricRecorder) RecordBytesTxCount(ctx context.Context, bytes int64, a Attributes) {
	m.mBytesTx.Add(ctx, bytes,
		metric.WithAttributeSet(attribute.NewSet(
			attribute.String(connectorType, connectorTypeValue(a.UserAgent)),
		)),
	)
}

// RecordDialCount records increments the number of dial attempts.
func (m *MetricRecorder) RecordDialCount(ctx context.Context, a Attributes) {
	m.mDialCount.Add(ctx, 1,
		metric.WithAttributeSet(attribute.NewSet(
			attribute.String(connectorType, connectorTypeValue(a.UserAgent)),
			attribute.String(authType, authTypeValue(a.IAMAuthN)),
			attribute.Bool(isCacheHit, a.CacheHit),
			attribute.String(status, a.DialStatus)),
		))
}

// RecordDialLatency records a latency measurement for a particular dial
// attempt.
func (m *MetricRecorder) RecordDialLatency(ctx context.Context, latencyMS int64, a Attributes) {
	m.mDialLatency.Record(ctx, float64(latencyMS),
		metric.WithAttributeSet(attribute.NewSet(
			attribute.String(connectorType, connectorTypeValue(a.UserAgent)),
		)),
	)
}

// RecordOpenConnection increments the number of open connections.
func (m *MetricRecorder) RecordOpenConnection(ctx context.Context, a Attributes) {
	m.mOpenConns.Add(ctx, 1,
		metric.WithAttributeSet(attribute.NewSet(
			attribute.String(connectorType, connectorTypeValue(a.UserAgent)),
			attribute.String(authType, authTypeValue(a.IAMAuthN)),
		)),
	)
}

// RecordClosedConnection decrements the number of open connections.
func (m *MetricRecorder) RecordClosedConnection(ctx context.Context, a Attributes) {
	m.mOpenConns.Add(ctx, -1,
		metric.WithAttributeSet(attribute.NewSet(
			attribute.String(connectorType, connectorTypeValue(a.UserAgent)),
			attribute.String(authType, authTypeValue(a.IAMAuthN)),
		)),
	)
}

// RecordRefreshCount records the result of a refresh operation.
func (m *MetricRecorder) RecordRefreshCount(ctx context.Context, a Attributes) {
	m.mRefreshCount.Add(ctx, 1,
		metric.WithAttributeSet(attribute.NewSet(
			attribute.String(connectorType, connectorTypeValue(a.UserAgent)),
			attribute.String(status, a.RefreshStatus),
			attribute.String(refreshType, a.RefreshType),
		)),
	)
}
