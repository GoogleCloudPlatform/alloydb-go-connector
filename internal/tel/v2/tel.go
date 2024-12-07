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
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	mexporter "github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

const (
	meterName = "cloud.google.com/go/alloydbconn"

	dialCount       = "dial_count"
	dialLatency     = "dial_latencies"
	openConnections = "open_connections"
	bytesSent       = "bytes_sent"
	bytesReceived   = "bytes_received"
	refreshCount    = "refresh_count"

	// attribute labels
	projectID     = "project"
	location      = "location"
	cluster       = "cluster_id"
	instance      = "instance_id"
	instanceUID   = "_uid"
	connectorType = "connector_type"
	authType      = "auth_type"
	isCacheHit    = "is_cache_hit"
	status        = "status"
	refreshType   = "refresh_type"

	DialSuccess    = "success"
	DialUserError  = "user-error"
	DialCacheError = "cache-error"
	DialTCPError   = "tcp-error"
	DialTLSError   = "tls-error"
	DialMDXError   = "mdx-error"
	RefreshSuccess = "success"
	RefreshFailure = "failure"
	RefreshAhead   = "refresh-ahead"
	RefreshLazy    = "lazy"
)

var (
	mDialCount    metric.Int64Counter
	mDialLatency  metric.Float64Histogram
	mOpenConns    metric.Int64UpDownCounter
	mBytesTx      metric.Int64Counter
	mBytesRx      metric.Int64Counter
	mRefreshCount metric.Int64Counter
)

// TODO use the same client options as dialer in the exporter
func InitMetrics(unused string) (func(context.Context) error, error) {
	opts := []mexporter.Option{
		mexporter.WithProjectID("enocom-experiments-304623"), // TODO don't hardcode this
		mexporter.WithDisableCreateMetricDescriptors(),
		mexporter.WithMetricDescriptorTypeFormatter(func(desc metricdata.Metrics) string {
			return fmt.Sprintf("alloydb.googleapis.com/internal/connector/%s", desc.Name)
		}),
		// mexporter.WithFilteredResourceAttributes(func(kv attribute.KeyValue) bool {
		// 	return kv.Key == projectID ||
		// 		kv.Key == location ||
		// 		kv.Key == cluster ||
		// 		kv.Key == instance ||
		// 		kv.Key == connectorType ||
		// 		kv.Key == authType ||
		// 		kv.Key == isCacheHit ||
		// 		kv.Key == status ||
		// 		kv.Key == refreshType ||
                // kv.Key == "node_id" ||
                // kv.Key == "instance_type"
		// }),
	}
	exporter, err := mexporter.New(opts...)
	if err != nil {
		return func(context.Context) error { return nil }, err
	}

	p := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter,
			sdkmetric.WithInterval(3*time.Second), // TODO use the default
		)),
	)
	m := p.Meter(meterName, metric.WithInstrumentationVersion("TODO alloydbconnversion"))

	mDialCount, err = m.Int64Counter(dialCount,
		metric.WithDescription("The number of dial invocations"),
		// TODO fill out all metadata for metrics
	)
	if err != nil {
		return p.Shutdown, err
	}
	mDialLatency, err = m.Float64Histogram(dialLatency)
	if err != nil {
		return p.Shutdown, err
	}
	mOpenConns, err = m.Int64UpDownCounter(openConnections)
	if err != nil {
		return p.Shutdown, err
	}
	mBytesTx, err = m.Int64Counter(bytesSent)
	if err != nil {
		return p.Shutdown, err
	}
	mBytesRx, err = m.Int64Counter(bytesReceived)
	if err != nil {
		return p.Shutdown, err
	}
	mRefreshCount, err = m.Int64Counter(refreshCount)
	if err != nil {
		return p.Shutdown, err
	}
	return p.Shutdown, nil
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
	return "native" // TODO this should be built-in
}

type Attributes struct {
	IAMAuthN  bool
	UserAgent string

	CacheHit   bool
	DialStatus string

	RefreshStatus string
	RefreshType   string
}

func RecordDialCount(ctx context.Context, a Attributes) {
	mDialCount.Add(ctx, 1,
		metric.WithAttributeSet(attribute.NewSet(
			attribute.String(projectID, "enocom-experiments-304623"),
			attribute.String(location, "us-central1"),
			attribute.String(cluster, "enocom-cluster"),

			attribute.String(instance, "enocom-primary"),
			attribute.String("instance_type", "unknown"),

			attribute.String("node_id", "unknown"),
			attribute.String("service", "unknown"),
			attribute.String("_uid", "unknown"),

			attribute.String(connectorType, connectorTypeValue(a.UserAgent)),
			attribute.String(authType, authTypeValue(a.IAMAuthN)),
			attribute.Bool(isCacheHit, a.CacheHit),
			attribute.String(status, a.DialStatus)),
		))

}

func RecordDialLatency(ctx context.Context, latencyMS int64, a Attributes) {
	// mDialLatency.Record(ctx, float64(latencyMS),
	// 	metric.WithAttributeSet(attribute.NewSet(
	// 		attribute.String("project_id", "enocom-experiments-304623"),
	// 		attribute.String("location", "us-central1"),
	// 		attribute.String("cluster", "enocom-cluster"),
	// 		attribute.String("instance", "enocom-primary"),

	// 		attribute.String(connectorType, connectorTypeValue(a.UserAgent)),
	// 	)),
	// )
}

func RecordOpenConnection(ctx context.Context, a Attributes) {
	// mOpenConns.Add(ctx, 1,
	// 	metric.WithAttributeSet(attribute.NewSet(
	// 		attribute.String("project_id", "enocom-experiments-304623"),
	// 		attribute.String("location", "us-central1"),
	// 		attribute.String("cluster", "enocom-cluster"),
	// 		attribute.String("instance", "enocom-primary"),

	// 		attribute.String(connectorType, connectorTypeValue(a.UserAgent)),
	// 		attribute.String(authType, authTypeValue(a.IAMAuthN)),
	// 	)),
	// )
}

func RecordClosedConnection(ctx context.Context, a Attributes) {
	// mOpenConns.Add(ctx, -1,
	// 	metric.WithAttributeSet(attribute.NewSet(
	// 		attribute.String("project_id", "enocom-experiments-304623"),
	// 		attribute.String("location", "us-central1"),
	// 		attribute.String("cluster", "enocom-cluster"),
	// 		attribute.String("instance", "enocom-primary"),

	// 		attribute.String(connectorType, connectorTypeValue(a.UserAgent)),
	// 		attribute.String(authType, authTypeValue(a.IAMAuthN)),
	// 	)),
	// )
}

func RecordBytesTxCount(ctx context.Context, bytes int64, a Attributes) {
	// mBytesTx.Add(ctx, bytes,
	// 	metric.WithAttributeSet(attribute.NewSet(
	// 		attribute.String("project_id", "enocom-experiments-304623"),
	// 		attribute.String("location", "us-central1"),
	// 		attribute.String("cluster", "enocom-cluster"),
	// 		attribute.String("instance", "enocom-primary"),

	// 		attribute.String(connectorType, connectorTypeValue(a.UserAgent)),
	// 	)),
	// )
}

func RecordBytesRxCount(ctx context.Context, bytes int64, a Attributes) {
	// mBytesRx.Add(ctx, bytes,
	// 	metric.WithAttributeSet(attribute.NewSet(
	// 		attribute.String("project_id", "enocom-experiments-304623"),
	// 		attribute.String("location", "us-central1"),
	// 		attribute.String("cluster", "enocom-cluster"),
	// 		attribute.String("instance", "enocom-primary"),

	// 		attribute.String(connectorType, connectorTypeValue(a.UserAgent)),
	// 	)),
	// )
}

func RecordRefreshCount(ctx context.Context, a Attributes) {
	// mRefreshCount.Add(ctx, 1,
	// 	metric.WithAttributeSet(attribute.NewSet(
	// 		attribute.String("project_id", "enocom-experiments-304623"),
	// 		attribute.String("location", "us-central1"),
	// 		attribute.String("cluster", "enocom-cluster"),
	// 		attribute.String("instance", "enocom-primary"),

	// 		attribute.String(connectorType, connectorTypeValue(a.UserAgent)),
	// 		attribute.String(status, a.RefreshStatus),
	// 		attribute.String(refreshType, a.RefreshType),
	// 	)),
	// )
}
