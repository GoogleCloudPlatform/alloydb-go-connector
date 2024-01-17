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
	"context"
	"sync"
	"testing"
	"time"

	alloydbadmin "cloud.google.com/go/alloydb/apiv1alpha"
	"cloud.google.com/go/alloydbconn/internal/mock"
	"go.opencensus.io/stats/view"
	"google.golang.org/api/option"
)

type spyMetricsExporter struct {
	mu   sync.Mutex
	data []*view.Data
}

func (e *spyMetricsExporter) ExportView(vd *view.Data) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.data = append(e.data, vd)
}

type metric struct {
	name string
	data view.AggregationData
}

func (e *spyMetricsExporter) Data() []metric {
	e.mu.Lock()
	defer e.mu.Unlock()
	var res []metric
	for _, d := range e.data {
		for _, r := range d.Rows {
			res = append(res, metric{name: d.View.Name, data: r.Data})
		}
	}
	return res
}

// wantLastValueMetric ensures the provided metrics include a metric with the
// wanted name and at least data point.
func wantLastValueMetric(t *testing.T, wantName string, ms []metric) {
	t.Helper()
	gotNames := make(map[string]view.AggregationData)
	for _, m := range ms {
		gotNames[m.name] = m.data
		_, ok := m.data.(*view.LastValueData)
		if m.name == wantName && ok {
			return
		}
	}
	t.Fatalf("metric name want = %v with LastValueData, all metrics = %#v", wantName, gotNames)
}

// wantDistributionMetric ensures the provided metrics include a metric with the
// wanted name and at least one data point.
func wantDistributionMetric(t *testing.T, wantName string, ms []metric) {
	t.Helper()
	gotNames := make(map[string]view.AggregationData)
	for _, m := range ms {
		gotNames[m.name] = m.data
		_, ok := m.data.(*view.DistributionData)
		if m.name == wantName && ok {
			return
		}
	}
	t.Fatalf("metric name want = %v with DistributionData, all metrics = %#v", wantName, gotNames)
}

// wantCountMetric ensures the provided metrics include a metric with the wanted
// name and at least one data point.
func wantCountMetric(t *testing.T, wantName string, ms []metric) {
	t.Helper()
	gotNames := make(map[string]view.AggregationData)
	for _, m := range ms {
		gotNames[m.name] = m.data
		_, ok := m.data.(*view.CountData)
		if m.name == wantName && ok {
			return
		}
	}
	t.Fatalf("metric name want = %v with CountData, all metrics = %#v", wantName, gotNames)
}

func TestDialerWithMetrics(t *testing.T) {
	spy := &spyMetricsExporter{}
	view.RegisterExporter(spy)
	defer view.UnregisterExporter(spy)
	view.SetReportingPeriod(time.Millisecond)

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
	c, err := alloydbadmin.NewAlloyDBAdminRESTClient(ctx, option.WithHTTPClient(mc), option.WithEndpoint(url))
	if err != nil {
		t.Fatalf("expected NewClient to succeed, but got error: %v", err)
	}

	d, err := NewDialer(ctx, WithTokenSource(stubTokenSource{}))
	if err != nil {
		t.Fatalf("expected NewDialer to succeed, but got error: %v", err)
	}
	d.client = c

	// dial a good instance
	conn, err := d.Dial(ctx, "/projects/my-project/locations/my-region/clusters/my-cluster/instances/my-instance")
	if err != nil {
		t.Fatalf("expected Dial to succeed, but got error: %v", err)
	}
	defer conn.Close()
	// dial a bogus instance
	_, err = d.Dial(ctx, "/projects/my-project/locations/my-region/clusters/my-cluster/instances/notaninstance")
	if err == nil {
		t.Fatal("expected Dial to fail, but got no error")
	}

	time.Sleep(100 * time.Millisecond) // allow exporter a chance to run

	// success metrics
	wantLastValueMetric(t, "alloydbconn/open_connections", spy.Data())
	wantDistributionMetric(t, "alloydbconn/dial_latency", spy.Data())
	wantCountMetric(t, "alloydbconn/refresh_success_count", spy.Data())

	// failure metrics from dialing bogus instance
	wantCountMetric(t, "alloydbconn/dial_failure_count", spy.Data())
	wantCountMetric(t, "alloydbconn/refresh_failure_count", spy.Data())
}
