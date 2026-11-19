// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package tel

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

type testLogger struct{}

func (testLogger) Debugf(context.Context, string, ...any) {}

func TestGlobalMetricRecorder(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	recorder := newGlobalMetricRecorder(t.Context(), testLogger{}, provider, Config{
		Enabled:   true,
		Version:   "1.2.3",
		ProjectID: "my-project",
		Location:  "my-location",
		Cluster:   "my-cluster",
		Instance:  "my-instance",
	})
	attrs := Attributes{
		IAMAuthN:      true,
		UserAgent:     "alloydb-go-connector/1.2.3",
		CacheHit:      true,
		DialStatus:    DialSuccess,
		RefreshStatus: RefreshFailure,
		RefreshType:   RefreshLazyType,
	}

	recorder.RecordDialCount(t.Context(), attrs)
	recorder.RecordDialLatency(t.Context(), 12, attrs)
	recorder.RecordOpenConnection(t.Context(), attrs)
	recorder.RecordClosedConnection(t.Context(), attrs)
	recorder.RecordBytesTxCount(t.Context(), 7, attrs)
	recorder.RecordBytesRxCount(t.Context(), 11, attrs)
	recorder.RecordRefreshCount(t.Context(), attrs)

	var got metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &got); err != nil {
		t.Fatal(err)
	}
	metrics := make(map[string]metricdata.Metrics)
	for _, scope := range got.ScopeMetrics {
		if scope.Scope.Name != publicMeterName {
			continue
		}
		for _, m := range scope.Metrics {
			metrics[m.Name] = m
		}
	}

	wantUnits := map[string]string{
		publicDialCount:   "{dial}",
		publicDialLatency: "ms",
		publicOpenConns:   "{connection}",
		publicBytesSent:   "By",
		publicBytesRecv:   "By",
		publicRefresh:     "{refresh}",
	}
	if len(metrics) != len(wantUnits) {
		t.Fatalf("got %d metrics, want %d: %v", len(metrics), len(wantUnits), metrics)
	}
	for name, wantUnit := range wantUnits {
		m, ok := metrics[name]
		if !ok {
			t.Errorf("missing metric %q", name)
			continue
		}
		if m.Unit != wantUnit {
			t.Errorf("metric %q unit = %q, want %q", name, m.Unit, wantUnit)
		}
	}

	wantTarget := map[string]string{
		publicProjectID: "my-project",
		publicLocation:  "my-location",
		publicCluster:   "my-cluster",
		publicInstance:  "my-instance",
	}
	assertInt64Sum(t, metrics[publicDialCount], 1, true, wantTarget, map[string]string{
		publicConnectorType: "go",
		publicAuthType:      "iam",
		publicCacheHit:      "true",
		publicStatus:        DialSuccess,
	})
	assertHistogram(t, metrics[publicDialLatency], 12, wantTarget, map[string]string{
		publicConnectorType: "go",
	})
	assertInt64Sum(t, metrics[publicOpenConns], 0, false, wantTarget, map[string]string{
		publicConnectorType: "go",
		publicAuthType:      "iam",
	})
	assertInt64Sum(t, metrics[publicBytesSent], 7, true, wantTarget, map[string]string{
		publicConnectorType: "go",
	})
	assertInt64Sum(t, metrics[publicBytesRecv], 11, true, wantTarget, map[string]string{
		publicConnectorType: "go",
	})
	assertInt64Sum(t, metrics[publicRefresh], 1, true, wantTarget, map[string]string{
		publicConnectorType: "go",
		publicStatus:        RefreshFailure,
		publicRefreshType:   RefreshLazyType,
	})
}

func TestGlobalMetricRecorderDisabled(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	recorder := newGlobalMetricRecorder(t.Context(), testLogger{}, provider, Config{})
	if _, ok := recorder.(NullMetricRecorder); !ok {
		t.Fatalf("got %T, want %T", recorder, NullMetricRecorder{})
	}
}

func TestGlobalMetricRecorderDistinguishesInstances(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	first := newGlobalMetricRecorder(t.Context(), testLogger{}, provider, Config{
		Enabled:   true,
		ProjectID: "project",
		Location:  "location",
		Cluster:   "cluster",
		Instance:  "first",
	})
	second := newGlobalMetricRecorder(t.Context(), testLogger{}, provider, Config{
		Enabled:   true,
		ProjectID: "project",
		Location:  "location",
		Cluster:   "cluster",
		Instance:  "second",
	})
	first.RecordDialCount(t.Context(), Attributes{DialStatus: DialSuccess})
	second.RecordDialCount(t.Context(), Attributes{DialStatus: DialSuccess})

	var got metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &got); err != nil {
		t.Fatal(err)
	}
	var points []metricdata.DataPoint[int64]
	for _, scope := range got.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name == publicDialCount {
				points = metric.Data.(metricdata.Sum[int64]).DataPoints
			}
		}
	}
	if len(points) != 2 {
		t.Fatalf("got %d dial count points, want 2", len(points))
	}
	instances := map[string]bool{}
	for _, point := range points {
		value, ok := point.Attributes.Value(attribute.Key(publicInstance))
		if !ok {
			t.Fatal("dial count point missing AlloyDB instance attribute")
		}
		instances[value.AsString()] = true
	}
	if !instances["first"] || !instances["second"] {
		t.Fatalf("got instance attributes %v, want first and second", instances)
	}
}

func TestMultiMetricRecorder(t *testing.T) {
	first := &recordingMetricRecorder{}
	second := &recordingMetricRecorder{shutdownErr: errors.New("second")}
	recorder := NewMultiMetricRecorder(first, nil, second)
	recorder.RecordDialCount(t.Context(), Attributes{})
	if first.dialCount != 1 || second.dialCount != 1 {
		t.Fatalf("dial count = (%d, %d), want (1, 1)", first.dialCount, second.dialCount)
	}
	if !errors.Is(recorder.Shutdown(t.Context()), second.shutdownErr) {
		t.Fatal("Shutdown error did not include recorder error")
	}
	if first.shutdowns != 1 || second.shutdowns != 1 {
		t.Fatalf("shutdowns = (%d, %d), want (1, 1)", first.shutdowns, second.shutdowns)
	}
}

type recordingMetricRecorder struct {
	shutdownErr error
	shutdowns   int
	dialCount   int
}

func (r *recordingMetricRecorder) Shutdown(context.Context) error {
	r.shutdowns++
	return r.shutdownErr
}
func (*recordingMetricRecorder) RecordBytesRxCount(context.Context, int64, Attributes) {}
func (*recordingMetricRecorder) RecordBytesTxCount(context.Context, int64, Attributes) {}
func (r *recordingMetricRecorder) RecordDialCount(context.Context, Attributes) {
	r.dialCount++
}
func (*recordingMetricRecorder) RecordDialLatency(context.Context, int64, Attributes) {}
func (*recordingMetricRecorder) RecordOpenConnection(context.Context, Attributes)     {}
func (*recordingMetricRecorder) RecordClosedConnection(context.Context, Attributes)   {}
func (*recordingMetricRecorder) RecordRefreshCount(context.Context, Attributes)       {}

func assertInt64Sum(t *testing.T, metric metricdata.Metrics, wantValue int64, wantMonotonic bool, target, labels map[string]string) {
	t.Helper()
	sum, ok := metric.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("metric %q data type = %T, want metricdata.Sum[int64]", metric.Name, metric.Data)
	}
	if sum.IsMonotonic != wantMonotonic {
		t.Errorf("metric %q monotonic = %t, want %t", metric.Name, sum.IsMonotonic, wantMonotonic)
	}
	if len(sum.DataPoints) != 1 {
		t.Fatalf("metric %q has %d data points, want 1", metric.Name, len(sum.DataPoints))
	}
	if got := sum.DataPoints[0].Value; got != wantValue {
		t.Errorf("metric %q value = %d, want %d", metric.Name, got, wantValue)
	}
	assertAttributes(t, metric.Name, sum.DataPoints[0].Attributes, target, labels)
}

func assertHistogram(t *testing.T, metric metricdata.Metrics, wantSum float64, target, labels map[string]string) {
	t.Helper()
	histogram, ok := metric.Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("metric %q data type = %T, want metricdata.Histogram[float64]", metric.Name, metric.Data)
	}
	if len(histogram.DataPoints) != 1 {
		t.Fatalf("metric %q has %d data points, want 1", metric.Name, len(histogram.DataPoints))
	}
	if got := histogram.DataPoints[0].Sum; got != wantSum {
		t.Errorf("metric %q sum = %v, want %v", metric.Name, got, wantSum)
	}
	assertAttributes(t, metric.Name, histogram.DataPoints[0].Attributes, target, labels)
}

func assertAttributes(t *testing.T, metricName string, got attribute.Set, target, labels map[string]string) {
	t.Helper()
	want := make(map[string]string, len(target)+len(labels))
	for key, value := range target {
		want[key] = value
	}
	for key, value := range labels {
		want[key] = value
	}
	actual := make(map[string]string, len(got.ToSlice()))
	for _, value := range got.ToSlice() {
		actual[string(value.Key)] = value.Value.Emit()
	}
	if len(actual) != len(want) {
		t.Errorf("metric %q attributes = %v, want %v", metricName, actual, want)
		return
	}
	for key, wantValue := range want {
		if actual[key] != wantValue {
			t.Errorf("metric %q attribute %q = %q, want %q", metricName, key, actual[key], wantValue)
		}
	}
}
