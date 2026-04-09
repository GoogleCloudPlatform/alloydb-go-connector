// Copyright 2022 Google LLC
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
package alloydbconn

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	alloydbadmin "cloud.google.com/go/alloydb/apiv1alpha"
	"cloud.google.com/go/alloydbconn/internal/mock"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/api/option"
)

// dump marshals a value to JSON for better test reporting.
func dump[T any](t *testing.T, data T) string {
	t.Helper()
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprint(string(b))
}

// collectMetrics gathers all metrics that have been recorded into the
// supplied manual reader.
func collectMetrics(t *testing.T, r *metric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := r.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("ManualReader.Collect failed: %v", err)
	}
	return rm
}

// findMetric returns the metricdata.Metrics with the given name, or nil.
func findMetric(rm metricdata.ResourceMetrics, name string) *metricdata.Metrics {
	for _, sm := range rm.ScopeMetrics {
		for i := range sm.Metrics {
			if sm.Metrics[i].Name == name {
				return &sm.Metrics[i]
			}
		}
	}
	return nil
}

// wantSumMetric asserts that a Sum metric with the given name has been
// recorded with at least one data point.
func wantSumMetric[N int64 | float64](t *testing.T, name string, rm metricdata.ResourceMetrics) {
	t.Helper()
	m := findMetric(rm, name)
	if m == nil {
		t.Fatalf("metric %q not found, all metrics = %v", name, dump(t, rm))
	}
	sum, ok := m.Data.(metricdata.Sum[N])
	if !ok {
		t.Fatalf("metric %q is %T, want Sum[%T]", name, m.Data, *new(N))
	}
	if len(sum.DataPoints) == 0 {
		t.Fatalf("metric %q has no data points", name)
	}
}

// wantHistogramMetric asserts that a Histogram metric with the given name
// has been recorded with at least one data point.
func wantHistogramMetric(t *testing.T, name string, rm metricdata.ResourceMetrics) {
	t.Helper()
	m := findMetric(rm, name)
	if m == nil {
		t.Fatalf("metric %q not found, all metrics = %v", name, dump(t, rm))
	}
	if _, ok := m.Data.(metricdata.Histogram[float64]); !ok {
		t.Fatalf("metric %q is %T, want Histogram[float64]", name, m.Data)
	}
}

// wantStatusOnMetric asserts that the named Sum metric has at least one
// data point whose status attribute matches wantStatus.
func wantStatusOnMetric(t *testing.T, name, wantStatus string, rm metricdata.ResourceMetrics) {
	t.Helper()
	m := findMetric(rm, name)
	if m == nil {
		t.Fatalf("metric %q not found, all metrics = %v", name, dump(t, rm))
	}
	sum, ok := m.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("metric %q is %T, want Sum[int64]", name, m.Data)
	}
	for _, dp := range sum.DataPoints {
		if v, ok := dp.Attributes.Value("status"); ok && v.AsString() == wantStatus {
			return
		}
	}
	t.Fatalf("metric %q has no data point with status=%q, got = %v", name, wantStatus, dump(t, sum))
}

func TestDialerWithMetrics(t *testing.T) {
	// Register a manual reader on the global MeterProvider so we can collect
	// the public metrics that the connector records.
	reader := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(reader))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(mp)
	t.Cleanup(func() {
		otel.SetMeterProvider(prev)
		_ = mp.Shutdown(context.Background())
	})

	ctx := context.Background()
	inst := mock.NewFakeInstance(
		"my-project", "my-region", "my-cluster", "my-instance",
	)
	mc, url, cleanup := mock.HTTPClient(
		mock.InstanceGetSuccess(inst, 1),
		mock.CreateEphemeralSuccess(inst, 1),
	)
	stop := mock.StartServerProxy(t, inst)
	defer func() {
		stop()
		if err := cleanup(); err != nil {
			t.Fatalf("%v", err)
		}
	}()
	c, err := alloydbadmin.NewAlloyDBAdminRESTClient(
		ctx, option.WithHTTPClient(mc), option.WithEndpoint(url),
	)
	if err != nil {
		t.Fatalf("expected NewClient to succeed, but got error: %v", err)
	}

	// Disable the GCP system-metric export so the test doesn't try to
	// reach Cloud Monitoring. The public metrics still flow through the
	// global meter provider.
	d, err := NewDialer(ctx, WithTokenSource(stubTokenSource{}), WithOptOutOfBuiltInTelemetry())
	if err != nil {
		t.Fatalf("expected NewDialer to succeed, but got error: %v", err)
	}
	d.client = c

	// dial a good instance
	conn, err := d.Dial(ctx, testInstanceURI)
	if err != nil {
		t.Fatalf("expected Dial to succeed, but got error: %v", err)
	}
	defer conn.Close()
	// dial a second time to ensure counters increment
	conn2, err := d.Dial(ctx, testInstanceURI)
	if err != nil {
		t.Fatalf("expected Dial to succeed, but got error: %v", err)
	}
	// write to conn to test bytes_sent and bytes_received
	buf := &bytes.Buffer{}
	if err := buf.WriteByte('a'); err != nil {
		t.Fatalf("buf.WriteByte failed: %v", err)
	}
	// Doing a read before doing a write, because when this unit test runs on
	// Windows, it fails when the write is done before the read.
	if _, err := conn2.Read(buf.Bytes()); err != nil {
		t.Fatalf("conn.Read failed: %v", err)
	}
	if _, err := conn2.Write(buf.Bytes()); err != nil {
		t.Fatalf("conn.Write failed: %v", err)
	}
	// Closing conn2 forces a flush of the bytes counters before collection.
	if err := conn2.Close(); err != nil {
		t.Fatalf("conn2.Close failed: %v", err)
	}

	// dial a bogus instance to drive the failure metrics
	_, err = d.Dial(ctx,
		"projects/my-project/locations/my-region/clusters/"+
			"my-cluster/instances/notaninstance",
	)
	if err == nil {
		t.Fatal("expected Dial to fail, but got no error")
	}

	// Several metrics are recorded from background goroutines (e.g.
	// RecordDialCount, RecordOpenConnection); give them a moment to run
	// before collecting.
	time.Sleep(100 * time.Millisecond)

	rm := collectMetrics(t, reader)

	// success metrics
	wantSumMetric[int64](t, "alloydbconn.open_connections", rm)
	wantHistogramMetric(t, "alloydbconn.dial_latencies", rm)
	wantStatusOnMetric(t, "alloydbconn.refresh_count", "success", rm)
	wantSumMetric[int64](t, "alloydbconn.bytes_sent_count", rm)
	wantSumMetric[int64](t, "alloydbconn.bytes_received_count", rm)

	// failure metrics from dialing the bogus instance
	wantStatusOnMetric(t, "alloydbconn.dial_count", "cache_error", rm)
	wantStatusOnMetric(t, "alloydbconn.refresh_count", "failure", rm)
}
