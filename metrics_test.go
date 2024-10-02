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
	"strings"
	"sync"
	"testing"
	"time"

	alloydbadmin "cloud.google.com/go/alloydb/apiv1alpha"
	"cloud.google.com/go/alloydbconn/internal/mock"
	"go.opencensus.io/stats/view"
	"google.golang.org/api/option"
)

type spyMetricsExporter struct {
	mu       sync.Mutex
	viewData []*view.Data
}

func (e *spyMetricsExporter) ExportView(vd *view.Data) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.viewData = append(e.viewData, vd)
}

type metric struct {
	name string
	data view.AggregationData
}

func (e *spyMetricsExporter) data() []metric {
	e.mu.Lock()
	defer e.mu.Unlock()
	var res []metric
	for _, d := range e.viewData {
		for _, r := range d.Rows {
			res = append(res, metric{name: d.View.Name, data: r.Data})
		}
	}
	return res
}

// dump marshals a value to JSON for better test reporting
func dump[T any](t *testing.T, data T) string {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprint(string(b))
}

// wantLastValueMetric ensures the provided metrics include a metric with the
// wanted name and wanted value.
func wantLastValueMetric(t *testing.T, wantName string, ms []metric, wantValue int) {
	t.Helper()
	gotNames := make(map[string]view.AggregationData)
	for _, m := range ms {
		if strings.Contains(m.name, "open_connections") {
			fmt.Printf("RISHABH DEBUG calling wantLastValueMetric() => name: %v, value: %v\n", m.name, m.data)
		}
		gotNames[m.name] = m.data
	}
	ad, ok := gotNames[wantName]
	if ok {
		lvd, ok := ad.(*view.LastValueData)
		if ok {
			if lvd.Value == float64(wantValue) {
				return
			}
		}
	}
	t.Fatalf(
		"want metric LastValueData{name = %q, value = %v}, got metrics = %v",
		wantName, wantValue, dump(t, gotNames),
	)
}

// wantDistributionMetric ensures the provided metrics include a metric with
// the wanted name and at least one data point.
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
	t.Fatalf(
		"metric name want = %v with DistributionData, all metrics = %v",
		wantName, dump(t, gotNames),
	)
}

// wantCountMetric ensures the provided metrics include a metric with the
// wanted name and at least one data point.
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
	t.Fatalf(
		"metric name want = %v with CountData, all metrics = %v",
		wantName, dump(t, gotNames),
	)
}

// wantSumMetric ensures the provided metrics include a metric with the wanted
// name and at least one data point.
func wantSumMetric(t *testing.T, wantName string, ms []metric) {
	t.Helper()
	gotNames := make(map[string]view.AggregationData)
	for _, m := range ms {
		gotNames[m.name] = m.data
		_, ok := m.data.(*view.SumData)
		if m.name == wantName && ok {
			return
		}
	}
	t.Fatalf(
		"metric name want = %v with SumData, all metrics = %v",
		wantName, dump(t, gotNames),
	)
}

func TestDialerWithMetrics(t *testing.T) {
	fmt.Printf("RISHABH DEBUG: TestDialerWithMetrics\n")
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
	c, err := alloydbadmin.NewAlloyDBAdminRESTClient(
		ctx, option.WithHTTPClient(mc), option.WithEndpoint(url),
	)
	if err != nil {
		t.Fatalf("expected NewClient to succeed, but got error: %v", err)
	}

	d, err := NewDialer(ctx, WithTokenSource(stubTokenSource{}))
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
	// dial a second time to ensure the counter is working
	conn2, err := d.Dial(ctx, testInstanceURI)
	if err != nil {
		t.Fatalf("expected Dial to succeed, but got error: %v", err)
	}
	// write to conn to test bytes_sent and bytes_received
	buf := &bytes.Buffer{}
	err = buf.WriteByte('a')
	if err != nil {
		t.Fatalf("buf.WriteByte failed: %v", err)
	}
	// Doing a read before doing a write, because when this unit test runs on
	// Windows, it fails when the write is done before the read.
	_, err = conn2.Read(buf.Bytes())
	if err != nil {
		t.Fatalf("conn.Read failed: %v", err)
	}
	_, err = conn2.Write(buf.Bytes())
	if err != nil {
		t.Fatalf("conn.Write failed: %v", err)
	}
	defer conn2.Close()
	// dial a bogus instance
	_, err = d.Dial(ctx,
		"projects/my-project/locations/my-region/clusters/"+
			"my-cluster/instances/notaninstance",
	)
	if err == nil {
		t.Fatal("expected Dial to fail, but got no error")
	}

	time.Sleep(100 * time.Millisecond) // allow exporter a chance to run

	// success metrics
	wantLastValueMetric(t, "alloydbconn/open_connections", spy.data(), 2)
	wantDistributionMetric(t, "alloydbconn/dial_latency", spy.data())
	wantCountMetric(t, "alloydbconn/refresh_success_count", spy.data())
	wantSumMetric(t, "alloydbconn/bytes_sent", spy.data())
	wantSumMetric(t, "alloydbconn/bytes_received", spy.data())

	// failure metrics from dialing bogus instance
	wantCountMetric(t, "alloydbconn/dial_failure_count", spy.data())
	wantCountMetric(t, "alloydbconn/refresh_failure_count", spy.data())

	fmt.Printf("RISHABH DEBUG: exiting out of test\n")
}
